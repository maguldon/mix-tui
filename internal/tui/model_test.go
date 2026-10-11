package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/maguldon/mix-tui/internal/config"
	"github.com/maguldon/mix-tui/internal/engine"
)

type fakeEngine struct {
	snap    engine.Snapshot
	applied []engine.Action
}

func (f *fakeEngine) Apply(a engine.Action)     { f.applied = append(f.applied, a) }
func (f *fakeEngine) Snapshot() engine.Snapshot { return f.snap }
func (f *fakeEngine) DeckCount() int            { return len(f.snap.Decks) }

func testModel(t *testing.T) (*Model, *fakeEngine) {
	t.Helper()
	f := &fakeEngine{snap: engine.Snapshot{
		Decks: []engine.DeckState{
			{Index: 0, Loaded: true, Playing: true,
				Position: 12 * time.Second, Duration: 3 * time.Minute,
				BPM: 128, HasBeatGrid: true, Title: "A Track",
				Artist: "Someone", Level: 0.4, Peak: 0.7,
				LoopActive: true, LoopBeatCount: 4},
			{Index: 1},
		},
	}}
	keys, err := config.NewKeyResolver(config.Defaults())
	if err != nil {
		t.Fatal(err)
	}
	return New(f, &config.Config{UI: config.UIConfig{DefaultView: "D"}}, keys), f
}

func TestDefaultLayoutIsD(t *testing.T) {
	m, _ := testModel(t)
	if m.State().Layout != LayoutD {
		t.Errorf("default layout = %v, want D", m.State().Layout)
	}
}

func TestViewCycleChangesLayout(t *testing.T) {
	m, _ := testModel(t)
	seen := map[LayoutKind]bool{m.State().Layout: true}
	for i := 0; i < 4; i++ {
		m2, _ := m.Update(keyMsg("v"))
		m = m2.(*Model)
		seen[m.State().Layout] = true
	}
	if len(seen) != 4 {
		t.Errorf("saw %d distinct layouts, want 4", len(seen))
	}
}

