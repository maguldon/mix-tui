package tui

import (
	"strings"
	"testing"

	"github.com/maguldon/mix-tui/internal/engine"
	"github.com/maguldon/mix-tui/internal/tui/library"
	"github.com/maguldon/mix-tui/internal/tui/views"
)

// libraryModel returns a model with a small library and the browser open.
func libraryModel(t *testing.T) (*Model, *fakeEngine) {
	t.Helper()
	m, f := testModel(t)
	ix := library.NewIndex()
	ix.Add(library.Track{Path: "/1.wav", Title: "One", BPM: 128, KeyCamelot: "8A", KeyConfidence: 0.9})
	ix.Add(library.Track{Path: "/2.wav", Title: "Two", BPM: 140, KeyCamelot: "9A", KeyConfidence: 0.9})
	ix.Add(library.Track{Path: "/3.wav", Title: "Three", BPM: 90, KeyCamelot: "10A", KeyConfidence: 0.9})
	m.SetLibrary(ix)
	m.st.Mode = ModeLibrary
	m.st.LibPane = views.PaneTracks
	// The model cannot decode audio itself; the binary supplies the loader.
	// Recording the deck is what makes "which deck did this load into?" a
	// question with an answer.
	m.SetLoader(func(deck int, path string) error {
		f.applied = append(f.applied, engine.Action{Kind: engine.DeckPlay, Target: deck})
		return nil
	})
	return m, f
}

// TestTabCyclesLibraryPanesNotDecks is the regression test for spec 6.5.
//
// TAB fell through to the global handler, which moved DECK focus. Browsing a
// library and pressing tab silently changed which deck ENTER would load into --
// the exact moment when a mistake is most expensive.
func TestTabCyclesLibraryPanesNotDecks(t *testing.T) {
	m, _ := libraryModel(t)
	deckBefore := m.st.Focus
	paneBefore := m.st.LibPane

	m.handleKey("tab")

	if m.st.Focus != deckBefore {
		t.Errorf("tab changed deck focus from %d to %d while browsing", deckBefore, m.st.Focus)
	}
	if m.st.LibPane == paneBefore {
		t.Errorf("tab did not move the pane focus (stayed at %v)", paneBefore)
	}

	// It must cycle, not latch.
	seen := map[views.Pane]bool{paneBefore: true}
	for i := 0; i < int(views.PaneCount); i++ {
		m.handleKey("tab")
		seen[m.st.LibPane] = true
	}
	if len(seen) != int(views.PaneCount) {
		t.Errorf("tab reached %d panes, want %d", len(seen), views.PaneCount)
	}
	if m.st.Focus != deckBefore {
		t.Errorf("cycling panes changed deck focus to %d", m.st.Focus)
	}
}

// TestTabStillSwitchesDecksOutsideTheBrowser. The browser change must not have
// stolen tab from the deck view, which is where deck focus actually belongs.
func TestTabStillSwitchesDecksOutsideTheBrowser(t *testing.T) {
	m, _ := testModel(t)
	m.st.Mode = ModeMix
	before := m.st.Focus

	m.handleKey("tab")

	if m.st.Focus == before {
		t.Error("tab no longer switches decks in the mix view")
	}
}

// TestTabThenEnterLoadsToTheOtherDeck replaces the SHIFT+ENTER test.
//
// SHIFT+ENTER was spec 6.5's way to load to the other deck, and it was
// unreachable: bubbletea has no Shift field, so on an ordinary terminal it
// reported "enter" and the case never fired. The test passed because it called
// handleKey("shift+enter") directly, bypassing the transport.
//
// The reachable path is to move deck focus and press ENTER. This drives it the
// way the application receives keys, so it cannot pass on a binding no terminal
// sends.
func TestTabThenEnterLoadsToTheOtherDeck(t *testing.T) {
	m, f := libraryModel(t)
	m.st.Focus = 0
	m.st.Mode = ModeMix
	m.st.TrackSel = 0

	press(t, m, "tab")
	if m.st.Focus != 1 {
		t.Fatalf("tab left deck focus at %d, want 1", m.st.Focus)
	}

	m.st.Mode = ModeLibrary
	press(t, m, "enter")

	if len(f.applied) != 1 {
		t.Fatalf("enter produced %d loads, want 1", len(f.applied))
	}
	if got := f.applied[0].Target; got != 1 {
		t.Errorf("loaded into deck %d, want deck 1 (focus was 1)", got)
	}
	if m.st.Focus != 1 {
		t.Errorf("focus = %d; it should follow the load", m.st.Focus)
	}
	if m.st.Mode != ModeMix {
		t.Errorf("enter left the browser open (mode %v)", m.st.Mode)
	}
}

