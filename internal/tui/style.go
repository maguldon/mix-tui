package tui

import (
	"github.com/charmbracelet/lipgloss"

	"github.com/maguldon/mix-tui/internal/tui/palette"
)

// The palette lives in internal/tui/palette so that internal/tui/views can
// share it too. It was declared here and there separately, and the copies had
// already drifted.
var (
	played    = palette.Played
	unplayed  = palette.Unplayed
	beat      = palette.Beat
	cue       = palette.Cue
	loopColor = palette.Loop
	playhead  = palette.Playhead
	ok        = palette.Ok
	dim       = palette.Dim
	warn      = palette.Warn
	border    = palette.Border
	text      = palette.Text
	accent    = palette.Accent
)

// Shared styles, aliased from the single palette definition.
var (
	titleStyle = palette.TitleStyle
	labelStyle = palette.LabelStyle
	dimStyle   = palette.DimStyle
	warnStyle  = palette.WarnStyle
	errStyle   = palette.ErrStyle
	okStyle    = palette.OkStyle
	borderBox  = palette.BorderBox
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
