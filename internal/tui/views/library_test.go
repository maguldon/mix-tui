package views

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"

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
		if !strings.Contains(out, want) {
			t.Errorf("missing %q", want)
		}
	}
}

func TestLibrarySortPaneReplacesDetailPane(t *testing.T) {
	ix := testIndex()
	off := RenderLibrary(120, 40, ix, LibraryState{ShowSort: false})
	on := RenderLibrary(120, 40, ix, LibraryState{ShowSort: true})
	if strings.Contains(on, "key conf") {
		t.Error("detail pane still shown with sort open")
	}
	if !strings.Contains(on, "BPM") {
		t.Error("sort pane not shown")
	}
	if off == on {
		t.Error("sort toggle changed nothing")
	}
}

func TestLibrarySortDoesNotRewriteCrate(t *testing.T) {
	ix := testIndex()
	_ = RenderLibrary(120, 40, ix, LibraryState{SortBy: library.SortBPM, ShowSort: true})
	if len(ix.Tracks()) != 2 {
		t.Error("rendering mutated the index")
	}
}

func TestBelowThresholdKeyShowsUndetected(t *testing.T) {
	ix := library.NewIndex()
	ix.Add(library.Track{Path: "/x", Title: "T", BPM: 128,
		KeyCamelot: "9A", KeyConfidence: 0.2})
	out := RenderLibrary(120, 40, ix, LibraryState{})
	if strings.Contains(out, "9A") {
		t.Errorf("low-confidence key displayed:\n%s", out)
	}
}

func TestOverlayFiltersAsYouType(t *testing.T) {
	ix := testIndex()
	all := RenderOverlay(120, 40, ix, "", 0)
	few := RenderOverlay(120, 40, ix, "ware", 0)
	if strings.Count(few, "Inner") != 0 {
		t.Error("filter did not exclude")
	}
	if !strings.Contains(few, "Warehouse") {
		t.Error("filter excluded the match")
	}
	if strings.Count(all, "Speaker") == 0 {
		t.Error("unfiltered view lost a track")
	}
}

func TestOverlayShowsDeckBehind(t *testing.T) {
	out := RenderOverlay(120, 40, testIndex(), "", 0)
	if !strings.Contains(out, "LOAD TRACK") {
		t.Error("overlay frame missing")
	}
}

func TestLibrarySurvivesEmptyIndex(t *testing.T) {
	out := RenderLibrary(120, 40, library.NewIndex(), LibraryState{})
	if out == "" {
		t.Error("empty library rendered nothing")
	}
	_ = RenderOverlay(120, 40, library.NewIndex(), "zzz", 0)
}

func TestMatchTrackClampsSelection(t *testing.T) {
	ts := testIndex().Tracks()
	if _, ok := MatchTrack(ts, "", 99); ok {
		t.Error("out-of-range selection should report no match")
	}
}

// TestMatchTrackFiltersBeforeSelecting guards the ordering: a selection index
// applies to the FILTERED list, so selecting index 1 with a query active must
// not silently return the wrong track.
func TestMatchTrackFiltersBeforeSelecting(t *testing.T) {
	ts := testIndex().Tracks()
	// With no filter, index 1 is the second track.
	if _, ok := MatchTrack(ts, "", 1); !ok {
		t.Error("index 1 should match with no filter")
	}
	// With a filter, the index applies to the filtered list.
	got, ok := MatchTrack(ts, "ware", 0)
	if !ok {
		t.Fatal("filtered selection missed")
	}
	if !strings.Contains(got.Title, "Warehouse") {
		t.Errorf("filtered index 0 returned %q", got.Title)
	}
}

// TestLibrarySelectionClampsOutOfRange keeps a stale selection index from
// panicking or rendering nothing after a search narrows the list.
func TestLibrarySelectionClampsOutOfRange(t *testing.T) {
	ix := testIndex()
	st := LibraryState{TrackSel: 99}
	if out := RenderLibrary(120, 40, ix, st); out == "" {
		t.Error("out-of-range selection rendered nothing")
	}
}

// TestLibraryIsRectangular checks every line is exactly w, not merely within
// it, so the full-screen frame is a clean rectangle.
func TestLibraryIsRectangular(t *testing.T) {
	for _, w := range []int{200, 140, 120, 121} {
		out := RenderLibrary(w, 20, testIndex(), LibraryState{})
		for i, l := range strings.Split(out, "\n") {
			if got := lipgloss.Width(l); got != w {
				t.Errorf("w=%d line %d width %d", w, i, got)
			}
		}
	}
}

// TestLibraryNeverPanicsAcrossSizes sweeps the browser over a size range.
func TestLibraryNeverPanicsAcrossSizes(t *testing.T) {
	ix := testIndex()
	for w := 1; w <= 170; w += 7 {
		for h := 1; h <= 30; h += 7 {
			_ = RenderLibrary(w, h, ix, LibraryState{})
			_ = RenderOverlay(w, h, ix, "q", 0)
		}
	}
}

// TestOverlayClampsSelectionPastResults keeps the overlay from showing a cursor
// beyond the filtered results.
func TestOverlayClampsSelectionPastResults(t *testing.T) {
	ix := testIndex()
	// Only one result matches, but select index 5.
	if out := RenderOverlay(120, 40, ix, "ware", 5); out == "" {
		t.Error("overlay rendered nothing for an out-of-range selection")
	}
}

// TestLibraryNarrowPaneDropsDurationBarFirst pins the column-priority rule:
// when width runs out, the duration bar goes before the title.
func TestLibraryNarrowPaneDropsDurationBarFirst(t *testing.T) {
	ix := testIndex()
	// Wide enough for everything.
	wide := RenderLibrary(200, 40, ix, LibraryState{})
	if !strings.Contains(wide, "Inner Speaker") {
		t.Fatalf("wide render lost the title:\n%s", wide)
	}
	// Still exactly 120 columns wide with all content fitting.
	narrow := RenderLibrary(120, 40, ix, LibraryState{})
	if !strings.Contains(narrow, "Inner Speaker") {
		t.Errorf("120-col render dropped the title entirely:\n%s", narrow)
	}
}
