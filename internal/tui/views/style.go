package views

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

var (
	titleStyle  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#58a6ff"))
	labelStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("#c9d1d9"))
	dimStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("#3d444d"))
	accentStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#7ee787"))
	warnStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("#ffa657"))
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
