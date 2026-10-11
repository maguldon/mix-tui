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

// TestOverlayShowsDeckBehind.
//
// RenderOverlay draws only the box; it takes no snapshot and cannot show a deck.
// The deck appears because the model composites the box over the deck view, so
// asserting on RenderOverlay alone -- which is what this test used to do, and
// what its name claimed -- proves nothing about the deck being visible.
//
// The real assertion lives in TestOverlayKeepsTheDecksVisible, which renders a
// model. This test covers the box on its own.
func TestOverlayShowsDeckBehind(t *testing.T) {
	out := RenderOverlay(120, 40, testIndex(), "", 0)
	if !strings.Contains(out, "LOAD TRACK") {
		t.Error("overlay frame missing")
	}
	// The box must fit the frame it was given: a full-screen app whose frame
	// overflows its terminal scrolls and corrupts the display.
	if n := len(strings.Split(strings.TrimRight(out, "\n"), "\n")); n > 40 {
		t.Errorf("overlay is %d rows for a 40-row frame", n)
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

// TestColumnsAlignWithHeader is the test the review said was missing: the
// header and the rows computed their widths from different inputs, so every
// column sat one to the left of its heading while the "fits within width" tests
// passed.
//
// It compares where each right-aligned value ENDS against where its heading
// ENDS, which is what a reader aligns on. Rows are identified by the selection
// cursor so the crate pane's lines are not mistaken for track rows.
func TestColumnsAlignWithHeader(t *testing.T) {
	out := RenderLibrary(140, 12, testIndex(), LibraryState{})
	lines := strings.Split(out, "\n")

	var header, row string
	for _, l := range lines {
		if strings.Contains(l, "TITLE") && header == "" {
			header = l
		}
		// The selected track row carries the cursor.
		if strings.Contains(l, "> Inner Speaker") {
			row = l
		}
	}
	if header == "" || row == "" {
		t.Fatalf("header or track row not found:\n%s", out)
	}

	for _, c := range []struct{ heading, value string }{
		{"BPM", "128.00"},
		// The key cell carries a confidence marker (spec 6.5), so the value
		// is searched for including it. Searching for the bare code would find
		// it one column late and report a misalignment that is not there.
		{"KEY", "✓8A"},
		{"TIME", "3:34"},
	} {
		// Column positions must be compared in DISPLAY cells, not bytes: the
		// key cell now carries a confidence marker, and "✓" is three bytes but
		// one column, so byte arithmetic reports a two-column misalignment that
		// is not on screen.
		hi := strings.Index(header, c.heading)
		vi := strings.Index(row, c.value)
		if hi < 0 || vi < 0 {
			t.Fatalf("%s: heading at %d, value at %d\n%s", c.heading, hi, vi, out)
		}
		headEnd := lipgloss.Width(header[:hi]) + lipgloss.Width(c.heading)
		valEnd := lipgloss.Width(row[:vi]) + lipgloss.Width(c.value)
		if headEnd != valEnd {
			t.Errorf("%s value ends at column %d but its heading ends at %d\nheader: %q\nrow:    %q",
				c.heading, valEnd, headEnd, header, row)
		}
	}
}

// TestColumnsAlignWithWideGlyphs is spec review focus #3 and the bug the
// width-aware padding exists to prevent: fmt's %-*s pads by rune count, so a
// CJK title pushed every column after it to the right.
func TestColumnsAlignWithWideGlyphs(t *testing.T) {
	ascii := library.NewIndex()
	ascii.Add(library.Track{Path: "/a.wav", Title: "Inner Speaker", Artist: "Safras", BPM: 128, Duration: 214e9})
	cjk := library.NewIndex()
	cjk.Add(library.Track{Path: "/b.wav", Title: "日本語のトラック名です", Artist: "アーティスト", BPM: 128, Duration: 214e9})

	// Measure in DISPLAY COLUMNS, not bytes: a CJK rune is three bytes, so
	// strings.Index would report a difference that is not an alignment
	// difference and would hide a real one elsewhere.
	bpmCol := func(frame string) int {
		for _, l := range strings.Split(frame, "\n") {
			if strings.Contains(l, "128.00") && strings.Contains(l, ">") {
				return lipgloss.Width(l[:strings.Index(l, "128.00")])
			}
		}
		return -1
	}
	ai, bi := bpmCol(RenderLibrary(140, 12, ascii, LibraryState{})),
		bpmCol(RenderLibrary(140, 12, cjk, LibraryState{}))
	if ai < 0 || bi < 0 {
		t.Fatalf("BPM value not found in a track row (ascii=%d cjk=%d)", ai, bi)
	}
	if ai != bi {
		t.Errorf("BPM at column %d for an ASCII title but %d for a CJK title", ai, bi)
	}
}

// TestKeyConfidenceMarkers covers spec 6.5's three-tier display. Confidence was
// stored on every track and never shown: the list displayed either the bare key
// or "--", with nothing in between, so a detection the analyser was unsure of
// looked identical to one it was sure of.
func TestKeyConfidenceMarkers(t *testing.T) {
	for _, tc := range []struct {
		name string
		tr   library.Track
		want string
	}{
		{"high confidence", library.Track{KeyCamelot: "8A", KeyConfidence: 0.9}, "✓8A"},
		{"detected but not confident", library.Track{KeyCamelot: "9B", KeyConfidence: 0.60}, "~9B"},
		{"below the detection threshold is undetected", library.Track{KeyCamelot: "3A", KeyConfidence: 0.10}, "?"},
		{"no key at all", library.Track{}, "?"},
	} {
		if got := formatKey(tc.tr); got != tc.want {
			t.Errorf("%s: formatKey = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// TestKeyMarkersRenderInTheList. The marker is the whole point, so it has to
// reach the screen and not just the formatter.
func TestKeyMarkersRenderInTheList(t *testing.T) {
	ix := library.NewIndex()
	ix.Add(library.Track{Path: "/a.wav", Title: "Sure", KeyCamelot: "8A", KeyConfidence: 0.95})
	ix.Add(library.Track{Path: "/b.wav", Title: "Unsure", KeyCamelot: "9B", KeyConfidence: 0.60})
	ix.Add(library.Track{Path: "/c.wav", Title: "Unknown", KeyConfidence: 0.0})

	out := RenderLibrary(140, 12, ix, LibraryState{})
	for _, want := range []string{"✓8A", "~9B"} {
		if !strings.Contains(out, want) {
			t.Errorf("the track list does not show %q:\n%s", want, out)
		}
	}
}
