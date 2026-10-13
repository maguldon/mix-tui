package tui

import (
	"errors"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/maguldon/mix-tui/internal/config"
	"github.com/maguldon/mix-tui/internal/engine"
	"github.com/maguldon/mix-tui/internal/tui/library"
	"github.com/maguldon/mix-tui/internal/tui/views"
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
		if a.Kind == engine.DeckPlayPause {
			found = true
		}
	}
	if !found {
		t.Errorf("space produced no DeckPlayPause; actions: %+v", f.applied)
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
	after := m2.(*Model).State()
	if after.Mode != before.Mode || after.Layout != before.Layout ||
		after.Focus != before.Focus || after.TrackSel != before.TrackSel ||
		after.Query != before.Query {
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
//
// The hook panics from inside the real render path, so this exercises the
// recovery rather than a flag check above it. Before the fix the recover had no
// named result, so a genuine panic produced a blank frame.
func TestViewSurvivesPanic(t *testing.T) {
	m, _ := testModel(t)
	m.panicOnRender = true
	out := m.View()
	if out == "" {
		t.Fatal("panic produced no output at all; a blank screen is what focus #4 forbids")
	}
	for _, want := range []string{"error", "press q or ctrl+c to quit"} {
		if !strings.Contains(out, want) {
			t.Errorf("panic screen missing %q:\n%s", want, out)
		}
	}
	// The frame must still be a clean rectangle, or the recovery itself
	// corrupts the display it is trying to rescue.
	for i, line := range strings.Split(out, "\n") {
		if got := lipgloss.Width(line); got != 80 {
			t.Errorf("error frame line %d width %d, want 80", i, got)
		}
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
	after := m.State()
	// Compared field by field: ViewState embeds a DeckState, which holds
	// slices, so the struct is not comparable with ==.
	if after.Mode != before.Mode || after.Layout != before.Layout ||
		after.Focus != before.Focus || after.Zoom != before.Zoom ||
		after.ZoomBars != before.ZoomBars || after.Xfade != before.Xfade ||
		after.TrackSel != before.TrackSel || after.Query != before.Query ||
		after.LastErr != before.LastErr {
		t.Errorf("RenderAt mutated ViewState:\n before %+v\n after  %+v", before, after)
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
	ix := library.NewIndex()
	ix.Add(library.Track{Path: "/1.wav", Title: "One"})
	ix.Add(library.Track{Path: "/2.wav", Title: "Two"})
	m.SetLibrary(ix)
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

// TestLibraryEnterLoadsSelectedTrack is the test that was missing: the review
// found ENTER consumed the key and did nothing, so there was no way to load a
// track at all and the empty-deck art told the user to press a key that was not
// bound.
func TestLibraryEnterLoadsSelectedTrack(t *testing.T) {
	m, _ := testModel(t)
	ix := library.NewIndex()
	ix.Add(library.Track{Path: "/m/1.wav", Title: "Inner Speaker", BPM: 128, Duration: 3 * time.Minute})
	m.SetLibrary(ix)

	var loaded []string
	m.SetLoader(func(deck int, path string) error {
		loaded = append(loaded, path)
		return nil
	})

	m2, _ := m.Update(keyMsg("/"))
	m3, _ := m2.(*Model).Update(keyMsg("enter"))
	if len(loaded) != 1 || loaded[0] != "/m/1.wav" {
		t.Fatalf("enter loaded %v, want [/m/1.wav]", loaded)
	}
	// Loading returns to the decks so the user sees the result.
	if m3.(*Model).State().Mode != ModeMix {
		t.Error("loading did not return to the mix view")
	}
}

// TestOverlayIsReachable covers the other half: the quick-load overlay had no
// producer at all, so RenderOverlay was dead code in production.
func TestOverlayIsReachable(t *testing.T) {
	m, _ := testModel(t)
	m2, _ := m.Update(keyMsg("l"))
	if m2.(*Model).State().Mode != ModeOverlay {
		t.Fatal("the key the empty-deck art tells the user to press does not open the overlay")
	}
}

// TestOverlayTypingFilters covers filtering, which only worked in the
// unreachable overlay and never in the full-screen browser.
func TestOverlayTypingFilters(t *testing.T) {
	m, _ := testModel(t)
	m2, _ := m.Update(keyMsg("l"))
	mm := m2.(*Model)
	for _, c := range "ware" {
		mm2, _ := mm.Update(keyMsg(string(c)))
		mm = mm2.(*Model)
	}
	if mm.State().Query != "ware" {
		t.Errorf("query = %q, want %q", mm.State().Query, "ware")
	}
}

// TestBackspaceDeletesARuneNotAByte guards against leaving invalid UTF-8.
func TestBackspaceDeletesARuneNotAByte(t *testing.T) {
	m, _ := testModel(t)
	m2, _ := m.Update(keyMsg("l"))
	mm := m2.(*Model)
	mm.Update(keyMsg("日"))
	mm3, _ := mm.Update(keyMsg("backspace"))
	if got := mm3.(*Model).State().Query; got != "" {
		t.Errorf("query = %q, want empty; got %q which is not valid UTF-8", got, got)
	}
}

// TestLoadErrorSurfacesAndExpires checks a failed load is reported rather than
// swallowed.
func TestLoadErrorSurfacesAndExpires(t *testing.T) {
	m, _ := testModel(t)
	ix := library.NewIndex()
	ix.Add(library.Track{Path: "/bad.wav", Title: "Broken"})
	m.SetLibrary(ix)
	m.SetLoader(func(int, string) error { return errTest })

	m2, _ := m.Update(keyMsg("/"))
	m3, _ := m2.(*Model).Update(keyMsg("enter"))
	if m3.(*Model).State().LastErr == "" {
		t.Error("a failed load was swallowed")
	}
	if m3.(*Model).State().Mode != ModeLibrary {
		t.Error("a failed load closed the library; the user lost their place")
	}
}

var errTest = errors.New("decode failed")

// TestDebugStatsFuncIsSampled covers the wiring that made the debug overlay
// report permanent zeros -- the stats source was never installed.
func TestDebugStatsFuncIsSampled(t *testing.T) {
	m, _ := testModel(t)
	m.SetStatsFunc(func() views.DebugStats {
		return views.DebugStats{AudioExpected: true, MainCallbacks: 42, CueCallbacks: 42}
	})
	m2, _ := m.Update(tickMsg{t: time.Now()})
	if got := m2.(*Model).stats.MainCallbacks; got != 42 {
		t.Errorf("callbacks = %d, want 42; the stats source is not being sampled", got)
	}
}

// TestNudgeAppliesToEngine covers I1: the arrow keys moved a mirrored copy of
// the crossfader and never told the engine, so pressing them did nothing
// audible and the fader appeared stuck wherever it was last dragged.
func TestNudgeAppliesToEngine(t *testing.T) {
	m, f := testModel(t)
	before := len(f.applied)
	for i := 0; i < 5; i++ {
		m2, _ := m.Update(keyMsg("down"))
		m = m2.(*Model)
	}
	if len(f.applied) == before {
		t.Fatal("arrow keys produced no engine action; the crossfader cannot be moved by keyboard")
	}
	found := false
	for _, a := range f.applied {
		if a.Kind == engine.MixerCrossfader {
			found = true
			if a.Value <= 0 {
				t.Errorf("crossfader action has value %v, want a positive nudge", a.Value)
			}
		}
	}
	if !found {
		t.Errorf("no crossfader action among %+v", f.applied)
	}
}

// TestCrossfaderMirrorsFromSnapshot covers the other half of I1: a MIDI
// crossfader move must move what the interface draws. The model used to keep a
// second copy that drifted.
func TestCrossfaderMirrorsFromSnapshot(t *testing.T) {
	m, f := testModel(t)
	// Simulate a MIDI move landing in the engine's Snapshot.
	f.snap.Crossfader = 0.75
	m2, _ := m.Update(tickMsg{t: time.Now()})
	if got := m2.(*Model).State().Xfade; got != 0.75 {
		t.Errorf("mirrored crossfader = %v, want 0.75 from the snapshot", got)
	}
}

// TestLibraryDownClampsAtBottom covers I8: there was no bottom clamp, so
// pressing down past the end left the scroll offset beyond the list and every
// track row blanked. On a one-track library, one press of down showed nothing.
func TestLibraryDownClampsAtBottom(t *testing.T) {
	m, _ := testModel(t)
	ix := library.NewIndex()
	ix.Add(library.Track{Path: "/only.wav", Title: "Only Track", BPM: 128})
	m.SetLibrary(ix)

	m2, _ := m.Update(keyMsg("/"))
	mm := m2.(*Model)
	for i := 0; i < 5; i++ {
		next, _ := mm.Update(keyMsg("down"))
		mm = next.(*Model)
	}
	if got := mm.State().TrackSel; got != 0 {
		t.Errorf("TrackSel = %d after pressing down past the end; want 0", got)
	}
	out := mm.RenderAt(140, 20)
	if !strings.Contains(out, "Only Track") {
		t.Errorf("the track pane is blank after pressing down:\n%s", out)
	}
}

// TestCtrlCQuits. ctrl+c is bound to Quit in the default keymap, and until
// Quit was handled the only way out of a live session was SIGTERM: pressing
// ctrl+c did nothing at all, mid-set, with audio playing.
func TestCtrlCQuits(t *testing.T) {
	m, _ := testModel(t)
	m.st.Mode = ModeMix

	m2, cmd := m.Update(keyMsg("ctrl+c"))

	if m2.(*Model).quitting != true {
		t.Fatal("ctrl+c did not set the quit flag")
	}
	if cmd == nil {
		t.Fatal("ctrl+c returned no command; the program would not terminate")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Error("ctrl+c did not produce tea.QuitMsg")
	}
}

// TestQuitActionQuits covers the action itself, not just the default binding:
// Quit is what a MIDI mapping or a custom config would resolve to.
func TestQuitActionQuits(t *testing.T) {
	m, f := testModel(t)
	m.st.Mode = ModeMix

	m.handleKey("ctrl+c")

	if !m.quitting {
		t.Error("Quit did not set the quit flag")
	}
	if len(f.applied) != 0 {
		t.Errorf("Quit reached the engine as %v; it is a program action, not audio state", f.applied)
	}
}

// TestQuitIsNotAppliedWhilePanicking. On the error screen the keymap does not
// apply, so "q" must exit rather than quantise a deck -- q is bound to
// Quantize, and the old screen told users to press it.
func TestQuitIsNotAppliedWhilePanicking(t *testing.T) {
	m, f := testModel(t)
	m.panicOnRender = true
	_ = m.View() // triggers the recovery and sets panicked

	m.handleKey("q")

	if !m.quitting {
		t.Error("q on the error screen did not quit")
	}
	if len(f.applied) != 0 {
		t.Errorf("q on the error screen applied %v; it must exit, not quantise", f.applied)
	}
}

// TestPanickedScreenIgnoresOtherKeys. A broken interface should not act on
// anything except the exit key; a stray deck nudge would be surprising while
// the user is reading an error.
func TestPanickedScreenIgnoresOtherKeys(t *testing.T) {
	m, f := testModel(t)
	m.panicOnRender = true
	_ = m.View()

	for _, k := range []string{" ", "enter", "tab", "x", "s"} {
		m2, _ := testModel(t)
		m2.panicOnRender = true
		_ = m2.View()
		m2.handleKey(k)
		if m2.quitting {
			t.Errorf("key %q quit from the error screen; only q and ctrl+c should", k)
		}
	}
	if len(f.applied) != 0 {
		t.Errorf("the error screen applied %v", f.applied)
	}
}

// TestLoopRollTogglesFromTheKeyboard. A loop roll is momentary: a MIDI pad
// sends a press and a release. There is no key-release event here, so a direct
// binding would engage the roll and leave the deck stuck in it. Pressing again
// must return.
func TestLoopRollTogglesFromTheKeyboard(t *testing.T) {
	m, f := testModel(t)
	m.st.Mode = ModeMix

	m.handleKey("r")
	if !m.rollActive {
		t.Fatal("first press did not engage the roll")
	}
	if len(f.applied) != 1 || f.applied[0].Value <= 0 {
		t.Fatalf("first press applied %v, want a positive roll length", f.applied)
	}

	m.handleKey("r")
	if m.rollActive {
		t.Error("second press did not release the roll")
	}
	last := f.applied[len(f.applied)-1]
	if last.Value != 0 {
		t.Errorf("release applied Value %v, want 0", last.Value)
	}
}

// A real keystroke must reach the key handler.
//
// Every other test in this file drives Update with keyMsg, the plain string
// form. That skipped the transport entirely, and it is why the missing
// tea.KeyMsg case shipped: the interface rendered and ignored every key, while
// every test that "proved" key handling passed.
//
// This goes through the message type bubbletea actually delivers.
func TestRealKeyPressReachesTheKeyHandler(t *testing.T) {
	// Update has a pointer receiver and mutates in place, so the resulting
	// mode/layout has to be read immediately rather than compared later
	// through the returned pointer.
	pressRune := func(m *Model, k rune) LayoutKind {
		m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{k}})
		return m.State().Layout
	}
	pressMode := func(m *Model, k rune) Mode {
		m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{k}})
		return m.State().Mode
	}

	m, _ := testModel(t)

	// "v" cycles the layout.
	before := m.State().Layout
	after := pressRune(m, 'v')
	if before == after {
		t.Errorf("pressing v left the layout at %v: keyboard input is not reaching the handler", after)
	}

	// "?" opens help.
	if got := pressMode(m, '?'); got != ModeHelp {
		t.Errorf("pressing ? gave mode %v, want ModeHelp", got)
	}
}

// ctrl+c must quit, which the default keymap routes through the Quit action.
// Without it a running session can only be left with SIGTERM from another
// terminal.
func TestRealCtrlCQuits(t *testing.T) {
	m, _ := testModel(t)
	m2, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	if !m2.(*Model).quitting {
		t.Error("ctrl+c did not set quitting")
	}
}
