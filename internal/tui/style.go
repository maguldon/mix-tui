package tui

import "github.com/charmbracelet/lipgloss"

// The palette is chosen once, here. Every view refers to these names rather
// than embedding a hex literal, so recolouring the whole interface is a single
// edit and the colours cannot drift between panels.
var (
	played   = lipgloss.Color("#f97583") // waveform before the playhead
	unplayed = lipgloss.Color("#316dca") // waveform after the playhead
	beat     = lipgloss.Color("#ffa657") // beatgrid downbeats
	cue      = lipgloss.Color("#d2a8ff") // hotcues
	ok       = lipgloss.Color("#7ee787") // active/armed
	dim      = lipgloss.Color("#3d444d") // box drawing
	warn     = lipgloss.Color("#ffa657") // low-confidence key
	border   = lipgloss.Color("#30363d")
	text     = lipgloss.Color("#c9d1d9")
	accent   = lipgloss.Color("#58a6ff")
)

// Shared styles. lipgloss styles are values, so these are cheap to copy.
var (
	titleStyle = lipgloss.NewStyle().Bold(true).Foreground(accent)
	labelStyle = lipgloss.NewStyle().Foreground(text)
	dimStyle   = lipgloss.NewStyle().Foreground(dim)
	warnStyle  = lipgloss.NewStyle().Foreground(warn)
	okStyle    = lipgloss.NewStyle().Foreground(ok)
	borderBox  = lipgloss.NewStyle().Foreground(border)
)

// truncate cuts s to at most w display columns.
//
// It measures with lipgloss.Width, not len, because a track name containing CJK
// or emoji occupies more columns than it has bytes (review focus #3). Counting
// bytes would cut mid-rune and misalign every column after it.
func truncate(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= w {
		return s
	}
	// Walk runes accumulating display width, stopping before the limit. This
	// never splits a multi-column glyph, so alignment is preserved.
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

// padRight pads s with spaces to exactly w display columns, truncating first if
// it is already too wide. Padding by display width (not byte length) keeps
// columns aligned when the content contains wide glyphs.
func padRight(s string, w int) string {
	if w <= 0 {
		return ""
	}
	s = truncate(s, w)
	if gap := w - lipgloss.Width(s); gap > 0 {
		return s + spaces(gap)
	}
	return s
}

func spaces(n int) string {
	if n <= 0 {
		return ""
	}
	b := make([]byte, n)
	for i := range b {
		b[i] = ' '
	}
	return string(b)
}
