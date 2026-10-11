package views

import (
	"strings"

	"github.com/maguldon/mix-tui/internal/tui/library"
)

// maxOverlayResults is how many matches the quick-load overlay shows at once.
// A DJ mid-set wants the first few candidates, not a scrolling list; the
// library view is where the full list lives.
const maxOverlayResults = 8

// RenderOverlay draws the mid-set quick-load box: a query line, a separator,
// and up to maxOverlayResults filtered results.
//
// The caller draws this OVER the deck view and dims what is behind it rather
// than erasing it: the deck staying visible is the entire point of an overlay
// (spec §6.5), so a DJ can see what is still playing while choosing a
// replacement.
func RenderOverlay(w, h int, ix *library.Index, query string, sel int) string {
	rows, _ := OverlayRows(w, h, ix, query, sel)
	return strings.Join(rows, "\n")
}

// OverlayRows returns the box rows and the row index at which the box starts.
//
// It exists so the box can be composited OVER the deck view rather than
// replacing it. Spec 6.5: during a set the deck view must not disappear, and
// the deck staying visible is the entire point of an overlay. The caller used
// to discard the deck entirely, leaving a floating box on an empty screen.
//
// The offset is a plain top-anchored row count: a mid-set box sits over the
// waveform rather than centred, because the eye is already there.
func OverlayRows(w, h int, ix *library.Index, query string, sel int) ([]string, int) {
	if w <= 0 || h <= 0 {
		return nil, 0
	}

	matches := library.Search(ix.Tracks(), query)

	boxW := minInt(w, 100)
	// The box is as tall as it needs: a title, a query line, a rule, results,
	// and a hint. A short terminal just shows fewer results.
	boxH := minInt(h, 4+minInt(len(matches), maxOverlayResults))

	inner := boxW - 2
	if inner < 0 {
		inner = 0
	}
	lines := make([]string, 0, boxH)

	lines = append(lines, titleStyle.Render(padTo("LOAD TRACK", inner)))
	lines = append(lines, labelStyle.Render(padTo("> "+query+"_", inner)))
	lines = append(lines, dimStyle.Render(padTo(strings.Repeat("-", inner), inner)))

	shown := matches
	if len(shown) > maxOverlayResults {
		shown = shown[:maxOverlayResults]
	}
	for i, t := range shown {
		cursor := "  "
		if i == sel {
			cursor = "> "
		}
		line := cursor + t.Title
		if t.Artist != "" {
			line += "  " + t.Artist
		}
		style := labelStyle
		if i == sel {
			style = accentStyle
		}
		lines = append(lines, style.Render(padTo(line, inner)))
	}

	if len(matches) == 0 {
		lines = append(lines, dimStyle.Render(padTo("no matches", inner)))
	} else if len(matches) > maxOverlayResults {
		lines = append(lines, dimStyle.Render(
			padTo(truncate("+"+itoa(len(matches)-maxOverlayResults)+" more", inner), inner)))
	}
	for len(lines) < boxH {
		lines = append(lines, spaces(inner))
	}
	lines = lines[:boxH]

	// Box border, padded to the full frame width so the overlay does not leave
	// a ragged right edge when the frame is wider than the box.
	rule := dimStyle.Render(strings.Repeat("-", w))
	out := make([]string, 0, boxH+2)
	out = append(out, rule)
	for _, l := range lines {
		out = append(out, pad(l, w))
	}
	out = append(out, rule)
	// Leave at least one row of deck visible above and below where possible, so
	// the box never fills a short terminal completely.
	offset := maxInt(0, (h-len(out))/2)
	if len(out)+offset > h {
		offset = maxInt(0, h-len(out))
	}
	return out, offset
}

// padTo pads a styled string to exactly w visible columns.
func padTo(s string, w int) string {
	s = truncate(s, w)
	if gap := w - lipglossWidth(s); gap > 0 {
		return s + spaces(gap)
	}
	return s
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	if neg {
		return "-" + string(b)
	}
	return string(b)
}
