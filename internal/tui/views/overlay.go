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
// the results, and the keys that drive them.
//
// The caller draws this OVER the deck view and dims what is behind it rather
// than erasing it: the deck staying visible is the entire point of an overlay
// (spec §6.5), so a DJ can see what is still playing while choosing a
// replacement.
//
// cursor is the caret position within query, in runes.
func RenderOverlay(w, h int, ix *library.Index, query string, cursor, sel int, hint string) string {
	rows, _ := OverlayRows(w, h, ix, query, cursor, sel, hint)
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
//
// hint is the mode's key line, drawn inside the box. The overlay is the one
// screen that cannot use the frame's footer row -- it is composited over the
// deck view, so a footer would overwrite rendered rows rather than sit below
// them -- and it is also the screen with the most to explain: five keys, four of
// them mode-relative. The caller owns the text, so it stays the same string the
// other modes' footers come from rather than a second copy here that can drift.
func OverlayRows(w, h int, ix *library.Index, query string, cursor, sel int, hint string) ([]string, int) {
	if w <= 0 || h <= 0 {
		return nil, 0
	}

	matches := library.Search(ix.Tracks(), query)

	boxW := minInt(w, 100)
	inner := boxW - 2
	if inner < 0 {
		inner = 0
	}

	// The box is as tall as it needs: a title, a query line, a rule, the
	// results, a status row and the key hint. A short terminal shows fewer
	// results -- it never drops the hint, which is the part of this screen the
	// user cannot infer from it.
	hintRows := 0
	if hint != "" {
		hintRows = 1
	}
	shown := matches
	if len(shown) > maxOverlayResults {
		shown = shown[:maxOverlayResults]
	}
	// Rows the box spends on something that is not a result: the two rules that
	// frame it, the title, the query line, the rule under it, a status row and
	// the hint.
	room := maxInt(0, h-2-3-1-hintRows)
	if len(shown) > room {
		shown = shown[:room]
	}

	lines := make([]string, 0, 3+len(shown)+1+hintRows)

	lines = append(lines, titleStyle.Render(padTo("LOAD TRACK", inner)))
	lines = append(lines, labelStyle.Render(padTo(caretField("> ", query, cursor), inner)))
	lines = append(lines, dimStyle.Render(padTo(strings.Repeat("-", inner), inner)))

	for i, t := range shown {
		marker := "  "
		if i == sel {
			marker = "> "
		}
		line := marker + t.Title
		if t.Artist != "" {
			line += "  " + t.Artist
		}
		style := labelStyle
		if i == sel {
			style = accentStyle
		}
		lines = append(lines, style.Render(padTo(line, inner)))
	}

	// One status row covers both ways there are fewer rows than matches: the
	// list is longer than the box shows by design, or the terminal is shorter
	// than the box. Neither is a reason to say there are no matches.
	switch {
	case len(matches) == 0:
		lines = append(lines, dimStyle.Render(padTo("no matches", inner)))
	case len(shown) < len(matches):
		lines = append(lines, dimStyle.Render(
			padTo(truncate("+"+itoa(len(matches)-len(shown))+" more", inner), inner)))
	}
	if hintRows > 0 {
		lines = append(lines, dimStyle.Render(padTo(truncate(hint, inner), inner)))
	}

	// Box border, padded to the full frame width so the overlay does not leave
	// a ragged right edge when the frame is wider than the box.
	rule := dimStyle.Render(strings.Repeat("-", w))
	out := make([]string, 0, len(lines)+2)
	out = append(out, rule)
	for _, l := range lines {
		out = append(out, pad(l, w))
	}
	out = append(out, rule)
	// A terminal too short even for that: drop rows rather than run past the
	// frame, which would push the deck view off the bottom of the screen.
	if len(out) > h {
		out = out[:h]
	}
	// Leave at least one row of deck visible above and below where possible, so
	// the box never fills a short terminal completely.
	offset := maxInt(0, (h-len(out))/2)
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