// TestShiftTabCyclesLibraryPanesBackwards covers the other half of the pair.
// SHIFT+TAB was not matched in the browser at all, so it reached the global
// handler and moved DECK focus while the user was browsing -- the exact mistake
// the TAB case exists to prevent, and the pane test only ever exercised TAB.
func TestShiftTabCyclesLibraryPanesBackwards(t *testing.T) {
	m, _ := libraryModel(t)
	deckBefore := m.st.Focus
	paneBefore := m.st.LibPane

	press(t, m, "shift+tab")

	if m.st.Focus != deckBefore {
		t.Errorf("shift+tab changed deck focus from %d to %d while browsing", deckBefore, m.st.Focus)
	}
	if m.st.LibPane == paneBefore {
		t.Errorf("shift+tab did not move the pane focus (stayed at %v)", paneBefore)
	}
}

// TestShiftTabMovesDeckFocusBackwards keeps reverse deck focus reachable in the
// mix view. It used to match "backtab", which no terminal emits, so the key did
// nothing at all.
func TestShiftTabMovesDeckFocusBackwards(t *testing.T) {
	m, _ := testModel(t)
	m.st.Focus = 0

	press(t, m, "shift+tab")

	if m.st.Focus != len(m.eng.Snapshot().Decks)-1 {
		t.Errorf("shift+tab left focus at %d, want it to wrap to the last deck", m.st.Focus)
	}
}

// TestEnterStillLoadsToTheFocusedDeck guards the other half of the pair.
func TestEnterStillLoadsToTheFocusedDeck(t *testing.T) {
	m, f := libraryModel(t)
	m.st.Focus = 1
	m.st.TrackSel = 0

	m.handleKey("enter")

	if len(f.applied) != 1 || f.applied[0].Target != 1 {
		t.Errorf("enter loaded into %v, want deck 1 (the focused deck)", f.applied)
	}
}

// TestSortMenuChangesTheSort. The sort menu rendered and could be opened, but
// nothing ever set SortBy, so the list was permanently title-ascending and the
// menu had no way to change anything.
func TestSortMenuChangesTheSort(t *testing.T) {
	m, _ := libraryModel(t)

	m.handleKey("s")
	if !m.st.ShowSort {
		t.Fatal("s did not open the sort menu")
	}
	// The menu must actually render while open, showing the current choice.
	if open := m.RenderAt(140, 20); !strings.Contains(open, "SORT BY") {
		t.Errorf("the open sort menu did not render:\n%s", open)
	}

	// Select "BPM", third entry.
	m.handleKey("down")
	m.handleKey("down")
	m.handleKey("enter")

	if m.st.ShowSort {
		t.Error("enter did not close the sort menu")
	}
	if m.st.SortBy != library.SortBPM {
		t.Errorf("sort is %v, want SortBPM", m.st.SortBy)
	}
}

