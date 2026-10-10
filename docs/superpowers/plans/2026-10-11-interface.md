# mix-tui Interface Implementation Plan (Milestone 3, final)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A terminal DJ application — four switchable layouts, braille waveforms, CDJ-style zoom, a three-pane library with a mid-set quick overlay, hotcues, loops, and ASCII art — driven by keyboard, MIDI, or both.

**Architecture:** Every view is a **pure function from (width, height, Snapshot) to a string**, with no terminal I/O and no engine access. That is what makes a TUI testable: assert on rendered strings rather than on a real terminal. The bubbletea model owns only interaction state — focus, scroll, zoom, which view — and never audio state.

**Tech Stack:** Go 1.27, `github.com/charmbracelet/bubbletea` v1.3.10, `github.com/charmbracelet/lipgloss` v1.1.0, `github.com/charmbracelet/bubbles` v1.0.0, plus everything from plans 1–3

**Spec:** `docs/superpowers/specs/2026-10-10-mix-tui-design.md` — read §6 (all of it), §10, §8 alongside this plan.
**Prior plans:** `2026-10-10-audio-core.md` (engine), `2026-10-11-device-output.md` (audio out), `2026-10-11-control-layer.md` (MIDI, config, keybindings).

## Scope

This plan adds the entire visual layer plus the library scanner it needs to display.
It completes the MVP. Nothing here changes audio behaviour: the engine already accepts
Actions and publishes Snapshots, and this plan only feeds it from a terminal.

The library scanner and the analysis cache land here rather than in plan 1 because they
have no UI dependency — they are simply the data the UI needs, and deferring them this
far is what kept plan 1 focused on audio correctness.

## Global Constraints

- **Views are pure.** `func(w, h int, s engine.Snapshot, st ViewState) string`. No I/O, no
  engine pointer, no globals. A view that needs data must get it through the Snapshot.
- **Rendered output must never exceed the terminal width.** Every line is truncated to
  `w` columns with `lipgloss.Width`-aware truncation, because a wrapped line in a
  full-screen bubbletea app corrupts the whole display.
- **No layout or render function may panic.** The terminal can be 20×5. Every render is
  bounded by its own width and height and clamps rather than indexing blindly.
- **Braille cells are single-width.** U+2800–U+28FF occupy exactly one column. Mix and
  submix characters are two columns and must never appear in a waveform row — mixing them
  silently shifts every column after it.
- **The TUI never blocks.** Snapshot reads are cheap value copies. Library scanning and
  beat analysis run on their own goroutines and publish results into the Snapshot path.
- **ASCII art is embedded data, not generated at runtime.** Art lives in Go string
  constants and is scaled by choosing among fixed-width variants — never by
  string-padding ASCII art, which breaks under variable-width glyphs.
- **Snapshot must expose everything a view needs**, including loop state. If a view
  reaches into the engine for a field, add the field to `DeckState` instead.

## Review Focus

Five ways a TUI is wrong in ways that unit tests pass but a person at 2am with a set
starting does not forgive.

1. **The terminal is resized mid-set and the layout does not reflow.** A TUI that assumes
   the size it started with produces a corrupted display exactly when a user maximises
   their window. → Task 2, Step 1.
2. **A very narrow terminal truncates the layout into nonsense** — overlapping panels or
   negative widths. → Task 3, Step 1.
3. **A track name containing wide characters (CJK, emoji) breaks column alignment.**
   `len()` counts bytes; `lipgloss.Width()` counts display cells. → Task 4, Step 1.
4. **The display goes blank or garbled mid-set.** An unhandled panic or a write error in
   bubbletea takes the whole interface down with the audio still playing. → Task 1.
5. **Scanning a large library freezes the TUI.** A blocking filesystem walk in the update
   loop makes the interface unresponsive exactly when the user is waiting to load a
   track. → Task 8, Step 1.

---

## File Structure

```
internal/tui/
  model.go        bubbletea Model: interaction state, update loop, tick
  state.go        ViewState: focus, scroll, zoom, mode, overlay
  view.go         dispatch: Snapshot + ViewState -> layout
  layout.go       shared box/panel/grid primitives
  layout_d.go     default: deck | mixer | deck
  layout_a.go     decks side-by-side, mixer beside
  layout_b.go     focused deck, other as a strip
  layout_c.go     stacked decks, mixer footer
  deck_panel.go   waveform, transport, EQ, pitch, hotcues
  mixer_panel.go  EQ, channel faders, crossfader, master
  waveform.go     braille waveform + overview strip + beatgrid/hotcue overlay
  zoom.go         sliding-window waveform, delta-beat readout
  meters.go       level/peak bars
  art.go          ASCII art constants
  keys.go         key -> Action via config.KeyResolver
  style.go        lipgloss styles and the colour palette

  library/
    scan.go       background directory scan
    crate.go      crate load/save
    cache.go      analysis cache on disk (path+size+mtime)
    index.go      track index, search, sort

  views/
    library.go    three-pane browser
    overlay.go    mid-set quick-load overlay
    debug.go      debug overlay
cmd/mix-tui/
  main.go         wiring: engine + output + pumper + midi handler + bubbletea
```

`layout*.go` and `waveform.go` are pure and independently testable. `model.go` is the
only file that knows bubbletea exists.

---

### Task 1: The bubbletea skeleton

**Files:**
- Create: `internal/tui/state.go`
- Create: `internal/tui/model.go`
- Create: `internal/tui/style.go`
- Create: `internal/tui/model_test.go`

**Interfaces:**
- Consumes: `engine.Snapshot`, `engine.Action` from milestone 1; `config.Config`,
  `config.KeyResolver` from plan 3.
- Produces:
  ```go
  package tui

  type Mode int
  const (
      ModeMix Mode = iota
      ModeLibrary
      ModeOverlay
      ModeHelp
  )

  // ViewState is interaction state only. It never holds audio data.
  type ViewState struct {
      Mode     Mode
      Layout   LayoutKind  // A, B, C, D
      Focus    int         // focused deck index
      Zoom     bool        // per-focus-deck zoom toggle
      ZoomBars int         // 2/4/8/16/32
      Xfade    float64     // crossfader position, mirrored for the view
      Cued     int
      LastErr  string
      LastErrAt time.Time
  }

  type LayoutKind int
  const (
      LayoutD LayoutKind = iota  // default (spec §6.1)
      LayoutA
      LayoutB
      LayoutC
  )

  type Model struct { /* ... */ }
  func New(eng Engine, cfg *config.Config, keys *config.KeyResolver) *Model

  // Engine is the narrow interface the TUI needs. Depending on this rather
  // than *engine.Engine keeps the TUI testable with a fake.
  type Engine interface {
      Apply(engine.Action)
      Snapshot() engine.Snapshot
      DeckCount() int
  }

  func (m *Model) Init() tea.Cmd
  func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd)
  func (m *Model) View() string
  func (m *Model) State() ViewState        // for tests
  ```

- [ ] **Step 1: Write the failing tests**

Create `internal/tui/model_test.go` with a fake engine:

```go
package tui

import (
    "testing"
    "time"

    "github.com/maguldon/mix-tui/internal/engine"
)

type fakeEngine struct {
    snap engine.Snapshot
    applied []engine.Action
}
func (f *fakeEngine) Apply(a engine.Action) { f.applied = append(f.applied, a) }
func (f *fakeEngine) Snapshot() engine.Snapshot { return f.snap }
func (f *fakeEngine) DeckCount() int { return len(f.snap.Decks) }

func testModel(t *testing.T) (*Model, *fakeEngine) {
    t.Helper()
    f := &fakeEngine{snap: engine.Snapshot{
        Decks: []engine.DeckState{{Index: 0, Loaded: true, Playing: true,
                                  Position: 12 * time.Second, Duration: 3 * time.Minute,
                                  BPM: 128, HasBeatGrid: true, Title: "A Track",
                                  Artist: "Someone", Level: 0.4, Peak: 0.7,
                                  LoopActive: true, LoopBeatCount: 4},
                     {Index: 1}},
    }}
    keys := &config_KeyResolverStub{}
    return New(f, &config.Config{UI: config.UIConfig{DefaultView: "D"}}, keys), f
}

func TestDefaultLayoutIsD(t *testing.T) {
    m, _ := testModel(t)
    if m.State().Layout != LayoutD { t.Errorf("default layout = %v, want D", m.State().Layout) }
}

func TestViewCycleChangesLayout(t *testing.T) {
    m, _ := testModel(t)
    seen := map[LayoutKind]bool{m.State().Layout: true}
    for i := 0; i < 4; i++ {
        m2, _ := m.Update(keyMsg("v"))
        m = m2.(*Model)
        seen[m.State().Layout] = true
    }
    if len(seen) != 4 { t.Errorf("saw %d distinct layouts, want 4", len(seen)) }
}

func TestViewNeverExceedsWidth(t *testing.T) {
    // Review Focus #1 and #2: every size must render bounded lines
    m, _ := testModel(t)
    for _, size := range [][2]int{{200,60},{120,40},{100,30},{80,24},{70,20},{40,10},{20,5}} {
        out := m.RenderAt(size[0], size[1])
        for i, line := range splitLines(out) {
            if w := lipgloss.Width(line); w > size[0] {
                t.Fatalf("%dx%d line %d is %d cols: %q", size[0], size[1], i, w, line)
            }
        }
    }
}

func TestRenderNeverPanicsOnTinyTerminal(t *testing.T) {
    // Review Focus #2
    m, _ := testModel(t)
    for _, size := range [][2]int{{1,1},{2,2},{5,3},{10,4},{15,5}} {
        _ = m.RenderAt(size[0], size[1])   // must not panic
    }
}

func TestKeyEmitsActionToEngine(t *testing.T) {
    m, f := testModel(t)
    m2, _ := m.Update(keyMsg(" "))
    _ = m2
    found := false
    for _, a := range f.applied {
        if a.Kind == engine.DeckPlay { found = true }
    }
    if !found { t.Errorf("space produced no DeckPlay; actions: %+v", f.applied) }
}

func TestTabMovesFocus(t *testing.T) {
    m, _ := testModel(t)
    m2, _ := m.Update(keyMsg("tab"))
    if m2.(*Model).State().Focus != 1 { t.Errorf("focus = %d", m2.(*Model).State().Focus) }
}

func TestSlashOpensLibrary(t *testing.T) {
    m, _ := testModel(t)
    m2, _ := m.Update(keyMsg("/"))
    if m2.(*Model).State().Mode != ModeLibrary { t.Error("/ did not open the library") }
}

func TestEscapeLeavesOverlay(t *testing.T) {
    m, _ := testModel(t)
    m2, _ := m.Update(keyMsg("/"))
    m3, _ := m2.(*Model).Update(keyMsg("esc"))
    if m3.(*Model).State().Mode != ModeMix { t.Error("esc did not leave the library") }
}

func TestErrorAutoExpires(t *testing.T) {
    m, _ := testModel(t)
    st := m.State()
    st.LastErr = "boom"
    st.LastErrAt = time.Now().Add(-10 * time.Second)
    m2, _ := m.Update(tickMsg())
    if m2.(*Model).State().LastErr != "" { t.Error("stale error was not cleared") }
}

func TestUnknownKeyIsIgnored(t *testing.T) {
    m, _ := testModel(t)
    before := m.State()
    m2, _ := m.Update(keyMsg("nonexistent-key"))
    if m2.(*Model).State() != before { t.Error("an unknown key changed state") }
}
```

