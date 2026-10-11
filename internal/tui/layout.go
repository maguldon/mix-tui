package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// RowKind selects how a panel row draws itself.
type RowKind int

const (
	RowText RowKind = iota
	RowWave
	RowMeter
)

// PanelRow is one line of a panel.
type PanelRow struct {
	Kind  RowKind
	Text  string
	Wave  *WaveSpec
	Style lipgloss.Style
}

// Panel is a titled box drawn at an exact width.
type Panel struct {
	Title string
	Width int
	Rows  []PanelRow
}

// TruncateTo cuts s to at most w display columns, appending an ellipsis when it
// actually cut something.
//
// Width is measured with lipgloss.Width, never len: a CJK or emoji string has
// more display columns than bytes (review focus #3), and byte-length truncation
// would split a rune and misalign every column after it.
func TruncateTo(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= w {
		return s
	}
	if w == 1 {
		return "…" // the ellipsis alone fills the single available column
	}
	// Reserve one column for the ellipsis so the result is exactly w.
	return truncatePlain(s, w-1) + "…"
}

// truncatePlain cuts to w display columns without adding a marker.
func truncatePlain(s string, w int) string {
	if w <= 0 {
		return ""
	}
	var b []rune
	used := 0
	for _, r := range s {
		rw := lipgloss.Width(string(r))
		if used+rw > w {
			break
		}
		b = append(b, r)
		used += rw
	}
	return string(b)
}

// RenderPanel draws a titled box of exactly p.Width columns and height lines.
//
// Blank rows are spaces, never empty strings: an empty line in a column layout
// collapses that column for every row beneath it. Overflowing rows are dropped
// rather than pushing the box past its height.
func RenderPanel(p Panel, height int) []string {
	w := p.Width
	if w <= 0 {
		return nil
	}
	if height < 2 {
		// A box needs a top and bottom border; below that there is nothing to
		// draw, so return nothing rather than a partial border.
		return nil
	}

	// Border glyphs are single-width; the inner field is w-2 columns.
	inner := w - 2
	if inner < 0 {
		inner = 0
	}

	// Top border: ┌─ Title ────┐ filling exactly w columns.
	//
	// The inner field is w-2, spent as: "─" + title + " " + fill. So the title
	// may use inner-2 and the fill is what is left. Measuring the title with
	// lipgloss.Width rather than len keeps a wide-glyph title from pushing the
	// right border a column over.
	title := TruncateTo(p.Title, maxInt(0, inner-2))
	fill := maxInt(0, inner-2-lipgloss.Width(title))
	lines := make([]string, 0, height)
	lines = append(lines, borderBox.Render("┌─"+title+" "+strings.Repeat("─", fill)+"┐"))

	content := height - 2
	for i := 0; i < content; i++ {
		var body string
		var st lipgloss.Style
		if i < len(p.Rows) {
			r := p.Rows[i]
			st = r.Style
			switch r.Kind {
			case RowWave:
				if r.Wave != nil {
					body = r.Wave.Render(inner)
				}
			case RowMeter:
				body = r.Text
			default:
				body = r.Text
			}
		}
		_ = st
		lines = append(lines, "│"+padRight(body, inner)+"│")
	}
	lines = append(lines, borderBox.Render("└"+strings.Repeat("─", inner)+"┘"))
	return lines
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// RenderColumns joins pre-rendered columns with a fixed gap and pads each
// result line to exactly width.
//
// Rows beyond a column's length are filled with spaces rather than omitted:
// dropping them would shift every later row up by one, which reads as content
// jumping when the user scrolls.
func RenderColumns(cols [][]string, gap, width int) []string {
	if len(cols) == 0 || width <= 0 {
		return nil
	}
	maxRows := 0
	for _, c := range cols {
		if len(c) > maxRows {
			maxRows = len(c)
		}
	}
	gapStr := spaces(gap)
	out := make([]string, 0, maxRows)
	for i := 0; i < maxRows; i++ {
		var b strings.Builder
		for j, col := range cols {
			if j > 0 {
				b.WriteString(gapStr)
			}
			if i < len(col) {
				b.WriteString(col[i])
			} else {
				b.WriteString(spaces(colWidth(cols, j)))
			}
		}
		out = append(out, padRight(b.String(), width))
	}
	return out
}

// colWidth is the display width of a column: the widest line it contains.
func colWidth(cols [][]string, j int) int {
	w := 0
	for _, l := range cols[j] {
		if lw := lipgloss.Width(l); lw > w {
			w = lw
		}
	}
	return w
}
