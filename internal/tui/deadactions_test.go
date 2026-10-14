package tui

import (
	"testing"

	"github.com/maguldon/mix-tui/internal/engine"
	"github.com/maguldon/mix-tui/internal/tui/views"
)

// Every action a mapping file can name has to do something.
//
// These nine parsed from a mapping file, resolved through the keymap, and were
// then dropped by applyAction and Engine.Apply alike: a controller could be
// configured with them, the config would look correct, and nothing would happen
// at the desk. A key the application silently ignores is worse than one it does
// not offer, because the mapping file is the evidence that it was wired.

// TestViewPrevWalksTheLayoutCycleBackwards covers the missing direction. "v"
// has always gone forwards; there was no way back at all.
func TestViewPrevWalksTheLayoutCycleBackwards(t *testing.T) {
	m, _ := testModel(t)
	m.st.Layout = LayoutD
	m.applyAction(engine.Action{Kind: engine.ViewPrev})
	if m.st.Layout != LayoutC {
		t.Errorf("ViewPrev from D = %v, want C", m.st.Layout)
	}

	// Four steps forwards must return to where it started, so prev and next
	// describe one cycle rather than two that drift apart.
	at := m.st.Layout
	for i := 0; i < 4; i++ {
		m.applyAction(engine.Action{Kind: engine.ViewNext})
	}
	if m.st.Layout != at {
		t.Errorf("four ViewNext from %v landed on %v, want %v", at, m.st.Layout, at)
	}
}

func TestLayoutPrevWrapsAtBothEnds(t *testing.T) {
	for start, want := range map[LayoutKind]LayoutKind{
		LayoutA: LayoutD, LayoutB: LayoutA, LayoutC: LayoutB, LayoutD: LayoutC,
	} {
		if got := start.prev(); got != want {
			t.Errorf("%v.prev() = %v, want %v", start, got, want)
		}
	}
}

// TestFocusNextMovesDeckFocus mirrors TAB, including the rule that a mode which
// does not draw the decks must not have deck focus moved behind the user's back.
func TestFocusNextMovesDeckFocus(t *testing.T) {
	m, _ := testModel(t)
	m.st.Mode = ModeMix
	m.applyAction(engine.Action{Kind: engine.FocusNext})
	if m.st.Focus != 1 {
		t.Errorf("FocusNext did not move deck focus: %d", m.st.Focus)
	}

	m2, _ := testModel(t)
	m2.st.Mode = ModeSettings
	m2.applyAction(engine.Action{Kind: engine.FocusNext})
	if m2.st.Focus != 0 {
		t.Errorf("FocusNext moved deck focus behind the settings screen: %d", m2.st.Focus)
	}
}

func TestDebugAndHelpToggleTheirScreens(t *testing.T) {
	for _, tc := range []struct {
		kind engine.ActionKind
		want Mode
	}{
		{engine.DebugToggle, ModeDebug},
		{engine.HelpToggle, ModeHelp},
	} {
		m, _ := testModel(t)
		m.st.Mode = ModeMix
		m.applyAction(engine.Action{Kind: tc.kind})
		if m.st.Mode != tc.want {
			t.Errorf("%v = mode %v, want %v", tc.kind, m.st.Mode, tc.want)
		}
		// And off again, so it is a toggle and not a one-way door.
		m.applyAction(engine.Action{Kind: tc.kind})
		if m.st.Mode != ModeMix {
			t.Errorf("a second %v = mode %v, want back to the mix view", tc.kind, m.st.Mode)
		}
	}
}

// TestLibraryCloseLeavesEitherBrowserAndResets covers both browsers: the overlay
// is a library too, and a close that only knew about the full browser left the
// overlay uncloseable by name.
func TestLibraryCloseLeavesEitherBrowserAndResets(t *testing.T) {
	for _, mode := range []Mode{ModeLibrary, ModeOverlay} {
		m, _ := libraryModel(t)
		m.st.Mode = mode
		m.st.ShowSort = true
		m.st.SortBy = 3
		m.st.CrateSel = 2

		m.applyAction(engine.Action{Kind: engine.LibraryClose})

		if m.st.Mode != ModeMix {
			t.Errorf("LibraryClose from %v = mode %v, want the mix view", mode, m.st.Mode)
		}
		if m.st.ShowSort || m.st.SortBy != 0 || m.st.CrateSel != 0 {
			t.Errorf("LibraryClose from %v left browser state behind: %+v", mode, m.st)
		}
	}
}

// TestLibraryLoadLoadsTheHighlightedTrack is the action behind a "load" knob.
func TestLibraryLoadLoadsTheHighlightedTrack(t *testing.T) {
	var deck int
	var path string
	m, _ := libraryModel(t)
	m.SetLoader(func(d int, p string) error {
		deck, path = d, p
		return nil
	})
	m.st.LibPane = views.PaneTracks
	m.st.TrackSel = 1

	m.applyAction(engine.Action{Kind: engine.LibraryLoad})

	if m.st.Mode != ModeMix {
		t.Errorf("LibraryLoad did not leave the browser: mode %v", m.st.Mode)
	}
	if path == "" {
		t.Fatal("LibraryLoad loaded nothing into any deck")
	}
	if want := "/2.wav"; path != want {
		t.Errorf("LibraryLoad loaded %q, want the highlighted track %q", path, want)
	}
	if deck != 0 {
		t.Errorf("LibraryLoad loaded into deck %d, want the focused deck 0", deck)
	}
}