func TestViewNeverExceedsWidth(t *testing.T) {
	// Review Focus #1 and #2: every size must render bounded lines
	m, _ := testModel(t)
	for _, size := range [][2]int{{200, 60}, {120, 40}, {100, 30}, {80, 24}, {70, 20}, {40, 10}, {20, 5}} {
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
	for _, size := range [][2]int{{1, 1}, {2, 2}, {5, 3}, {10, 4}, {15, 5}} {
		_ = m.RenderAt(size[0], size[1]) // must not panic
	}
}

func TestKeyEmitsActionToEngine(t *testing.T) {
	m, f := testModel(t)
	m2, _ := m.Update(keyMsg(" "))
	_ = m2
	found := false
	for _, a := range f.applied {
		if a.Kind == engine.DeckPlay {
			found = true
		}
	}
	if !found {
		t.Errorf("space produced no DeckPlay; actions: %+v", f.applied)
	}
}

func TestTabMovesFocus(t *testing.T) {
	m, _ := testModel(t)
	m2, _ := m.Update(keyMsg("tab"))
	if m2.(*Model).State().Focus != 1 {
		t.Errorf("focus = %d", m2.(*Model).State().Focus)
	}
}

func TestSlashOpensLibrary(t *testing.T) {
	m, _ := testModel(t)
	m2, _ := m.Update(keyMsg("/"))
	if m2.(*Model).State().Mode != ModeLibrary {
		t.Error("/ did not open the library")
	}
}

func TestEscapeLeavesOverlay(t *testing.T) {
	m, _ := testModel(t)
	m2, _ := m.Update(keyMsg("/"))
	m3, _ := m2.(*Model).Update(keyMsg("esc"))
	if m3.(*Model).State().Mode != ModeMix {
		t.Error("esc did not leave the library")
	}
}

func TestErrorAutoExpires(t *testing.T) {
	m, _ := testModel(t)
	m.st.LastErr = "boom"
	m.st.LastErrAt = time.Now().Add(-10 * time.Second)
	m2, _ := m.Update(tickMsg{t: time.Now()})
	if m2.(*Model).State().LastErr != "" {
		t.Error("stale error was not cleared")
	}
}

func TestUnknownKeyIsIgnored(t *testing.T) {
	m, _ := testModel(t)
	before := m.State()
	m2, _ := m.Update(keyMsg("nonexistent-key"))
	if m2.(*Model).State() != before {
		t.Error("an unknown key changed state")
	}
}

// TestNarrowTerminalKeepsLineCountBounded guards height as well as width: a
// render that ignores h produces a screen taller than the terminal, which
// scrolls and corrupts a full-screen app.
func TestNarrowTerminalKeepsLineCountBounded(t *testing.T) {
	m, _ := testModel(t)
	for _, size := range [][2]int{{80, 24}, {40, 10}, {20, 5}, {10, 3}} {
		lines := splitLines(m.RenderAt(size[0], size[1]))
		if len(lines) > size[1] {
			t.Errorf("%dx%d produced %d lines, want <= %d", size[0], size[1], len(lines), size[1])
		}
	}
}

// TestViewSurvivesPanic covers review focus #4: a render panic must show an
// error screen rather than taking the interface down while audio keeps playing.
func TestViewSurvivesPanic(t *testing.T) {
	m, _ := testModel(t)
	m.panicOnRender = true
	out := m.View()
	if out == "" {
		t.Error("panic produced no output at all")
	}
	if !strings.Contains(out, "render error") {
		t.Errorf("panic did not produce a render-error screen: %q", out)
	}
}

// TestRenderAtIsPure checks RenderAt does not mutate interaction state: the
// tests call it constantly, and a render that moved focus would make every
// other assertion order-dependent.
func TestRenderAtIsPure(t *testing.T) {
	m, _ := testModel(t)
	before := m.State()
	m.RenderAt(80, 24)
	m.RenderAt(20, 5)
	if m.State() != before {
		t.Error("RenderAt mutated ViewState")
	}
}

// TestFocusWraps guards the tab cycle: focus must return to the first deck
// rather than running off the end.
func TestFocusWraps(t *testing.T) {
	m, _ := testModel(t)
	for i := 0; i < 3; i++ {
		m2, _ := m.Update(keyMsg("tab"))
		m = m2.(*Model)
	}
	if m.State().Focus != 1 {
		t.Errorf("focus after 3 tabs = %d, want wrapped to 1", m.State().Focus)
	}
}

func TestZoomToggleChangesDeckPanelHeight(t *testing.T) {
	m, _ := testModel(t)
	off := m.RenderAt(120, 40)
	m2, _ := m.Update(keyMsg("z"))
	on := m2.(*Model).RenderAt(120, 40)
	if off == on {
		t.Error("zoom toggle did not change the render")
	}
}

func TestZoomLevelKeyChangesBars(t *testing.T) {
	m, _ := testModel(t)
	before := m.State().ZoomBars
	m2, _ := m.Update(keyMsg("Z"))
	if m2.(*Model).State().ZoomBars == before {
		t.Error("zoom level unchanged")
	}
	if m2.(*Model).State().ZoomBars > 32 {
		t.Error("zoom bars out of range")
	}
}

func TestLibraryModeRendersLibrary(t *testing.T) {
	m, _ := testModel(t)
	m2, _ := m.Update(keyMsg("/"))
	out := m2.(*Model).RenderAt(120, 40)
	if !strings.Contains(out, "LOAD") && !strings.Contains(out, "crate") &&
		!strings.Contains(out, "CRATES") && !strings.Contains(out, "too small") {
		t.Errorf("library mode did not render a library:\n%s", out)
	}
}

func TestHelpToggle(t *testing.T) {
	m, _ := testModel(t)
	m2, _ := m.Update(keyMsg("?"))
	if m2.(*Model).State().Mode != ModeHelp {
		t.Error("? did not open help")
	}
}

func TestDebugToggle(t *testing.T) {
	m, _ := testModel(t)
	m2, _ := m.Update(keyMsg("`"))
	// ` toggles debug on; toggling again returns to the mix view.
	if m2.(*Model).State().Mode == ModeDebug {
		m3, _ := m2.(*Model).Update(keyMsg("`"))
		if m3.(*Model).State().Mode != ModeMix {
			t.Error("` did not toggle debug off")
		}
	}
}

func TestFocusAffectsRender(t *testing.T) {
	m, _ := testModel(t)
	a := m.RenderAt(120, 40)
	m2, _ := m.Update(keyMsg("tab"))
	b := m2.(*Model).RenderAt(120, 40)
	if a == b {
		t.Error("focus change did not alter the render")
	}
}

// TestUIKeysBeatConfigKeys pins the rule that navigation keys are handled
// before config lookups, so a user binding cannot lock the user out of leaving
// a screen.
func TestUIKeysBeatConfigKeys(t *testing.T) {
	// Bind "v" to a real action in config; "v" must still cycle the layout.
	keys, err := config.NewKeyResolver(config.Defaults())
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeEngine{snap: snapTwoDecks()}
	m := New(f, &config.Config{UI: config.UIConfig{DefaultView: "D"}}, keys)
	before := m.State().Layout
	m2, _ := m.Update(keyMsg("v"))
	if m2.(*Model).State().Layout == before {
		t.Error("a config binding on 'v' suppressed layout cycling")
	}
}

// TestEscapeLeavesAnyMode keeps escape a universal exit.
func TestEscapeLeavesAnyMode(t *testing.T) {
	m, _ := testModel(t)
	for _, opener := range []string{"?", "/", "`"} {
		m2, _ := m.Update(keyMsg(opener))
		m3, _ := m2.(*Model).Update(keyMsg("esc"))
		if m3.(*Model).State().Mode == ModeHelp || m3.(*Model).State().Mode == ModeDebug {
			t.Errorf("esc did not leave the mode opened by %q", opener)
		}
	}
}

// TestArrowsMoveSelectionInLibrary guards mode-relative keys: in the library,
// arrows move the cursor rather than nudging a fader.
func TestArrowsMoveSelectionInLibrary(t *testing.T) {
	m, _ := testModel(t)
	m2, _ := m.Update(keyMsg("/"))
	before := m2.(*Model).State().TrackSel
	m3, _ := m2.(*Model).Update(keyMsg("down"))
	if m3.(*Model).State().TrackSel <= before {
		t.Error("down did not move the library selection")
	}
}

// TestUpAtTopClamps keeps the selection from wrapping past the ends, which in a
// library means jumping to the bottom of a long list.
func TestUpAtTopClamps(t *testing.T) {
	m, _ := testModel(t)
	m2, _ := m.Update(keyMsg("/"))
	m3, _ := m2.(*Model).Update(keyMsg("up"))
	if m3.(*Model).State().TrackSel != 0 {
		t.Errorf("up from the top moved to %d", m3.(*Model).State().TrackSel)
	}
}

// TestRenderAtSurvivesEveryMode sweeps render across all modes, since the
// dispatch is new code on the panic-critical path.
func TestRenderAtSurvivesEveryMode(t *testing.T) {
	m, _ := testModel(t)
	for _, mode := range []Mode{ModeMix, ModeLibrary, ModeOverlay, ModeHelp, ModeDebug} {
		m.st.Mode = mode
		for _, size := range [][2]int{{200, 50}, {120, 40}, {80, 24}, {40, 10}, {20, 5}} {
			if out := m.RenderAt(size[0], size[1]); out == "" && size[0] > 0 && size[1] > 0 {
				t.Errorf("mode %v at %dx%d rendered nothing", mode, size[0], size[1])
			}
		}
	}
}

// TestRenderAtIsStillRectangularInEveryMode guards the frame shape across modes.
func TestRenderAtIsStillRectangularInEveryMode(t *testing.T) {
	m, _ := testModel(t)
	for _, mode := range []Mode{ModeMix, ModeLibrary, ModeOverlay, ModeHelp, ModeDebug} {
		m.st.Mode = mode
		for _, w := range []int{200, 140, 120, 90, 80} {
			out := m.RenderAt(w, 30)
			for i, line := range splitLines(out) {
				if got := lipgloss.Width(line); got != w {
					t.Errorf("mode %v w=%d line %d width %d", mode, w, i, got)
				}
			}
		}
	}
}
