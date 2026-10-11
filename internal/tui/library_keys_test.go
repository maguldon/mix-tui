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

// TestShiftEnterLoadsToTheOtherDeck covers spec 6.5's SHIFT+ENTER, which was
// absent entirely: every load went to the focused deck.
func TestShiftEnterLoadsToTheOtherDeck(t *testing.T) {
	m, f := libraryModel(t)
	m.st.Focus = 0
	m.st.TrackSel = 0

	m.handleKey("shift+enter")

	if len(f.applied) != 1 {
		t.Fatalf("shift+enter produced %d loads, want 1", len(f.applied))
	}
	if got := f.applied[0].Target; got != 1 {
		t.Errorf("shift+enter loaded into deck %d, want deck 1 (focus was 0)", got)
	}
	if m.st.Focus != 1 {
		t.Errorf("focus stayed on deck %d; it should follow the load", m.st.Focus)
	}
	if m.st.Mode != ModeMix {
		t.Errorf("shift+enter left the browser open (mode %v)", m.st.Mode)
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