// TestSortSelectionAppliesToTheTrackOrder is the assertion that matters: the
// chosen sort has to change what the list shows, not just a state field.
func TestSortSelectionAppliesToTheTrackOrder(t *testing.T) {
	m, _ := libraryModel(t)

	render := func() string {
		out := m.RenderAt(160, 24)
		return out
	}

	// Default: title ascending -> One, Three, Two.
	def := render()
	iOne, iTwo := strings.Index(def, "One"), strings.Index(def, "Two")
	if iOne < 0 || iTwo < 0 {
		t.Fatalf("tracks missing from the default order:\n%s", def)
	}

	// Switch to BPM ascending -> One(128), Two(140), Three(90) -> Three, One, Two.
	m.handleKey("s")
	m.handleKey("down")
	m.handleKey("down")
	m.handleKey("enter")

	bpm := render()
	oBPM := strings.Index(bpm, "One")
	tBPM := strings.Index(bpm, "Two")
	hBPM := strings.Index(bpm, "Three")
	if oBPM < 0 || tBPM < 0 || hBPM < 0 {
		t.Fatalf("tracks missing from the BPM order:\n%s", bpm)
	}
	if !(hBPM < oBPM && oBPM < tBPM) {
		t.Errorf("BPM ascending should be Three(90), One(128), Two(140); got %d,%d,%d", hBPM, oBPM, tBPM)
	}
}

// TestSortDirectionToggles covers the descending toggle.
func TestSortDirectionToggles(t *testing.T) {
	m, _ := libraryModel(t)
	if m.st.SortDesc {
		t.Fatal("sort should start ascending")
	}
	m.handleKey("s")
	m.handleKey("d")
	if !m.st.SortDesc {
		t.Error("d did not flip the sort direction")
	}
}

// TestSortResetsWhenLeavingTheBrowser covers spec 6.5's "sort state resets when
// the view is left". It did not: re-entering the library opened with the sort
// menu still covering the detail pane.
func TestSortResetsWhenLeavingTheBrowser(t *testing.T) {
	m, _ := libraryModel(t)

	m.handleKey("s")
	m.handleKey("down")
	m.handleKey("enter")
	if m.st.SortBy == library.SortTitle {
		t.Fatal("setup did not change the sort")
	}

	m.handleKey("escape")
	if m.st.Mode != ModeMix {
		t.Fatalf("escape did not leave the browser (mode %v)", m.st.Mode)
	}
	if m.st.ShowSort {
		t.Error("the sort menu survived leaving the browser")
	}
	if m.st.SortBy != library.SortTitle {
		t.Errorf("sort survived leaving the browser: %v", m.st.SortBy)
	}
	if m.st.SortDesc {
		t.Error("sort direction survived leaving the browser")
	}
}

// TestSortMenuKeysDoNotLeakToTheTrackList. With the menu open the arrows belong
// to the menu; letting them through would move both cursors at once.
func TestSortMenuKeysDoNotLeakToTheTrackList(t *testing.T) {
	m, _ := libraryModel(t)
	m.st.TrackSel = 1

	m.handleKey("s")
	m.handleKey("down")

	if m.st.TrackSel != 1 {
		t.Errorf("the track selection moved to %d while the sort menu was open", m.st.TrackSel)
	}
}

// TestSortStateResetsOnLoad is the regression test for the audit finding behind
// spec 6.5's "sort state resets when the view is left".
//
// It reset on escape and not on ENTER, so a load left the sort applied and the
// sort menu possibly covering the detail pane -- and the next "/" reopened
// looking as though someone had left it that way.
func TestSortStateResetsOnLoad(t *testing.T) {
	m, _ := libraryModel(t)
	m.st.SortBy = library.SortBPM
	m.st.SortDesc = true
	m.st.Sorted = true
	m.st.SortSel = 2
	m.st.CrateSel = 3

	// The sort menu is left closed: with it open, ENTER means "apply this
	// sort" and never reaches the load path at all.
	m.handleKey("enter")

	if m.st.SortBy != library.SortTitle || m.st.SortDesc || m.st.ShowSort ||
		m.st.SortSel != 0 || m.st.Sorted || m.st.CrateSel != 0 {
		t.Errorf("a load left the browser's sort state behind: %+v", m.st)
	}
	if m.st.Mode != ModeMix {
		t.Errorf("mode = %v, want the mix view", m.st.Mode)
	}
}