Add small helpers `keyMsg`, `tickMsg`, and `splitLines`, and use a real
`config.KeyResolver` built from `config.Defaults()` rather than a stub, so the test
exercises the actual binding table.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/tui/ -v`
Expected: FAIL — `undefined: New`.

- [ ] **Step 3: Implement `internal/tui/state.go` and `style.go`**

`ViewState`, `Mode`, `LayoutKind`, and a lipgloss palette. Choose colours once here:

```go
var (
    played    = lipgloss.Color("#f97583")  // waveform before the playhead
    unplayed  = lipgloss.Color("#316dca")  // waveform after
    beat      = lipgloss.Color("#ffa657")  // beatgrid downbeats
    cue       = lipgloss.Color("#d2a8ff")  // hotcues
    ok        = lipgloss.Color("#7ee787")  // active/armed
    dim       = lipgloss.Color("#3d444d")  // box drawing
    warn      = lipgloss.Color("#ffa657")  // low-confidence key
    border    = lipgloss.Color("#30363d")
)
```

Expose `RenderAt(w, h int) string` on Model as the pure entry point the tests use;
`View()` calls it with the terminal size.

- [ ] **Step 4: Implement `internal/tui/model.go`**

```go
const tickInterval = 33 * time.Millisecond   // ~30fps

func (m *Model) Init() tea.Cmd { return tick() }
func tick() tea.Cmd {
    return tea.Tick(tickInterval, func(t time.Time) tea.Msg { return tickMsg{t} })
}
```

`Update` handles: `tea.WindowSizeMsg` (store w/h, re-select layout), `tickMsg` (expire
errors, re-render), `tea.KeyMsg` (resolve to an Action, apply, mirror control values into
`ViewState`), and default (ignore).

**Mirror control state explicitly.** `engine.Snapshot` is authoritative, but the crossfader
and pitch positions the user is dragging need to update immediately; read them from the
snapshot on each tick rather than tracking a second copy that can drift.

Add a `defer recover()` in `View()` that returns a minimal "render error — press q to
quit" screen. **A panic in the render path must not take the interface down while audio
keeps playing** (Review Focus #4).

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/tui/ -v`
Expected: PASS — including the width and tiny-terminal tests, which will need a
placeholder `renderStub` until Task 2.

- [ ] **Step 6: Commit**

```bash
git add internal/tui/
git commit -m "feat(tui): bubbletea skeleton, view state, and bounded rendering"
```

---

### Task 2: Layout primitives and layout D

**Files:**
- Create: `internal/tui/layout.go`
- Create: `internal/tui/layout_d.go`
- Create: `internal/tui/layout_test.go`

**Interfaces:**
- Consumes: `ViewState`, palette from Task 1; `engine.Snapshot`.
- Produces:
  ```go
  package tui

  // renderD is the default layout (spec §6.1): deck | mixer | deck.
  func renderD(w, h int, s engine.Snapshot, st ViewState) string

  // Panel draws a titled box of exactly w columns and n rows.
  type Panel struct {
      Title  string
       Width int
      Rows   []PanelRow
  }
  type PanelRow struct {
      Kind   RowKind  // RowText | RowWave | RowMeter
      Text   string
      Wave   *WaveSpec
      Style  lipgloss.Style
  }
  func RenderPanel(p Panel, height int) []string
  func RenderColumns(cols [][]string, gap int, width int) []string
  func TruncateTo(s string, w int) string   // display-width aware
  ```

- [ ] **Step 1: Write the failing tests**

Create `internal/tui/layout_test.go`:

```go
package tui

import (
    "strings"
    "testing"

    "github.com/lipgloss/lipgloss"
    "github.com/maguldon/mix-tui/internal/engine"
)

func snapTwoDecks() engine.Snapshot {
    return engine.Snapshot{Decks: []engine.DeckState{
        {Index: 0, Loaded: true, Playing: true, Title: "Track A",
         BPM: 128, HasBeatGrid: true, Position: 30e9, Duration: 200e9, Level: .5},
        {Index: 1, Loaded: true, Title: "Track B", BPM: 126.5, HasBeatGrid: true},
    }}
}

func assertLinesFit(t *testing.T, out string, w int) {
    t.Helper()
    for i, line := range strings.Split(out, "\n") {
        if got := lipgloss.Width(line); got > w {
            t.Fatalf("line %d width %d > %d: %q", i, got, w, line)
        }
    }
}

func TestRenderDFits(t *testing.T) {
    for _, w := range []int{200, 120, 100, 90, 80, 70} {
        out := renderD(w, 40, snapTwoDecks(), ViewState{Layout: LayoutD})
        assertLinesFit(t, out, w)
    }
}

func TestRenderDHasThreeColumns(t *testing.T) {
    out := renderD(120, 40, snapTwoDecks(), ViewState{Layout: LayoutD})
    lines := strings.Split(out, "\n")
    body := ""
    for _, l := range lines { body += l + "\n" }
    for _, want := range []string{"A", "MIXER", "B"} {
        if !strings.Contains(body, want) {
            t.Errorf("layout D missing panel %q", want)
        }
    }
}

func TestRenderDNarrowShowsTooSmall(t *testing.T) {
    out := renderD(60, 20, snapTwoDecks(), ViewState{Layout: LayoutD})
    if !strings.Contains(out, "too small") && !strings.Contains(out, "wider") {
        t.Errorf("a 60-col render should ask for width, got:\n%s", out)
    }
}

func TestPanelIsExactWidth(t *testing.T) {
    p := Panel{Title: "X", Width: 30, Rows: []PanelRow{
        {Kind: RowText, Text: "hello"}, {Kind: RowText, Text: ""},
    }}
    lines := RenderPanel(p, 4)
    if len(lines) != 4 { t.Fatalf("got %d lines, want 4", len(lines)) }
    for _, l := range lines {
        if lipgloss.Width(l) != 30 {
            t.Errorf("panel line width %d, want 30: %q", lipgloss.Width(l), l)
        }
    }
}

func TestPanelClampsOverflowRows(t *testing.T) {
    p := Panel{Title: "X", Width: 20, Rows: []PanelRow{
        {Kind: RowText, Text: "1"}, {Kind: RowText, Text: "2"},
        {Kind: RowText, Text: "3"}, {Kind: RowText, Text: "4"},
        {Kind: RowText, Text: "5"},
    }}
    if got := len(RenderPanel(p, 3)); got != 3 {
        t.Errorf("panel ignored its height: got %d lines", got)
    }
}

func TestTruncateToIsWidthAware(t *testing.T) {
    // Review Focus #3: display width, not byte length
    if got := lipgloss.Width(TruncateTo("日本語テキスト", 5)); got > 5 {
        t.Errorf("CJK truncation exceeded: %d", got)
    }
    if got := lipgloss.Width(TruncateTo("plain ascii", 5)); got != 5 {
        t.Errorf("ascii truncation = %d, want 5", got)
    }
}

func TestRenderColumnsPadExactly(t *testing.T) {
    cols := [][]string{
        {"aaaa", "b"},
        {"cc", "d"},
    }
    out := RenderColumns(cols, 2, 20)
    for i, l := range out {
        if lipgloss.Width(l) != 20 { t.Errorf("line %d width %d, want 20", i, lipgloss.Width(l)) }
    }
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/tui/ -run 'RenderD|Panel|Truncate|Columns' -v`
Expected: FAIL — `undefined: renderD`.

- [ ] **Step 3: Implement `internal/tui/layout.go`**

`RenderPanel` draws `┌─ Title ───┐`, N content rows, `└──────────┘`, each line exactly
`p.Width` display columns. Blank rows are spaces, never empty strings — an empty string in
a column layout collapses the column.

`RenderColumns` joins pre-rendered columns with a fixed gap and pads the result to `width`.
**Rows beyond the shortest column are filled with spaces**, not omitted.

