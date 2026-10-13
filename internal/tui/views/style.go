package views

import (
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/maguldon/mix-tui/internal/tui/palette"
)

// The palette lives in internal/tui/style.go and is imported here.
//
// It used to be a second, parallel copy: the same hex values under different
// names, which is exactly the drift the original comment warned about --
// "recolouring the whole interface is a single edit and the colours cannot drift
// between panels". They had drifted across the package boundary, worst of all
// on the word "accent", which meant #58a6ff here and #7ee787 in the deck view.
var (
	titleStyle  = palette.TitleStyle
	labelStyle  = palette.LabelStyle
	dimStyle    = palette.DimStyle
	accentStyle = palette.OkStyle
	warnStyle   = palette.WarnStyle
	errStyle    = palette.ErrStyle
	okStyle     = palette.OkStyle
	borderBox   = palette.BorderBox
)

func spaces(n int) string {
	if n <= 0 {
		return ""
	}
	return strings.Repeat(" ", n)
}

// truncate cuts to w display columns without splitting a wide glyph.
func truncate(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= w {
		return s
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

// pad fills s with spaces to exactly w, honouring ANSI styling: it measures the
// visible width, not the byte length, so a coloured cell still lines up.
//
// Every pane line goes through this. A pane that returns a short line does not
// make the column collapse so much as it leaves the frame ragged, and a
// full-screen app repainting a ragged frame produces visible noise.
func pad(s string, w int) string {
	s = truncate(s, w)
	if gap := w - lipgloss.Width(s); gap > 0 {
		return s + spaces(gap)
	}
	return s
}

// lipglossWidth measures visible columns, ignoring ANSI escapes.
func lipglossWidth(s string) int { return lipgloss.Width(s) }

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// padRight pads s with spaces to exactly w, truncating first if too wide.
func padRight(s string, w int) string {
	s = truncate(s, w)
	if gap := w - lipgloss.Width(s); gap > 0 {
		return s + spaces(gap)
	}
	return s
}