// TestEscapeStillResetsSort keeps the path that already worked working.
func TestEscapeStillResetsSort(t *testing.T) {
	m, _ := libraryModel(t)
	m.st.SortBy = library.SortBPM
	m.st.SortDesc = true
	m.st.Sorted = true
	m.st.ShowSort = true

	m.handleKey("esc")

	if m.st.SortBy != library.SortTitle || m.st.SortDesc || m.st.ShowSort || m.st.Sorted {
		t.Errorf("escape left the sort state behind: %+v", m.st)
	}
}

// TestHomeAndEndWorkFromTheTransport is the check #14 said could not be written
// before: the transport table had no constructors for home/end, so pressing them
// in a test reported "no tea.KeyMsg reports home" -- a misleading failure that
// points at bubbletea rather than at the missing binding. Both are now carried
// by transportKey, so this drives the real key path.
func TestHomeAndEndWorkFromTheTransport(t *testing.T) {
	m, _ := testModel(t)
	m.st.Mode = ModeSettings
	m.st.SettingInput = "/srv/music/long/path"
	m.st.SettingCursor = 3

	press(t, m, "end")
	if m.st.SettingCursor != len([]rune(m.st.SettingInput)) {
		t.Errorf("end left the caret at %d, want %d", m.st.SettingCursor, len([]rune(m.st.SettingInput)))
	}

	press(t, m, "home")
	if m.st.SettingCursor != 0 {
		t.Errorf("home left the caret at %d, want 0", m.st.SettingCursor)
	}

	// And in the browser's filter, which is the other text field.
	m2, _ := libraryModel(t)
	m2.st.Query = "abc"
	m2.st.QueryCursor = 1
	m2.handleKey("end")
	if m2.st.QueryCursor != 3 {
		t.Errorf("end in the filter left the caret at %d, want 3", m2.st.QueryCursor)
	}
	m2.handleKey("home")
	if m2.st.QueryCursor != 0 {
		t.Errorf("home in the filter left the caret at %d, want 0", m2.st.QueryCursor)
	}
}

// TestTheCrateKeysAreBound is the regression test for the crate store's second
// dead layer.
//
// The store, the loading and the rendering were all real and tested; the three
// functions that edit it -- newCrate, addSelectedToCrate,
// removeSelectedFromCrate -- had no callers, so the running application never
// wrote crates.yaml and ctrl+n/ctrl+a/ctrl+d, which the README documented, did
// nothing. A test that drove the functions directly would have passed throughout.
//
// newCrate had a second bug underneath: it computed an unused crate name and
// persisted the list unchanged, so even once bound it reported "created Crate 1"
// while writing back the file it had just read.
func TestTheCrateKeysAreBound(t *testing.T) {
	var saved []library.Crate
	m, _ := libraryModel(t)
	m.SetCratePersister(func(cs []library.Crate) error {
		saved = append([]library.Crate(nil), cs...)
		return nil
	})
	m.st.LibPane = views.PaneTracks

	press(t, m, "ctrl+n")
	// crateCount includes the "All tracks" row; count the crates themselves.
	if n := len(m.lib.Crates()); n != 1 {
		t.Fatalf("ctrl+n left %d crates, want 1", n)
	}
	if len(saved) != 1 || saved[0].Name == "" {
		t.Fatalf("ctrl+n persisted %+v, want one named crate", saved)
	}

	press(t, m, "ctrl+a")
	if n := len(m.lib.Crates()[0].Tracks); n != 1 {
		t.Errorf("ctrl+a left the crate with %d tracks, want 1", n)
	}

	press(t, m, "ctrl+d")
	if n := len(m.lib.Crates()[0].Tracks); n != 0 {
		t.Errorf("ctrl+d left the crate with %d tracks, want 0", n)
	}
}

// TestCtrlDTypesNothingIntoTheFilter: the browser types any printable key into
// the filter, so a crate binding on a letter would be unreachable. These are
// control keys for exactly that reason.
func TestCtrlDTypesNothingIntoTheFilter(t *testing.T) {
	m, _ := libraryModel(t)
	press(t, m, "ctrl+n")
	press(t, m, "ctrl+a")
	press(t, m, "ctrl+d")

	if m.st.Query != "" {
		t.Errorf("the crate keys typed %q into the filter", m.st.Query)
	}
}
