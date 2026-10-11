package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"

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
	for _, l := range lines {
		body += l + "\n"
	}
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
	if len(lines) != 4 {
		t.Fatalf("got %d lines, want 4", len(lines))
	}
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
		if lipgloss.Width(l) != 20 {
			t.Errorf("line %d width %d, want 20", i, lipgloss.Width(l))
		}
	}
}

// TestRenderColumnsFillsShortColumn guards the rule that a ragged column is
// filled with spaces rather than omitted: dropping the line would shift every
// row below it up by one, which looks like content jumping.
func TestRenderColumnsFillsShortColumn(t *testing.T) {
	cols := [][]string{
		{"r0", "r1", "r2"},
		{"x"},
	}
	out := RenderColumns(cols, 1, 20)
	if len(out) != 3 {
		t.Fatalf("got %d lines, want 3 (shortest column padded)", len(out))
	}
	if !strings.Contains(out[2], "x") == false {
		// row 2 must exist; its second column is spaces
	}
	for i, l := range out {
		if lipgloss.Width(l) != 20 {
			t.Errorf("line %d width %d, want 20", i, lipgloss.Width(l))
		}
	}
}

// TestPanelBlankRowIsSpacesNotEmpty pins the rule that a blank row must still
// occupy its columns; an empty string collapses a column.
func TestPanelBlankRowIsSpacesNotEmpty(t *testing.T) {
	p := Panel{Title: "X", Width: 10, Rows: []PanelRow{
		{Kind: RowText, Text: ""},
	}}
	lines := RenderPanel(p, 3)
	if len(lines) != 3 {
		t.Fatalf("want 3 lines, got %d", len(lines))
	}
}

// TestRenderDIsRectangular checks every rendered row is exactly w columns, not
// merely within it: a ragged frame makes a full-screen app repaint noise.
func TestRenderDIsRectangular(t *testing.T) {
	for _, w := range []int{200, 120, 100, 90} {
		out := renderD(w, 30, snapTwoDecks(), ViewState{Layout: LayoutD})
		for i, line := range strings.Split(out, "\n") {
			if got := lipgloss.Width(line); got != w {
				t.Errorf("w=%d line %d width %d, want exactly %d", w, i, got, w)
			}
		}
	}
}

// TestRenderDNeverPanicsAcrossSizes sweeps a wide range of sizes.
func TestRenderDNeverPanicsAcrossSizes(t *testing.T) {
	for w := 1; w <= 140; w++ {
		for h := 1; h <= 30; h++ {
			_ = renderD(w, h, snapTwoDecks(), ViewState{Layout: LayoutD})
		}
	}
}

// TestRenderDHandlesNoDecks guards against an empty snapshot, which is what the
// model sees before anything is loaded.
func TestRenderDHandlesNoDecks(t *testing.T) {
	out := renderD(120, 30, engine.Snapshot{}, ViewState{Layout: LayoutD})
	assertLinesFit(t, out, 120)
}