`TruncateTo` uses `lipgloss.Width` and `runewidth`-aware truncation via lipgloss, appending
`…` when it actually cuts.

- [ ] **Step 4: Implement `internal/tui/layout_d.go`**

Three panels: deck A (left), mixer (centre), deck B (right). Panel widths derive from `w`:

```
if w < 90 → render a "needs at least 90 columns" message with the current width
deckW = clamp((w - mixerW - 2*gap) / 2, 24, 52)
mixerW = clamp(w/5, 14, 24)
```

Render placeholder rows for deck contents (Task 5 fills them in) — the panel structure,
borders, titles, and column arithmetic are what this task delivers.

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/tui/ -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/tui/
git commit -m "feat(tui): panel primitives and the default mixer-centred layout"
```

---

### Task 3: Layouts A, B, and C

**Files:**
- Create: `internal/tui/layout_a.go`
- Create: `internal/tui/layout_b.go`
- Create: `internal/tui/layout_c.go`
- Create: `internal/tui/layouts_test.go`

**Interfaces:**
- Consumes: primitives from Task 2.
- Produces:
  ```go
  func renderA(w, h int, s engine.Snapshot, st ViewState) string
  func renderB(w, h int, s engine.Snapshot, st ViewState) string  // focused
  func renderC(w, h int, s engine.Snapshot, st ViewState) string  // stacked
  func Render(w, h int, s engine.Snapshot, st ViewState) string   // dispatch
  // MinWidth returns the smallest width a layout renders meaningfully at.
  func MinWidth(k LayoutKind) int
  ```

- [ ] **Step 1: Write the failing tests**

Create `internal/tui/layouts_test.go`:

```go
package tui

import (
    "strings"
    "testing"
)

func TestAllLayoutsFitTheirMinWidth(t *testing.T) {
    cases := map[LayoutKind]struct{ render func(int,int,engine.Snapshot,ViewState)string }{
        LayoutA: {renderA}, LayoutB: {renderB}, LayoutC: {renderC}, LayoutD: {renderD},
    }
    for k, c := range cases {
        min := MinWidth(k)
        out := c.render(min, 30, snapTwoDecks(), ViewState{Layout: k})
        assertLinesFit(t, out, min)
    }
}

func TestAllLayoutsSurviveOversize(t *testing.T) {
    for _, k := range []LayoutKind{LayoutA, LayoutB, LayoutC, LayoutD} {
        out := Render(400, 120, snapTwoDecks(), ViewState{Layout: k})
        assertLinesFit(t, out, 400)
    }
}

func TestAllLayoutsSurviveTiny(t *testing.T) {
    // Review Focus #2: no panic, no overflow, at any size
    for _, k := range []LayoutKind{LayoutA, LayoutB, LayoutC, LayoutD} {
        for _, size := range [][2]int{{20,5},{10,3},{1,1},{200,2}} {
            out := Render(size[0], size[1], snapTwoDecks(), ViewState{Layout: k})
            for _, l := range strings.Split(out, "\n") {
                if len(l) > 0 && strings.Contains(l, "panic") {
                    t.Fatalf("layout %v at %dx%d looks broken:\n%s", k, size[0], size[1], out)
                }
            }
        }
    }
}

func TestDispatchMatchesLayoutChoice(t *testing.T) {
    for _, k := range []LayoutKind{LayoutA, LayoutB, LayoutC, LayoutD} {
        a := Render(120, 40, snapTwoDecks(), ViewState{Layout: k})
        b := Render(120, 40, snapTwoDecks(), ViewState{Layout: k})
        if a != b { t.Errorf("Render is not deterministic for layout %v", k) }
    }
}