// TestLibrarySearchStartsAFreshQuery is deliberately not the same as
// LibraryOpen. Keeping the old query means a second press narrows from text the
// user has already moved past -- and in the overlay that query is the only
// field there is.
func TestLibrarySearchStartsAFreshQuery(t *testing.T) {
	m, _ := libraryModel(t)
	m.st.Mode = ModeMix
	m.st.Query = "old text"

	m.applyAction(engine.Action{Kind: engine.LibrarySearch})

	if m.st.Mode != ModeLibrary {
		t.Errorf("LibrarySearch did not open the browser: mode %v", m.st.Mode)
	}
	if m.st.Query != "" || m.st.QueryCursor != 0 {
		t.Errorf("LibrarySearch kept the old query %q at caret %d", m.st.Query, m.st.QueryCursor)
	}
	if m.st.LibPane != views.PaneTracks {
		t.Errorf("LibrarySearch left focus on pane %v", m.st.LibPane)
	}
}

func TestLibrarySortTogglesTheSortMenu(t *testing.T) {
	m, _ := libraryModel(t)
	m.st.Mode = ModeLibrary

	m.applyAction(engine.Action{Kind: engine.LibrarySort})
	if !m.st.ShowSort {
		t.Error("LibrarySort did not open the sort menu")
	}
	m.applyAction(engine.Action{Kind: engine.LibrarySort})
	if m.st.ShowSort {
		t.Error("LibrarySort did not close the sort menu")
	}
}

// TestLibrarySortIsRefusedInTheOverlay because the overlay draws no menu and no
// sort column. Opening it would set state the user cannot see or undo.
func TestLibrarySortIsRefusedInTheOverlay(t *testing.T) {
	m, _ := libraryModel(t)
	m.st.Mode = ModeOverlay
	m.applyAction(engine.Action{Kind: engine.LibrarySort})
	if m.st.ShowSort {
		t.Error("LibrarySort opened a menu the overlay does not draw")
	}
}

func TestLibraryFocusNextCyclesThePanes(t *testing.T) {
	m, _ := libraryModel(t)
	m.st.Mode = ModeLibrary
	m.st.LibPane = views.PaneCrates

	m.applyAction(engine.Action{Kind: engine.LibraryFocusNext})

	if m.st.LibPane == views.PaneCrates {
		t.Error("LibraryFocusNext did not move the pane cursor")
	}

	// It must agree with TAB from the same starting pane, or a mapping file
	// names a different action from the key sitting beside it on the desk.
	m2, _ := libraryModel(t)
	m2.st.Mode = ModeLibrary
	m2.st.LibPane = views.PaneCrates
	m2.handleKey("tab")

	if m.st.LibPane != m2.st.LibPane {
		t.Errorf("LibraryFocusNext and TAB disagree from the same pane: action -> %v, TAB -> %v",
			m.st.LibPane, m2.st.LibPane)
	}
}

// TestEveryTActionOwnedActionIsLive is the standing guarantee.
//
// Deck, mixer, loop and sync actions belong to Engine.Apply and are deliberately
// absent from the TUI's switch. Everything in the View and Library families is
// view state, so the TUI owns it -- and every one of these parsed from a mapping
// file and resolved through the keymap before this existed. Nine of them were
// dropped on the floor.
//
// A mapping file is the evidence that an action was wired. One that names an
// action the application ignores is worse than an action that was never offered,
// because the config looks right and the desk does nothing.
func TestEveryTActionOwnedActionIsLive(t *testing.T) {
	librarySetup := func(m *Model) { m.st.Mode = ModeLibrary }

	for _, tc := range []struct {
		kind  engine.ActionKind
		setup func(*Model)
		live  func(*Model) bool
	}{
		{engine.ViewNext, nil, func(m *Model) bool { return m.st.Layout != LayoutD }},
		{engine.ViewPrev, nil, func(m *Model) bool { return m.st.Layout != LayoutD }},
		{engine.FocusNext, nil, func(m *Model) bool { return m.st.Focus != 0 }},
		{engine.ZoomToggle, nil, func(m *Model) bool { return m.st.Zoom[0] }},
		{engine.ZoomIn, nil, func(m *Model) bool { return m.st.ZoomBars != 8 }},
		{engine.ZoomOut, nil, func(m *Model) bool { return m.st.ZoomBars != 8 }},
		{engine.DebugToggle, nil, func(m *Model) bool { return m.st.Mode == ModeDebug }},
		{engine.HelpToggle, nil, func(m *Model) bool { return m.st.Mode == ModeHelp }},
		{engine.LibraryOpen, nil, func(m *Model) bool { return m.st.Mode == ModeLibrary }},
		{engine.LibraryClose, librarySetup, func(m *Model) bool { return m.st.Mode == ModeMix }},
		{engine.LibraryLoad, librarySetup, func(m *Model) bool { return m.st.Mode == ModeMix }},
		{engine.LibrarySearch, nil, func(m *Model) bool { return m.st.Mode == ModeLibrary }},
		{engine.LibrarySort, librarySetup, func(m *Model) bool { return m.st.ShowSort }},
		{engine.LibraryFocusNext, librarySetup, func(m *Model) bool {
			return m.st.LibPane != views.PaneCrates
		}},
	} {
		m, _ := libraryModel(t)
		m.st.Mode = ModeMix
		m.st.Layout = LayoutD
		m.st.ZoomBars = 8
		m.st.LibPane = views.PaneCrates
		if tc.setup != nil {
			tc.setup(m)
		}

		m.applyAction(engine.Action{Kind: tc.kind})

		if !tc.live(m) {
			t.Errorf("%v changed nothing: a mapping file can name it and nothing happens", tc.kind)
		}
	}
}