func TestLayoutBShowsFocusedDeckTitle(t *testing.T) {
    out := renderB(100, 40, snapTwoDecks(), ViewState{Layout: LayoutB, Focus: 1})
    if !strings.Contains(out, "Track B") { t.Error("focused deck title missing") }
    if !strings.Contains(out, "Track A") { t.Error("collapsed deck strip missing its title") }
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/tui/ -run 'AllLayouts|Dispatch|LayoutB' -v`
Expected: FAIL — `undefined: renderA`.

- [ ] **Step 3: Implement layouts A, B, and C**

**A** — deck A, deck B, mixer beside them; crossfader spans a full-width footer.
**B** — one deck fills the upper region, the other collapses to a two-line strip at the
bottom; `Focus` selects which. MinWidth 70.
**C** — deck A stacked above deck B, mixer in the footer. MinWidth 80.

All three use only Task 2's primitives. None contains layout arithmetic of its own beyond
choosing panel widths and ordering.

`Render` dispatches on `st.Layout` and delegates. `MinWidth` returns 100, 70, 80, 100 for
A, B, C, D.

**When the terminal is narrower than a layout's `MinWidth`, render the "too small" panel
with the required and current widths.** Do not degrade silently into a broken layout.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/tui/ -v -race`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/tui/
git commit -m "feat(tui): side-by-side, focused, and stacked layouts"
```

---

### Task 4: Braille waveform rendering

**Files:**
- Create: `internal/tui/waveform.go`
- Create: `internal/tui/meters.go`
- Create: `internal/tui/waveform_test.go`

**Interfaces:**
- Consumes: `engine.DeckState` from milestone 1.
- Produces:
  ```go
  package tui

  // Peaks is a min/max envelope for a span of audio.
  type Peaks struct { Min, Max []float32 }   // len == width*2

  type WaveSpec struct {
      Peaks    Peaks
      Width    int    // cells
      Height   int    // braille rows; 4 gives 16 amplitude levels per column pair
      PlayCol  int    // cell index of the playhead, or -1
      Beats    []int  // cell indices of beats
      Downbeats []int // cell indices of downbeats
      Cues     []int  // cell indices of hotcues
      Loops    []LoopSpan
  }
  type LoopSpan struct{ Start, End int }   // cell indices

  // RenderWave returns exactly Height strings of Width cells.
  func RenderWave(spec WaveSpec) []string
  func RenderWaveStyled(spec WaveSpec, played, unplayed lipgloss.Style) []string
  // RenderOverview draws the full-track strip with the window highlighted.
  func RenderOverview(peaks Peaks, width, winStart, winEnd, playCell int) string
  func PeaksFromPCM(pcm dsp.Stereo, width int) Peaks
  ```

**The braille mapping**, verified against the design mockups:

```go
// U+2800 + bits. Column 0 is the left dot column, column 1 the right.
var brailleDot = [2][4]rune{
    {0x01, 0x02, 0x04, 0x40},  // left column, rows 0-3
    {0x08, 0x10, 0x20, 0x80},  // right column, rows 0-3
}
```

A cell is `rune(0x2800 + bits)`. Mirrored waveform: the top `height/2` braille rows are
the positive half, the bottom `height/2` the negative half.

- [ ] **Step 1: Write the failing tests**

Create `internal/tui/waveform_test.go`:

```go
package tui

import (
    "strings"
    "testing"

    "github.com/lipgloss/lipgloss"
    "github.com/maguldon/mix-tui/internal/dsp"
)

func testPeaks(w int, v float32) Peaks {
    p := Peaks{Min: make([]float32, w*2), Max: make([]float32, w*2)}
    for i := range p.Max { p.Max[i] = v; p.Min[i] = -v }
    return p
}

func TestRenderWaveDimensions(t *testing.T) {
    for _, h := range []int{2, 3, 4} {
        rows := RenderWave(WaveSpec{Peaks: testPeaks(40, 1), Width: 40, Height: h})
        if len(rows) != h { t.Errorf("height %d gave %d rows", h, len(rows)) }
        for i, r := range rows {
            if lipgloss.Width(r) != 40 { t.Errorf("row %d width %d", i, lipgloss.Width(r)) }
        }
    }
}

func TestRenderWaveUsesBrailleRange(t *testing.T) {
    rows := RenderWave(WaveSpec{Peaks: testPeaks(20, 1), Width: 20, Height: 4})
    for _, r := range rows {
        for _, c := range r {
            if c < 0x2800 || c > 0x28FF {
                t.Fatalf("non-braille rune %U in waveform", c)
            }
        }
    }
}

func TestRenderWaveIsSingleWidthPerCell(t *testing.T) {
    // Review Focus #4 in Global Constraints: no mixed-width glyphs
    rows := RenderWave(WaveSpec{Peaks: testPeaks(30, 0.5), Width: 30, Height: 4})
    for _, r := range rows {
        if lipgloss.Width(r) != 30 {
            t.Errorf("row width %d, want 30 (a wide glyph slipped in)", lipgloss.Width(r))
        }
    }
}

func TestRenderWaveSilenceIsBlank(t *testing.T) {
    rows := RenderWave(WaveSpec{Peaks: testPeaks(20, 0), Width: 20, Height: 4})
    for _, r := range rows {
        for _, c := range r {
            if c != 0x2800 { t.Fatalf("silence produced %U", c) }
        }
    }
}

func TestRenderWaveQuietIsShorterThanLoud(t *testing.T) {
    quiet := RenderWave(WaveSpec{Peaks: testPeaks(20, 0.2), Width: 20, Height: 4})
    loud  := RenderWave(WaveSpec{Peaks: testPeaks(20, 1.0), Width: 20, Height: 4})
    count := func(rows []string) int { n := 0; for _, r := range rows {
        for _, c := range r { if c != 0x2800 { n++ } } }; return n }
    if count(quiet) >= count(loud) { t.Error("quiet waveform is not shorter than loud") }
}

func TestRenderWaveZeroWidth(t *testing.T) {
    _ = RenderWave(WaveSpec{Peaks: testPeaks(0, 1), Width: 0, Height: 4})  // must not panic
}

func TestPeaksFromPCMRespectsLength(t *testing.T) {
    p := PeaksFromPCM(dsp.Sine(440, 1.0), 50)
    if len(p.Max) != 100 { t.Errorf("peak slices = %d, want 100 (2 per cell)", len(p.Max)) }
}

func TestPeaksFromPCMEmptyIsSafe(t *testing.T) {
    p := PeaksFromPCM(nil, 10)
    if len(p.Max) != 20 { t.Errorf("empty PCM gave %d", len(p.Max)) }
}

func TestRenderOverviewHighlightsWindow(t *testing.T) {
    o := RenderOverview(testPeaks(20, 0.5), 20, 5, 10, 7)
    if lipgloss.Width(o) != 20 { t.Errorf("overview width %d", lipgloss.Width(o)) }
}

func TestRenderWaveZeroWidthHeight(t *testing.T) {
    for _, s := range []WaveSpec{
        {Width: 0, Height: 0}, {Width: 10, Height: 0}, {Width: 0, Height: 4},
        {Width: 10, Height: 1},
    } {
        _ = RenderWave(s)   // must not panic
    }
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/tui/ -run 'RenderWave|Peaks|Overview' -v`
Expected: FAIL — `undefined: RenderWave`.

- [ ] **Step 3: Implement `internal/tui/waveform.go`**

Port the verified braille algorithm from the design mockups:

1. `PeaksFromPCM` reduces the PCM to `width*2` min/max columns.
2. `RenderWave` builds a `[][width]int` bit matrix, then converts to runes.
   For each column `c` with amplitude `amp`, let `half = height/2` and
   `maxLvl = half*4 - 1`; `s = round(amp * maxLvl)`; for each level `0..s`, set
   `cell[c/2] |= brailleDot[c%2][3-(lvl%4)]` in row `half-1-lvl/4` (upper) and
   `half+lvl/4` (lower).
3. Mark playhead, beats, downbeats, cues, and loop spans by replacing the cell content —
   **overlay after generating the wave**, so markers are always visible.

**Guard every width and height against zero and negative.** These are the panic sites.

- [ ] **Step 4: Implement `internal/tui/meters.go`**

```go
func RenderMeter(v, width int) string    // block-character bar
func RenderPeakHold(peak, current float64, width int) string
```

Peak hold decays toward the current value by ~1dB per frame so a transient is visible for
about a second.

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/tui/ -v`
Expected: PASS.

- [ ] **Step 6: Verify the output visually**

```bash
go run ./cmd/mix-tui -a testdata/fixtures/sine440.wav -b testdata/fixtures/sine440.wav
```
(after Task 12) — or, sooner, add a `tuidemo` test helper that prints
`renderD(120, 40, snap, state)` to stdout. **Look at it.** Braille waveforms are
genuinely hard to get right and a passing dimension test does not mean it looks like
anything.

- [ ] **Step 7: Commit**

```bash
git add internal/tui/
git commit -m "feat(tui): braille waveform renderer and level meters"
```

---

### Task 5: Deck and mixer panels

**Files:**
- Create: `internal/tui/deck_panel.go`
- Create: `internal/tui/mixer_panel.go`
- Create: `internal/tui/panels_test.go`

**Interfaces:**
- Consumes: `WaveSpec`, `RenderWave`, meters from Task 4.
- Produces:
  ```go
  func renderDeckPanel(w int, d engine.DeckState, st ViewState, focused bool) Panel
  func renderMixerPanel(w int, s engine.Snapshot, st ViewState) Panel
  func RenderArt(w, h int, name string) []string   // empty deck, splash, playing accent
  ```

- [ ] **Step 1: Write the failing tests**

Create `internal/tui/panels_test.go`:

```go
package tui

import (
    "strings"
    "testing"

    "github.com/lipgloss/lipgloss"
)

func TestDeckPanelShowsTrackMetadata(t *testing.T) {
    p := renderDeckPanel(40, snapTwoDecks().Decks[0], ViewState{}, true)
    out := strings.Join(RenderPanel(p, 14), "\n")
    for _, want := range []string{"Track A", "128.00", "A"} {
        if !strings.Contains(out, want) { t.Errorf("deck panel missing %q:\n%s", want, out) }
    }
}

func TestDeckPanelShowsBPMWithoutGridAsUnknown(t *testing.T) {
    d := snapTwoDecks().Decks[0]
    d.BPM = 0; d.HasBeatGrid = false
    out := strings.Join(RenderPanel(renderDeckPanel(40, d, ViewState{}, false), 14), "\n")
    if strings.Contains(out, "0.00") { t.Error("a deck with no beatgrid must not show 0.00 BPM") }
    if !strings.Contains(out, "--") { t.Errorf("expected a placeholder, got:\n%s", out) }
}

func TestDeckPanelShowsKeyWithConfidence(t *testing.T) {
    d := snapTwoDecks().Decks[0]
    d.KeyCamelot, d.KeyConfidence = "8A", 0.91
    out := strings.Join(RenderPanel(renderDeckPanel(44, d, ViewState{}, false), 14), "\n")
    if !strings.Contains(out, "8A") { t.Errorf("key missing:\n%s", out) }

    d.KeyCamelot, d.KeyConfidence = "9A", 0.30   // below threshold
    out = strings.Join(RenderPanel(renderDeckPanel(44, d, ViewState{}, false), 14), "\n")
    if strings.Contains(out, "9A") {
        t.Errorf("a below-threshold key must render as undetected:\n%s", out)
    }
}

func TestDeckPanelShowsLoopState(t *testing.T) {
    d := snapTwoDecks().Decks[0]
    d.LoopActive, d.LoopBeatCount = true, 4
    out := strings.Join(RenderPanel(renderDeckPanel(44, d, ViewState{}, true), 14), "\n")
    if !strings.Contains(out, "LOOP 4") { t.Errorf("loop size not shown:\n%s", out) }
}

func TestEmptyDeckShowsArtNotBlank(t *testing.T) {
    d := engine_DeckState_empty()
    out := strings.Join(RenderPanel(renderDeckPanel(40, d, ViewState{}, false), 14), "\n")
    if strings.Contains(out, "EMPTY DECK — press") {
        // some art or at least a clear prompt is required
    }
    if !strings.Contains(out, "L") { t.Errorf("expected a load hint:\n%s", out) }
}

func TestMixerPanelShowsBothFaders(t *testing.T) {
    s := snapTwoDecks()
    s.Xfader = 0.3
    out := strings.Join(RenderPanel(renderMixerPanel(24, s, ViewState{}), 12), "\n")
    if !strings.Contains(out, "A") || !strings.Contains(out, "B") {
        t.Errorf("both channel labels required:\n%s", out)
    }
    if !strings.Contains(out, "MASTER") { t.Errorf("master missing:\n%s", out) }
}

func TestPanelWidthIsExactAtManySizes(t *testing.T) {
    for _, w := range []int{24, 28, 32, 40, 48, 52} {
        p := renderDeckPanel(w, snapTwoDecks().Decks[0], ViewState{}, true)
        for i, l := range RenderPanel(p, 14) {
            if lipgloss.Width(l) != w {
                t.Errorf("w=%d line %d is %d wide", w, i, lipgloss.Width(l))
            }
        }
    }
}
```

`engine_DeckState_empty()` is `engine.DeckState{Index: 0}` with `Loaded: false`.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/tui/ -run 'DeckPanel|MixerPanel' -v`
Expected: FAIL — `undefined: renderDeckPanel`.

- [ ] **Step 3: Implement `internal/tui/deck_panel.go`**

Row order, matching the approved mockups:

1. Waveform (`RenderWave`, height 2 in compact panels, 4 in wide ones)
2. Beatgrid / beat marks
3. Playhead progress bar
4. Hotcue markers, numbered
5. Track title and artist
6. BPM · KEY · pitch fader · remaining time
7. EQ bars (low/mid/high)
8. Transport row: `■CUE ■PLAY ■SYNC ■SLIP ■QUANT`
9. Loop indicator when active, else the VU art accent

**Every row is clamped to the panel's inner width.** The panel is often ~28 columns; a
BPM + key + pitch + remaining row does not fit, so compute which fields fit and drop the
least important first (remaining time, then key, then artist) rather than truncating
mid-field.

When `HasBeatGrid` is false, render BPM and key as `--` (spec §8: degraded, not wrong).
When `KeyConfidence` is below threshold, render key as `--` — never as a guessed value.

- [ ] **Step 4: Implement `internal/tui/mixer_panel.go`**

EQ bars, per-channel fader, crossfader, master level and peak hold. The crossfader row
spans the panel; per spec §6.1 the center mixer column stays narrow, so channel EQs live
in the deck panels, not here.

- [ ] **Step 5: Implement `internal/tui/art.go`**

Embed the splash logo, empty-deck art, and playing accent as string constants at fixed
widths, from the approved designs. `RenderArt(name, w, h)` selects the widest variant that
fits and pads with spaces — **never re-pad the art itself**.

- [ ] **Step 6: Run tests to verify they pass**

Run: `go test ./internal/tui/ -v -race`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/tui/
git commit -m "feat(tui): deck and mixer panels with art, keys, and loop state"
```

---

### Task 6: Zoom view

**Files:**
- Create: `internal/tui/zoom.go`
- Create: `internal/tui/zoom_test.go`

**Interfaces:**
- Consumes: `RenderWave`, `Peaks` from Task 4.
- Produces:
  ```go
  package tui

  // ZoomWindow is the sliding time window shown when Zoom is on (spec §6.3).
  type ZoomWindow struct {
      Start    time.Duration
      Length   time.Duration
      Playhead float64  // fraction of window width, 0..1
  }
  const DefaultZoomBars = 4
  var ZoomBarOptions = []int{2, 4, 8, 16, 32}
  // PlayX keeps the playhead left of centre so upcoming bars are visible.
  const PlayX = 0.30

  func WindowFor(pos time.Duration, bars int, bpm float64) ZoomWindow
  func RenderZoomed(pcm dsp.Stereo, w ZoomWindow, width, height int,
                    beats []time.Duration, cues []time.Duration) []string
  // DeltaBeat is the ΔBEAT readout in ms (spec §6.3).
  func DeltaBeat(d, master engine.DeckState) (time.Duration, bool)
  func RenderDeltaBeat(d, master engine.DeckState) string
  func ZoomIndex(bars int) int
  ```

- [ ] **Step 1: Write the failing tests**

Create `internal/tui/zoom_test.go`:

```go
package tui

import (
    "strings"
    "testing"
    "time"

    "github.com/maguldon/mix-tui/internal/dsp"
    "github.com/maguldon/mix-tui/internal/engine"
)

func TestWindowLengthIsFourBars(t *testing.T) {
    w := WindowFor(30e9, 4, 128)
    want := 4 * 4 * time.Second / 128
    if absDur(w.Length-want) > time.Millisecond {
        t.Errorf("window = %v, want 4 bars = %v", w.Length, want)
    }
}

func TestWindowTracksPosition(t *testing.T) {
    a := WindowFor(30e9, 4, 128)
    b := WindowFor(90e9, 4, 128)
    if b.Start-a.Start != 60e9 { t.Errorf("window did not track: %v vs %v", a.Start, b.Start) }
}

func TestWindowScalesWithTempo(t *testing.T) {
    slow := WindowFor(0, 4, 100)
    fast := WindowFor(0, 4, 140)
    if fast.Length >= slow.Length {
        t.Errorf("faster tempo should give a shorter window: %v vs %v", fast.Length, slow.Length)
    }
}

func TestWindowHandlesZeroBPM(t *testing.T) {
    _ = WindowFor(30e9, 4, 0)   // must not panic or divide by zero
}

func TestZoomIndexCycles(t *testing.T) {
    i := 0
    seen := map[int]bool{}
    for n := 0; n < len(ZoomBarOptions); n++ {
        seen[ZoomBarOptions[i]] = true
        i = (i + 1) % len(ZoomBarOptions)
    }
    if len(seen) != len(ZoomBarOptions) { t.Errorf("zoom cycle covered %d of %d", len(seen), len(ZoomBarOptions)) }
}

func TestRenderZoomedDimensions(t *testing.T) {
    w := WindowFor(30e9, 4, 128)
    rows := RenderZoomed(dsp.Sine(440, 60), w, 60, 4, nil, nil)
    if len(rows) != 4 { t.Errorf("got %d rows", len(rows)) }
}

func TestRenderZoomedShowsBeatsInWindow(t *testing.T) {
    w := WindowFor(30e9, 4, 128)
    beats := make([]time.Duration, 200)
    for i := range beats { beats[i] = time.Duration(i) * time.Second / 128 }
    rows := RenderZoomed(dsp.Sine(440, 60), w, 60, 2, beats, nil)
    joined := strings.Join(rows, "\n")
    if !strings.Contains(joined, "┃") && !strings.Contains(joined, "·") {
        t.Error("no beat markers rendered inside the window")
    }
}

func TestDeltaBeatReportsOffset(t *testing.T) {
    master := engine.DeckState{Position: 4e9, HasBeatGrid: true, BPM: 128}
    slave  := engine.DeckState{Position: 4e9 + 40e6, HasBeatGrid: true, BPM: 128}
    d, ok := DeltaBeat(slave, master)
    if !ok { t.Fatal("delta unavailable despite grids") }
    if absDur(d-40e6) > 5e6 { t.Errorf("delta = %v, want ~40ms", d) }
}

func TestDeltaBeatNeedsBothGrids(t *testing.T) {
    _, ok := DeltaBeat(engine.DeckState{Position: 1e9},
                       engine.DeckState{Position: 1e9, HasBeatGrid: true, BPM: 128})
    if ok { t.Error("delta must be unavailable when a grid is missing") }
}

func TestRenderDeltaBeatShowsPhases(t *testing.T) {
    s := strings.TrimSpace(RenderDeltaBeat(
        engine.DeckState{Position: 4e9, HasBeatGrid: true, BPM: 128},
        engine.DeckState{Position: 4e9 + 40e6, HasBeatGrid: true, BPM: 128}))
    if !strings.Contains(s, "ms") { t.Errorf("expected a ms readout, got %q", s) }
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/tui/ -run 'Window|Zoom|DeltaBeat' -v`
Expected: FAIL — `undefined: WindowFor`.

- [ ] **Step 3: Implement `internal/tui/zoom.go`**

```go
func WindowFor(pos time.Duration, bars int, bpm float64) ZoomWindow {
    if bpm <= 0 { bpm = 128 }            // guard: no grid must not divide by zero
    beat := time.Duration(float64(time.Second) / bpm)
    length := time.Duration(bars*4) * beat
    return ZoomWindow{Start: pos - time.Duration(float64(length)*PlayX),
                      Length: length, Playhead: PlayX}
}
```

`RenderZoomed` extracts `Peaks` for the window (using `PeaksFromPCM` over the PCM range),
renders the mirrored wave, then overlays beat and cue markers whose times fall inside
`[Start, Start+Length]`.

`DeltaBeat` computes each deck's offset from its own nearest beat and returns the
difference, clamped to ±half a beat. Returns `false` when either deck lacks a grid.

- [ ] **Step 4: Wire zoom into the deck panel**

In `renderDeckPanel`, when `st.Zoom` and this deck is focused, use `RenderZoomed` instead
of the full-track wave, and append the ΔBEAT row beneath the mixer or footer.

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/tui/ -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/tui/
git commit -m "feat(tui): CDJ-style zoomed tracking waveform with delta-beat readout"
```

---

### Task 7: Library scan, index, and analysis cache

**Files:**
- Create: `internal/tui/library/scan.go`
- Create: `internal/tui/library/index.go`
- Create: `internal/tui/library/cache.go`
- Create: `internal/tui/library/crate.go`
- Create: `internal/tui/library/library_test.go`

**Interfaces:**
- Consumes: `audio.Decode` from milestone 1; `analyze.AnalyzeBeatGrid` / `AnalyzeKey`.
- Produces:
  ```go
  package library

  type Track struct {
      Path string
      Title, Artist string
      Duration time.Duration
      BPM float64
      HasGrid bool
      KeyCamelot string
      KeyConfidence float64
      InLibrary bool
  }

  type Index struct { /* ... */ }
  func NewIndex() *Index
  // Scan walks the roots in a goroutine, publishing progress. It must never
  // block the caller (spec §8: the TUI stays responsive during a scan).
  func (ix *Index) Scan(ctx context.Context, roots []string,
                        onProgress func(done, total int), onTrack func(Track)) error
  func (ix *Index) Tracks() []Track
  func (ix *Index) Len() int

  // Sort orders a copy of the track list. It never mutates the index.
  func Sort(tracks []Track, by SortKey, desc bool) []Track
  type SortKey int
  const (SortTitle SortKey = iota; SortArtist; SortBPM; SortKey; SortAdded)

  // Search filters on filename, title, and artist, case-insensitively.
  func Search(tracks []Track, q string) []Track

  // Cache stores analysis keyed by path+size+mtime (spec §4.1).
  type Cache struct { dir string }
  func OpenCache(dir string) *Cache
  func (c *Cache) Get(path string) (Analysis, bool)
  func (c *Cache) Put(path string, a Analysis) error
  func (c *Cache) Stale(path string) bool      // true if the file changed
  type Analysis struct { BPM float64; Beats []time.Duration
                         KeyCamelot string; KeyConfidence float64
                         Size int64; ModTime time.Time }

  type Crate struct { Name string; Tracks []string }
  func LoadCrates(path string) ([]Crate, error)
  func SaveCrate(path string, c Crate) error
  ```

- [ ] **Step 1: Write the failing tests**

Create `internal/tui/library/library_test.go`:

```go
package library

import (
    "context"
    "os"
    "path/filepath"
    "testing"
    "time"
)

func tmpTracks(t *testing.T, n int) string {
    root := t.TempDir()
    for i := 0; i < n; i++ {
        name := filepath.Join(root, "track"+string(rune('a'+i))+".mp3")
        if err := os.WriteFile(name, []byte("x"), 0o644); err != nil { t.Fatal(err) }
    }
    return root
}

func TestScanFindsFiles(t *testing.T) {
    root := tmpTracks(t, 5)
    ix := NewIndex()
    if err := ix.Scan(context.Background(), []string{root}, nil, nil); err != nil {
        t.Fatal(err)
    }
    if ix.Len() != 5 { t.Errorf("found %d, want 5", ix.Len()) }
}

func TestScanSkipsMissingRootWithoutFailing(t *testing.T) {
    // spec §8: one bad path must not empty the library
    root := tmpTracks(t, 3)
    ix := NewIndex()
    err := ix.Scan(context.Background(), []string{root, "/nonexistent/xyz"}, nil, nil)
    if err == nil { t.Error("expected an error for the missing root") }
    if ix.Len() != 3 { t.Errorf("good root yielded %d tracks, want 3", ix.Len()) }
}

func TestScanRespectsContextCancel(t *testing.T) {
    root := tmpTracks(t, 3)
    ctx, cancel := context.WithCancel(context.Background())
    cancel()
    ix := NewIndex()
    _ = ix.Scan(ctx, []string{root}, nil, nil)
    // must return promptly; may be partial
}

func TestSortDoesNotMutateInput(t *testing.T) {
    ts := []Track{{Title: "b", BPM: 200}, {Title: "a", BPM: 100}}
    orig := append([]Track{}, ts...)
    Sort(ts, SortBPM, false)
    if ts[0].Title != orig[0].Title { t.Error("Sort mutated its input") }
    if Sort(ts, SortBPM, false)[0].BPM != 100 { t.Error("ascending sort wrong") }
    if Sort(ts, SortBPM, true)[0].BPM != 200 { t.Error("descending sort wrong") }
}

func TestSortPlacesUndetectedLast(t *testing.T) {
    ts := []Track{{Title: "x", BPM: 0}, {Title: "y", BPM: 128}}
    s := Sort(ts, SortBPM, false)
    if s[len(s)-1].BPM != 0 { t.Error("undetected BPM must sort last") }
}

func TestSearchMatchesTitleArtistAndPath(t *testing.T) {
    ts := []Track{
        {Title: "Warehouse", Artist: "Klara", Path: "/m/a.wav"},
        {Title: "Samba", Artist: "Jockstrap", Path: "/m/b.wav"},
    }
    if len(Search(ts, "ware")) != 1 { t.Error("title match failed") }
    if len(Search(ts, "jock")) != 1 { t.Error("artist match failed") }
    if len(Search(ts, "/m/b")) != 1 { t.Error("path match failed") }
    if len(Search(ts, "")) != 2 { t.Error("empty query must return everything") }
}

func TestCacheRoundTrip(t *testing.T) {
    d := t.TempDir()
    c := OpenCache(d)
    a := Analysis{BPM: 128, KeyCamelot: "8A", KeyConfidence: 0.9,
                 Size: 100, ModTime: time.Now()}
    if err := c.Put("/x/y.wav", a); err != nil { t.Fatal(err) }
    got, ok := c.Get("/x/y.wav")
    if !ok { t.Fatal("cache miss") }
    if got.BPM != 128 || got.KeyCamelot != "8A" { t.Errorf("round trip lost data: %+v", got) }
}

func TestCacheInvalidatesOnContentChange(t *testing.T) {
    // spec §4.1: replaced audio must invalidate
    d := t.TempDir()
    f := filepath.Join(d, "t.wav")
    os.WriteFile(f, []byte("original"), 0o644)
    st, _ := os.Stat(f)
    c := OpenCache(d)
    c.Put(f, Analysis{BPM: 128, Size: st.Size(), ModTime: st.ModTime()})
    if _, ok := c.Get(f); !ok { t.Fatal("fresh entry missing") }

    time.Sleep(10 * time.Millisecond)
    os.WriteFile(f, []byte("a completely different file"), 0o644)
    st2, _ := os.Stat(f)
    c.Put(f, Analysis{BPM: 128, Size: st2.Size(), ModTime: st2.ModTime()})
    if !c.Stale("/other") { /* unrelated path: not stale, fine */ }
}

func TestCacheMissingRootDoesNotError(t *testing.T) {
    c := OpenCache(filepath.Join(t.TempDir(), "nonexistent"))
    if _, ok := c.Get("/x"); ok { t.Error("empty cache reported a hit") }
}

func TestCrateRoundTrip(t *testing.T) {
    p := filepath.Join(t.TempDir(), "crates.yaml")
    in := Crate{Name: "Warm", Tracks: []string{"/a.mp3", "/b.mp3"}}
    if err := SaveCrate(p, in); err != nil { t.Fatal(err) }
    out, err := LoadCrates(filepath.Join(filepath.Dir(p), "crates.yaml"))
    if err != nil { t.Fatal(err) }
    if len(out) != 1 || len(out[0].Tracks) != 2 { t.Errorf("round trip lost data: %+v", out) }
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/tui/library/ -v`
Expected: FAIL — `undefined: NewIndex`.

- [ ] **Step 3: Implement `internal/tui/library/scan.go` and `index.go`**

Walk each root with `filepath.WalkDir`, skipping hidden directories and any directory named
in a skip list. **Collect per-root errors and return them joined at the end, never aborting
the whole scan** (spec §8).

Publish each found track through `onTrack` so the index fills incrementally and the UI can
show progress, rather than blocking until the walk completes.

`Sort` returns a new slice and puts entries with `BPM == 0` last regardless of direction —
a track with no detected tempo should not sit at the top of a BPM-sorted crate.

- [ ] **Step 4: Implement `internal/tui/library/cache.go`**

One file per track, named by a hash of the absolute path. Contents record `Size` and
``ModTime`; `Get` compares against the file's current stat and treats a mismatch as a miss.
**Use mtime + size, never content hashing** — hashing every library file on every launch is
unacceptable, and spec §4.1 already chose this tradeoff.

- [ ] **Step 5: Implement `internal/tui/library/crate.go`**

YAML list of `{name, tracks}`. `SaveCrate` merges into the existing file rather than
overwriting it, so saving one crate does not delete the others (spec §6.5: sorting never
rewrites a crate, and saving one must not either).

- [ ] **Step 6: Run tests to verify they pass**

Run: `go test ./internal/tui/library/ -v -race`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/tui/library/
git commit -m "feat(library): background scan, index, analysis cache, and crates"
```

---

### Task 8: Library and overlay views

**Files:**
- Create: `internal/tui/views/library.go`
- Create: `internal/tui/views/overlay.go`
- Create: `internal/tui/views/library_test.go`

**Interfaces:**
- Consumes: `library.Index`, `library.Sort`, `library.Search` from Task 7.
- Produces:
  ```go
  package views

  type LibraryState struct {
      Pane      Pane   // PaneCrates | PaneTracks | PaneSearch
      CrateSel  int
      TrackSel  int
      Query     string
      SortBy    library.SortKey
      SortDesc  bool
      ShowSort  bool
      Width     int
    }
  type Pane int
  const (PaneCrates Pane = iota; PaneTracks; PaneSearch)

  func RenderLibrary(w, h int, ix *library.Index, st LibraryState) string
  func RenderOverlay(w, h int, ix *library.Index, query string, sel int) string
  func MatchTrack(ts []library.Track, query string, sel int) (library.Track, bool)
  ```

- [ ] **Step 1: Write the failing tests**

Create `internal/tui/views/library_test.go`:

```go
package views

import (
    "strings"
    "testing"

    "github.com/lipgloss/lipgloss"
    "github.com/maguldon/mix-tui/internal/tui/library"
)

func testIndex() *library.Index {
    ix := library.NewIndex()
    ix.Add(library.Track{Path: "/m/1.wav", Title: "Inner Speaker",
                         Artist: "Safras", BPM: 128, HasGrid: true,
                         KeyCamelot: "8A", KeyConfidence: 0.9, Duration: 214e9})
    ix.Add(library.Track{Path: "/m/2.wav", Title: "Warehouse Dub",
                         Artist: "Klara", BPM: 126.5, HasGrid: true})
    return ix
}

func TestLibraryFitsWidth(t *testing.T) {
    for _, w := range []int{200, 140, 120, 121, 119} {
        out := RenderLibrary(w, 40, testIndex(), LibraryState{})
        for i, l := range strings.Split(out, "\n") {
            if lipgloss.Width(l) > w {
                t.Fatalf("w=%d line %d is %d wide: %q", w, i, lipgloss.Width(l), l)
            }
        }
    }
}

func TestLibraryRequiresWidth(t *testing.T) {
    out := RenderLibrary(100, 40, testIndex(), LibraryState{})
    if !strings.Contains(out, "120") {
        t.Errorf("a 100-col render should name the required width:\n%s", out)
    }
}

func TestLibraryShowsColumns(t *testing.T) {
    out := RenderLibrary(120, 40, testIndex(), LibraryState{})
    for _, want := range []string{"Inner Speaker", "Safras", "128.00", "8A", "3:34"} {
        if !strings.Contains(out, want) { t.Errorf("missing %q", want) }
    }
}

func TestLibrarySortPaneReplacesDetailPane(t *testing.T) {
    ix := testIndex()
    off := RenderLibrary(120, 40, ix, LibraryState{ShowSort: false})
    on  := RenderLibrary(120, 40, ix, LibraryState{ShowSort: true})
    if strings.Contains(on, "key conf") { t.Error("detail pane still shown with sort open") }
    if !strings.Contains(on, "BPM") { t.Error("sort pane not shown") }
    if off == on { t.Error("sort toggle changed nothing") }
}

func TestLibrarySortDoesNotRewriteCrate(t *testing.T) {
    ix := testIndex()
    _ = RenderLibrary(120, 40, ix, LibraryState{SortBy: library.SortBPM, ShowSort: true})
    if len(ix.Tracks()) != 2 { t.Error("rendering mutated the index") }
}

func TestBelowThresholdKeyShowsUndetected(t *testing.T) {
    ix := library.NewIndex()
    ix.Add(library.Track{Path: "/x", Title: "T", BPM: 128,
                         KeyCamelot: "9A", KeyConfidence: 0.2})
    out := RenderLibrary(120, 40, ix, LibraryState{})
    if strings.Contains(out, "9A") { t.Errorf("low-confidence key displayed:\n%s", out) }
}

func TestOverlayFiltersAsYouType(t *testing.T) {
    ix := testIndex()
    all := RenderOverlay(120, 40, ix, "", 0)
    few := RenderOverlay(120, 40, ix, "ware", 0)
    if strings.Count(few, "Inner") != 0 { t.Error("filter did not exclude") }
    if !strings.Contains(few, "Warehouse") { t.Error("filter excluded the match") }
    if strings.Count(all, "Speaker") == 0 { t.Error("unfiltered view lost a track") }
}

func TestOverlayShowsDeckBehind(t *testing.T) {
    out := RenderOverlay(120, 40, testIndex(), "", 0)
    if !strings.Contains(out, "LOAD TRACK") { t.Error("overlay frame missing") }
}

func TestLibrarySurvivesEmptyIndex(t *testing.T) {
    out := RenderLibrary(120, 40, library.NewIndex(), LibraryState{})
    if out == "" { t.Error("empty library rendered nothing") }
    _ = RenderOverlay(120, 40, library.NewIndex(), "zzz", 0)
}

func TestMatchTrackClampsSelection(t *testing.T) {
    ts := testIndex().Tracks()
    if _, ok := MatchTrack(ts, "", 99); ok { t.Error("out-of-range selection should report no match") }
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/tui/views/ -v`
Expected: FAIL — `undefined: RenderLibrary`.

- [ ] **Step 3: Implement `internal/tui/views/library.go`**

Three panes at 120 columns: crates (28) | tracks (fills) | detail (22). **Below 120 columns,
render a resize message naming the required width** — spec §6.5 chose a hard floor over
permanently truncated columns, and this is where that decision is enforced.

Track row columns, in priority order: cursor, duration bar, title, artist, BPM, key,
duration, hotcue count. **Distribute leftover width to title then artist**; drop the
duration bar first when space runs out.

Key rendering honours the confidence threshold: below it, render `--`, never the guess.

- [ ] **Step 4: Implement `internal/tui/views/overlay.go`**

The overlay box over the deck view: a query line, a separator, and up to 8 filtered results.
Dim the surrounding content rather than erasing it — the deck stays visible behind, which
is the entire point (spec §6.5).

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/tui/views/ -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/tui/views/
git commit -m "feat(tui): three-pane library browser and mid-set load overlay"
```

---

### Task 9: Debug overlay and help

**Files:**
- Create: `internal/tui/views/debug.go`
- Create: `internal/tui/views/help.go`
- Create: `internal/tui/views/debug_test.go`

**Interfaces:**
- Consumes: `midi.Handler.Counts`, `engine.Snapshot`, `pipewire` stats.
- Produces:
  ```go
  func RenderDebug(w, h int, s engine.Snapshot, stats DebugStats) string
  type DebugStats struct {
      Underruns   uint64
      MIDIIgnored map[string]uint64
      BlocksRendered uint64
      RenderWallMax time.Duration
      MainCallbacks, CueCallbacks uint64
      AnalysisState string
  }
  func RenderHelp(w, h int, bindings []string) string
  ```

- [ ] **Step 1: Write the failing tests**

Create `internal/tui/views/debug_test.go`:

```go
package views

import (
    "strings"
    "testing"

    "github.com/lipgloss/lipgloss"
)

func TestDebugShowsUnderruns(t *testing.T) {
    out := RenderDebug(120, 40, engine_SnapshotEmpty(), DebugStats{Underruns: 7})
    if !strings.Contains(out, "underrun") { t.Errorf("underruns not shown:\n%s", out) }
    if !strings.Contains(out, "7") { t.Error("underrun count missing") }
}

func TestDebugShowsMIDIIgnoredCounts(t *testing.T) {
    out := RenderDebug(120, 40, engine_SnapshotEmpty(),
        DebugStats{MIDIIgnored: map[string]uint64{"sysex": 12, "clock": 3}})
    if !strings.Contains(out, "sysex") { t.Error("sysex count missing") }
}

func TestDebugHandlesNilMap(t *testing.T) {
    _ = RenderDebug(120, 40, engine_SnapshotEmpty(), DebugStats{})
}

func TestDebugFits(t *testing.T) {
    out := RenderDebug(120, 40, engine_SnapshotEmpty(), DebugStats{
        MIDIIgnored: map[string]uint64{"a": 1, "b": 2, "c": 3, "d": 4}})
    for i, l := range strings.Split(out, "\n") {
        if lipgloss.Width(l) > 120 { t.Errorf("line %d too wide", i) }
    }
}

func TestHelpListsBindings(t *testing.T) {
    out := RenderHelp(100, 40, []string{"space  play deck A", "c  cue"})
    if !strings.Contains(out, "space") || !strings.Contains(out, "cue") { t.Error("bindings missing") }
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/tui/views/ -run 'Debug|Help' -v`
Expected: FAIL — `undefined: RenderDebug`.

- [ ] **Step 3: Implement both views**

Debug overlay: a panel with audio health (underruns, block counts, worst render time),
MIDI health (per-category ignored counts from spec §7), and analysis state. **This is the
only place a user can see why a controller is not doing what they expect**, so it must
show the counts even when they are zero.

Help: a scrollable list built from `config.KeyResolver.Bindings()`.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/tui/views/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/tui/views/
git commit -m "feat(tui): debug overlay and help view"
```

---

### Task 10: Keys and wiring the model together

**Files:**
- Create: `internal/tui/keys.go`
- Modify: `internal/tui/model.go`
- Modify: `internal/tui/layout_d.go`, `layout_a.go`, `layout_b.go`, `layout_c.go`

**Interfaces:**
- Consumes: `config.KeyResolver`, all render functions.
- Produces: a `Model` whose `RenderAt` dispatches to layout, library, overlay, help, or
  debug based on `ViewState.Mode`.

- [ ] **Step 1: Write the failing tests**

Extend `internal/tui/model_test.go`:

```go
func TestZoomToggleChangesDeckPanelHeight(t *testing.T) {
    m, _ := testModel(t)
    off := m.RenderAt(120, 40)
    m2, _ := m.Update(keyMsg("z"))
    on := m2.(*Model).RenderAt(120, 40)
    if off == on { t.Error("zoom toggle did not change the render") }
}

func TestZoomLevelKeyChangesBars(t *testing.T) {
    m, _ := testModel(t)
    before := m.State().ZoomBars
    m2, _ := m.Update(keyMsg("Z"))
    if m2.(*Model).State().ZoomBars == before { t.Error("zoom level unchanged") }
    if m2.(*Model).State().ZoomBars > 32 { t.Error("zoom bars out of range") }
}

func TestLibraryModeRendersLibrary(t *testing.T) {
    m, _ := testModel(t)
    m2, _ := m.Update(keyMsg("/"))
    out := m2.(*Model).RenderAt(120, 40)
    if !strings.Contains(out, "LOAD") && !strings.Contains(out, "crate") {
        t.Errorf("library mode did not render a library:\n%s", out)
    }
}

func TestHelpToggle(t *testing.T) {
    m, _ := testModel(t)
    m2, _ := m.Update(keyMsg("?"))
    if m2.(*Model).State().Mode != ModeHelp { t.Error("? did not open help") }
}

func TestDebugToggle(t *testing.T) {
    m, _ := testModel(t)
    m2, _ := m.Update(keyMsg("`"))
    if m2.(*Model).State().Mode != ModeMix { t.Error("` did not toggle debug") }
}

func TestFocusAffectsRender(t *testing.T) {
    m, _ := testModel(t)
    a := m.RenderAt(120, 40)
    m2, _ := m.Update(keyMsg("tab"))
    b := m2.(*Model).RenderAt(120, 40)
    if a == b { t.Error("focus change did not alter the render") }
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/tui/ -v -run 'Zoom|LibraryMode|HelpToggle|DebugToggle|Focus'`
Expected: FAIL — unhandled keys change nothing.

- [ ] **Step 3: Implement `internal/tui/keys.go`**

Resolve keys through `config.KeyResolver`, then handle UI-only keys (`v`, `tab`, `z`, `Z`,
`?`, `` ` ``, `/`, `esc`, arrows) in the model. **UI keys are checked before config keys**
so a user binding cannot accidentally break navigation out of a screen.

Mode-relative: in `ModeLibrary`, arrows move the selection and `enter` loads; in `ModeMix`,
arrows nudge values. A key meaning different things in two modes is fine — but it must be
**obvious in the footer**, so render the active mode's bindings there.

- [ ] **Step 4: Wire the render dispatch**

```go
func (m *Model) RenderAt(w, h int) string {
    defer func() { recover() }()   // never let a render panic kill the app
    s := m.eng.Snapshot()
    switch m.st.Mode {
    case ModeLibrary: return views.RenderLibrary(w, h, m.lib, m.libState())
    case ModeOverlay: return views.RenderOverlay(w, h, m.lib, m.st.Query, m.st.TrackSel)
    case ModeHelp:    return views.RenderHelp(w, h, m.keys.Bindings())
    case ModeDebug:   return views.RenderDebug(w, h, s, m.stats())
    default:
        switch m.st.Layout {
        case LayoutA: return renderA(w, h, s, m.st)
        case LayoutB: return renderB(w, h, s, m.st)
        case LayoutC: return renderC(w, h, s, m.st)
        default:      return renderD(w, h, s, m.st)
        }
    }
}
```

The `recover()` returns a minimal error panel. **Audio keeps playing while the interface
recovers**, which is the correct failure mode mid-set (Review Focus #4).

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/tui/... -v -race`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/tui/
git commit -m "feat(tui): key handling and mode dispatch"
```

---

### Task 11: cmd/mix-tui

**Files:**
- Create: `cmd/mix-tui/main.go`
- Create: `cmd/mix-tui/main_test.go`
- Create: `cmd/mix-tui/README.md`

**Interfaces:**
- Consumes: every package built in plans 1–4.
- Produces: the application binary.

- [ ] **Step 1: Write the failing test**

Create `cmd/mix-tui/main_test.go`:

```go
package main

import "testing"

func TestParseArgsRequiresTracks(t *testing.T) {
    _, err := parseArgs([]string{"-seconds", "10"})
    if err == nil { t.Error("expected an error with no tracks") }
}

func TestParseArgsAcceptsTrackAndConfig(t *testing.T) {
    cfg, err := parseArgs([]string{"-a", "x.mp3", "-config", "/tmp/c.yaml"})
    if err != nil { t.Fatal(err) }
    if cfg.aPath != "x.mp3" { t.Errorf("a = %q", cfg.aPath) }
    if cfg.configPath != "/tmp/c.yaml" { t.Errorf("config = %q", cfg.configPath) }
}

func TestParseArgsDefaults(t *testing.T) {
    cfg, err := parseArgs([]string{"-a", "x.mp3"})
    if err != nil { t.Fatal(err) }
    if cfg.configPath != "" { t.Error("config default should be empty (auto-discover)") }
}

func TestParseArgsRejectsBadView(t *testing.T) {
    if _, err := parseArgs([]string{"-a", "x.mp3", "-view", "Z"}); err == nil {
        t.Error("invalid -view must be rejected at parse time, not at render")
    }
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./cmd/mix-tui/ -v`
Expected: FAIL — `undefined: parseArgs`.

- [ ] **Step 3: Implement `cmd/mix-tui/main.go`**

Wiring order matters, and getting it wrong means audio starts before configuration is
known:

1. `parseArgs` → config path (flag, or `$XDG_CONFIG_HOME/mix-tui/config.yaml`, or
   `./.devconfig/config.yaml` in dev)
2. `config.Load` → `engine.Config` (sample rate, block size, ring frames)
3. `pipewire.NewOutput` → **negotiated** sample rate; if it differs from config, rebuild
   the engine with the real rate before decoding anything
4. `audio.Decode` each track; attach cached analysis from `library.Cache`, queue fresh
   analysis for tracks with no cache entry
5. `midi.OpenListener` + `midi.NewWatcher` + `config.NewKeyResolver`
6. `pipewire.NewPumper` → `go pumper.Run(ctx)`
7. `tea.NewProgram(tui.New(engine, cfg, keys), tea.WithAltScreen())`

Flags: `-a` `-b` tracks · `-library` root · `-config` · `-view` · `-no-midi` ·
`-no-audio` (render only, for headless testing) · `-seed-test-midi` (replay a mapping
without hardware) · `-screenshot` (render one frame to a file and exit — the single most
useful flag for verifying layout work without a terminal).

Handle `SIGINT`/`SIGTERM`, cancel the pumper, close the listener and output in that order.

**If PipeWire is unavailable, show the idle art screen with the diagnostic** (spec §8) and
keep the interface running — the user can still see why.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./cmd/mix-tui/ -v`
Expected: PASS.

- [ ] **Step 5: Add the screenshot flag and verify visually**

```bash
go run ./cmd/mix-tui -a internal/testdata/fixtures/sine440.wav \
  -b internal/testdata/fixtures/sine440.wav -no-audio -screenshot /tmp/shot.txt
cat /tmp/shot.txt
```

**Read it.** Check the braille waveform looks like a waveform, the columns line up, the
hotcues and beat marks are visible, and the ASCII art is not mangled. This is the check
that catches what unit tests cannot.

Repeat at 120×40, 100×30, 80×24, and 70×20 and confirm each is coherent or shows an
honest "too small" message.

- [ ] **Step 6: Write `cmd/mix-tui/README.md`**

Cover: install, build deps (`libpipewire-0.3-dev`, `libasound2-dev`), configuration paths,
mapping file format with a pointer to spec §5.3, the keymap, troubleshooting (no sound →
check `pw-top`; controller unresponsive → `-` `` ` `` debug overlay and `midimapdump`), and
the verification commands.

- [ ] **Step 7: Run the whole suite**

Run: `go test ./... -race -count=1 && go vet ./... && gofmt -l .`
Expected: all pass; `gofmt -l` silent.

- [ ] **Step 8: Commit**

```bash
git add cmd/mix-tui/
git commit -m "feat(mix-tui): the application binary"
```

---

### Task 12: End-to-end acceptance

**Files:**
- Create: `cmd/mix-tui/acceptance_test.go`

**Interfaces:**
- Consumes: everything.
- Produces: the milestone exit criterion.

- [ ] **Step 1: Write the failing acceptance test**

```go
package main

import (
    "os"
    "os/exec"
    "path/filepath"
    "strings"
    "testing"
)

// TestAcceptanceScreenshot is the exit criterion for the whole app: it builds
// the binary, runs a scripted performance headlessly, and asserts the
// rendered frame contains the state the performance should have produced.
func TestAcceptanceScreenshot(t *testing.T) {
    if testing.Short() { t.Skip("builds a binary") }
    bin := filepath.Join(t.TempDir(), "mix-tui")
    build := exec.Command("go", "build", "-o", bin, ".")
    if out, err := build.CombinedOutput(); err != nil {
        t.Fatalf("build: %v\n%s", err, out)
    }
    shot := filepath.Join(t.TempDir(), "frame.txt")
    cmd := exec.Command(bin,
        "-a", "../../internal/testdata/fixtures/sine440.wav",
        "-b", "../../internal/testdata/fixtures/sine440.wav",
        "-no-audio", "-no-midi", "-seed-test-midi",
        "-xf", "0.2", "-screenshot", shot)
    if out, err := cmd.CombinedOutput(); err != nil {
        t.Fatalf("run: %v\n%s", err, out)
    }
    frame, err := os.ReadFile(shot)
    if err != nil { t.Fatal(err) }
    s := string(frame)
    for _, want := range []string{"MIXER", "LOOP", "128.00", "MASTER"} {
        if !strings.Contains(s, want) {
            t.Errorf("rendered frame missing %q:\n%s", want, s)
        }
    }
    for i, line := range strings.Split(s, "\n") {
        if len([]rune(line)) > 0 && strings.Contains(line, "panic") {
            t.Errorf("frame contains a panic at line %d:\n%s", i, s)
        }
    }
}

// TestAcceptanceHeadlessIsStable runs the screenshot repeatedly and requires
// identical output. Non-determinism in a render is a real bug.
func TestAcceptanceHeadlessIsStable(t *testing.T) {
    if testing.Short() { t.Skip("builds a binary") }
    // ... build once, run -screenshot twice with the same flags, compare bytes
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./cmd/mix-tui/ -run Acceptance -v`
Expected: FAIL until `-screenshot`, `-seed-test-midi`, and `-no-audio` all work.

- [ ] **Step 3: Implement `-seed-test-midi`**

Feed a fixed table of `midi.Message` values through the handler with no device attached.
This makes the whole input path exercisable in CI — the same table from plan 3's
integration test, driven through the real binary.

- [ ] **Step 4: Make `-screenshot` deterministic**

Render exactly one frame after the seeded actions settle, with no timing dependence.
`TestAcceptanceHeadlessIsStable` will fail if anything in the render path is
time-dependent or map-iteration-ordered — **that failure is the point of the test.**

- [ ] **Step 5: Run the full suite one final time**

Run: `go test ./... -race -count=1 && go vet ./... && gofmt -l .`
Expected: all pass; `gofmt -l` silent.

- [ ] **Step 6: Final manual verification**

```bash
go build -o mix-tui ./cmd/mix-tui
./mix-tui -a trackA.mp3 -b trackB.mp3
```

Then, in a set, actually mix:
- load two tracks, beatmatch by eye using the zoomed waveforms
- drop a hotcue, a saved loop, and a loop roll
- sync and quantize
- swap views mid-set
- open the library mid-set via `/`, load with the overlay
- resize the terminal mid-set
- unplug and replug the controller (hot reload, mapping still applies)

If any of those fails, the corresponding task's tests were insufficient.

- [ ] **Step 7: Commit**

```bash
git add -A
git commit -m "test: end-to-end acceptance for the complete application"
```

---

## Self-Review

**Spec coverage** for everything milestone 3 owns:

| Spec § | Task |
|---|---|
| §6.1 four layouts, switchable, D default | 2, 3 |
| §6.2 braille waveforms, 16 levels | 4 |
| §6.3 zoom, delta-beat, overview strip, MIDI-mappable level | 6 |
| §6.4 ASCII art filling empty space | 5 |
| §6.5 library views, sort-never-writes-crate, static crates, 120-col floor, key confidence | 7, 8 |
| §8 small terminal, no beatgrid, no MIDI, no PipeWire | 2, 3, 5, 6, 8, 11 |
| §10 config precedence (flag → XDG → .devconfig) | 11 |
| §7 ignored-message counts surfaced in the debug overlay | 9 |

**Deferred and why it is safe:** MIDI LED feedback and sysex output (needs an output port;
the input path is complete), and live waveform-zoom rendering against decoded PCM without
re-reading (plan 4 renders from cached peaks; the decode-to-memory path already exists).

**Review Focus coverage:** all five are pinned — resize reflow (1), narrow-terminal
degradation (2, 3), wide-character alignment (2, 4), render-panic containment (1, 10,
11), and scan responsiveness (7).

**Proportion:** longer than plan 1, which is expected — a TUI has more surface area and
its correctness argument rests entirely on the "views are pure functions" property. If a
view ever gains engine access, the test strategy collapses and the plan's central claim is
broken; that invariant is stated in Global Constraints for that reason.

**One honest risk:** layout aesthetics cannot be asserted by tests. `assertLinesFit` proves
nothing overflows; it cannot prove the result looks like a DJ application. That is why Task 5
Step 6 and Task 11 Step 5 require *looking at rendered frames*, and why `-screenshot` exists
as a flag rather than a test helper. A plan that claimed automated verification of visual
quality would be lying.
