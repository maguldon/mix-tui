// Package views renders the library browser and the mid-set load overlay.
//
// Both are pure functions of (width, height, index, state) to a string: no I/O,
// no terminal access. That is what lets the whole interface be asserted on
// without a terminal (spec §8).
package views

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/maguldon/mix-tui/internal/tui/library"
)

// minLibraryWidth is the hard floor for the three-pane browser.
//
// spec §6.5 chose a floor over permanently truncated columns: at a narrow
// terminal, telling the user to widen it is more useful than showing three
// unreadable slivers.
const minLibraryWidth = 120

// keyColWidth fits the widest possible key cell: a confidence marker plus a
// two-digit Camelot code ("✓12B").
const keyColWidth = 4

// keyHighConfidence is the point at which a detection is presented as certain.
// Below it but above the detection floor the key is shown with a "~" so the
// user knows the analyser was not confident.
const keyHighConfidence = 0.75

// formatKey renders the Camelot key with its confidence marker (spec 6.5):
//
//	✓  high confidence
//	~  low confidence -- shown, but flagged
//	?  below threshold; reported as undetected
//
// A key below keyConfidenceThreshold was never detected at all, so it has no
// Camelot value to show and reports "?". A detected key whose confidence is
// under keyHighConfidence is shown with "~": the detection is real but shaky,
// and hiding it entirely would be as unhelpful as showing it as certain.
//
// The three tiers exist because key detection on percussive or atonal material
// yields confident nonsense. A wrong key displayed confidently is worse than
// no key, because the user would crate-match against it -- so the band between
// "no idea" and "confident" is shown and marked rather than hidden or
// presented as certain.
//
// The confidence was stored on every track and never displayed: the list showed
// either the bare key or "--", with nothing in between.
func formatKey(t library.Track) string {
	if t.KeyCamelot == "" || t.KeyConfidence < keyConfidenceThreshold {
		return "?"
	}
	if t.KeyConfidence < keyHighConfidence {
		return "~" + t.KeyCamelot
	}
	return "✓" + t.KeyCamelot
}

// keyConfidenceThreshold matches the engine's gate. Below it a key is shown as
// unknown, because a displayed key is believed.
const keyConfidenceThreshold = 0.55

// Pane selects which pane has focus.
type Pane int

const (
	PaneCrates Pane = iota
	PaneTracks
	// PaneSearch is the filter field. It is the third stop in the cycle because
	// the filter is a field, not a list: tabbing to it is how a user finds out
	// they can type.
	PaneSearch

	// PaneCount is the cycle length for TAB.
	PaneCount
)

// SortKeys is the order the sort menu presents, matching sortRows. The model
// needs the same list to move a selection and to apply it.
var SortKeys = []library.SortKey{
	library.SortTitle,
	library.SortArtist,
	library.SortBPM,
	library.SortByKey,
	library.SortAdded,
}

// LibraryState is the browser's interaction state.
type LibraryState struct {
	Pane     Pane
	CrateSel int
	TrackSel int
	Query    string
	SortBy   library.SortKey
	SortDesc bool
	// ShowSort swaps the detail pane for the sort menu.
	ShowSort bool
	Width    int
}

// RenderLibrary draws the three-pane browser: crates | tracks | detail.
func RenderLibrary(w, h int, ix *library.Index, st LibraryState) string {
	if w <= 0 || h <= 0 {
		return ""
	}
	if w < minLibraryWidth {
		return tooNarrow(w, minLibraryWidth, "library browser")
	}

	tracks := ix.Tracks()
	if st.Query != "" {
		tracks = library.Search(tracks, st.Query)
	}
	tracks = library.Sort(tracks, st.SortBy, st.SortDesc)

	// Fixed panes take their width; the track pane absorbs the remainder so the
	// frame is exactly w columns wide. Leftover width goes to the track list
	// rather than being dropped, which would leave the right edge ragged.
	gap := 1
	crateW := 28
	detailW := 22
	if w < crateW+detailW+3*gap {
		// Too narrow for three panes: drop the detail pane before squeezing the
		// track list into something unreadable.
		detailW = 0
	}
	if crateW > w-2*gap {
		crateW = maxInt(4, w-2*gap)
	}
	trackW := w - crateW - detailW - 2*gap
	if trackW < 20 {
		detailW = 0
		trackW = w - crateW - gap
	}
	if trackW < 1 {
		trackW = 1
	}

	rows := h - 1 // one header row
	trackPane := trackRows(tracks, st, trackW, rows)
	cratePane := crateRows(ix, crateW, rows)

	right := detailRows(tracks, st, detailW, rows)
	if st.ShowSort {
		right = sortRows(st, detailW, rows)
	}

	lines := combine([][]string{cratePane, trackPane, right}, gap, w, h)
	return frame(lines, w, h)
}

// trackRows renders the middle pane.
func trackRows(tracks []library.Track, st LibraryState, w, rows int) []string {
	out := make([]string, 0, rows)
	header := pad(trackHeader(w), w)
	out = append(out, header)
	for i := 0; i < rows-1; i++ {
		idx := i + scrollOffset(st, rows)
		if idx < 0 || idx >= len(tracks) {
			out = append(out, spaces(w))
			continue
		}
		cursor := "  "
		if idx == st.TrackSel {
			cursor = "> "
		}
		out = append(out, pad(cursor+trackLine(tracks[idx], w-2), w))
	}
	return out
}

// scrollOffset keeps the selection visible in a long list.
func scrollOffset(st LibraryState, rows int) int {
	visible := rows - 1
	if visible < 1 {
		visible = 1
	}
	if st.TrackSel < visible {
		return 0
	}
	return st.TrackSel - visible + 1
}

// colWidths derives the track pane's column widths from the pane width, once,
// so the header and every row line up. Computing them separately per row is how
// a column drifts by one and the whole table stops being readable.
func colWidths(w int) (title, artist int) {
	const bpm, key, dur, gap = 7, 4, 6, 3
	avail := w - bpm - key - dur - gap - 3 // minus the cursor and a trailing gap
	if avail < 10 {
		// Too narrow for both names: give the title what it needs and drop the
		// artist rather than truncating the title to nothing.
		return maxInt(0, avail), 0
	}
	title = avail / 2
	artist = avail - title
	if title > 28 {
		// Leftover goes to the title first: a DJ scans titles.
		title = minInt(28, avail-6)
		artist = avail - title
	}
	return title, artist
}

// trackHeader labels the columns using the same widths as the rows.
//
// It takes the FULL pane width, not the post-cursor width the rows use, and
// subtracts the cursor itself. Computing these separately is how every column
// ends up one to the left of its header.
func trackHeader(w int) string {
	tw, aw := colWidths(w - cursorW)
	return padRight("  "+cell(tw, "TITLE")+" "+cell(aw, "ARTIST")+" "+right("BPM", 7)+" "+right("KEY", keyColWidth)+" "+right("TIME", 6), w)
}

// cursorW is the width reserved for the selection cursor.
const cursorW = 2

// cell renders s in exactly w display columns, truncating if too wide.
//
// This exists because fmt's %-*s pads by RUNE COUNT, not display width. A CJK
// title then overruns its column by one column per character and every column
// after it drifts right -- exactly the bug wide-character alignment is meant to
// prevent.
func cell(w int, s string) string { return padRight(truncate(s, w), w) }

// right renders s right-aligned in w display columns, for numeric columns.
func right(s string, w int) string {
	s = truncate(s, w)
	gap := w - lipgloss.Width(s)
	if gap <= 0 {
		return s
	}
	return spaces(gap) + s
}

// trackLine renders one track row, dropping the duration bar first when space
// runs short and distributing leftover width to title then artist.
func trackLine(t library.Track, w int) string {
	if w <= 0 {
		return ""
	}
	bpm := "--"
	if t.BPM > 0 {
		bpm = fmt.Sprintf("%.2f", t.BPM)
	}
	key := formatKey(t)
	dur := mmss(t.Duration)

	tw, aw := colWidths(w)
	if aw == 0 {
		return padRight(cell(tw, t.Title)+" "+right(bpm, 7)+" "+right(key, keyColWidth)+" "+right(dur, 6), w)
	}
	return padRight(cell(tw, t.Title)+" "+cell(aw, t.Artist)+" "+right(bpm, 7)+" "+right(key, keyColWidth)+" "+right(dur, 6), w)
}

// crateRows renders the left pane: crate names plus the scan summary.
func crateRows(ix *library.Index, w, rows int) []string {
	out := make([]string, 0, rows)
	out = append(out, pad(dimStyle.Render("CRATES"), w))

	// Spec 8: a root that could not be read is listed as unreachable, with the
	// reason. Without this an unmounted music drive produced an empty library
	// and no explanation at all.
	if un := ix.Unreachable(); len(un) > 0 {
		out = append(out, pad(warnStyle.Render("UNREACHABLE"), w))
		for _, u := range un {
			if len(out) >= rows-2 {
				break
			}
			out = append(out, pad(truncate("  "+u, w), w))
		}
	}
	out = append(out, pad(dimStyle.Render(strings.Repeat("-", minInt(w, 6))), w))
	entries := []string{"All tracks", fmt.Sprintf("  (%d tracks)", ix.Len())}
	// Crate storage is not implemented. Listing numbered placeholders filled
	// the pane with "(crate 7: none saved)", which reads as lost data rather
	// than as an absent feature -- an honest empty pane is better than a
	// misleading full one. Once crates exist this becomes a real list.
	for _, e := range entries {
		if len(out) >= rows {
			return out
		}
		out = append(out, pad(e, w))
	}
	// One line, not one per row: the pane is mostly empty, and filling it with
	// the same placeholder reads as though something is wrong.
	if len(out) < rows {
		out = append(out, pad(dimStyle.Render("  (no crates yet)"), w))
	}
	for len(out) < rows {
		out = append(out, pad("", w))
	}
	return out
}

// detailRows renders the right pane for the selected track.
func detailRows(tracks []library.Track, st LibraryState, w, rows int) []string {
	if w <= 0 {
		return make([]string, rows)
	}
	var lines []string
	lines = append(lines, labelStyle.Render("DETAIL"))
	if t, ok := pick(tracks, st.TrackSel); ok {
		lines = append(lines, truncate(t.Title, w))
		lines = append(lines, dimStyle.Render(truncate(t.Artist, w)))
		lines = append(lines, "")
		lines = append(lines, truncate("BPM  "+bpmText(t), w))
		lines = append(lines, truncate("KEY  "+keyText(t), w))
		lines = append(lines, truncate("key conf "+fmt.Sprintf("%.0f%%", t.KeyConfidence*100), w))
		lines = append(lines, "")
		lines = append(lines, truncate("TIME "+mmss(t.Duration), w))
		lines = append(lines, truncate("PATH "+t.Path, w))
	} else {
		lines = append(lines, dimStyle.Render(truncate("no selection", w)))
	}
	for len(lines) < rows {
		lines = append(lines, spaces(w))
	}
	return lines[:rows]
}

// sortRows renders the sort menu that replaces the detail pane.
func sortRows(st LibraryState, w, rows int) []string {
	opts := []struct {
		name string
		k    library.SortKey
	}{
		{"title", library.SortTitle},
		{"artist", library.SortArtist},
		{"BPM", library.SortBPM},
		{"key", library.SortByKey},
		{"added", library.SortAdded},
	}
	out := make([]string, 0, rows)
	dir := "asc"
	if st.SortDesc {
		dir = "desc"
	}
	out = append(out, labelStyle.Render("SORT BY  ("+dir+")"))
	for i, o := range opts {
		label := "  " + o.name
		if library.SortKey(i) == st.SortBy {
			// The current sort is marked, so the menu shows where it is
			// rather than making the user remember a setting they cannot see.
			label = "> " + o.name
			out = append(out, truncate(label, w))
			continue
		}
		out = append(out, truncate(label, w))
	}
	for len(out) < rows {
		out = append(out, spaces(w))
	}
	return out[:rows]
}

// MatchTrack returns the track a selection refers to after filtering.
//
// The selection applies to the FILTERED list, so filtering first and clamping
// after is what keeps a stale index from returning the wrong track.
func MatchTrack(ts []library.Track, query string, sel int) (library.Track, bool) {
	return pick(library.Search(ts, query), sel)
}

func pick(tracks []library.Track, sel int) (library.Track, bool) {
	if sel < 0 || sel >= len(tracks) {
		return library.Track{}, false
	}
	return tracks[sel], true
}

func bpmText(t library.Track) string {
	if t.BPM <= 0 {
		return "--"
	}
	return fmt.Sprintf("%.2f", t.BPM)
}

func keyText(t library.Track) string {
	if t.KeyCamelot == "" || t.KeyConfidence < keyConfidenceThreshold {
		return "--"
	}
	return t.KeyCamelot
}

func combine(cols [][]string, gap, w, h int) []string {
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
		for j, c := range cols {
			if j > 0 {
				b.WriteString(gapStr)
			}
			if i < len(c) {
				b.WriteString(c[i])
			} else {
				b.WriteString(spaces(colWidth(cols, j)))
			}
		}
		out = append(out, b.String())
	}
	for len(out) < h {
		out = append(out, spaces(w))
	}
	return out[:h]
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func colWidth(cols [][]string, j int) int {
	w := 0
	for _, l := range cols[j] {
		if lw := lipgloss.Width(l); lw > w {
			w = lw
		}
	}
	return w
}

// frame makes the output an exact w-by-h rectangle: every line is padded to w
// and the row count clamped to h. A ragged frame makes a full-screen app
// repaint noise, so this is not cosmetic.
func frame(lines []string, w, h int) string {
	out := make([]string, h)
	for i := 0; i < h; i++ {
		if i < len(lines) {
			out[i] = pad(lines[i], w)
		} else {
			out[i] = spaces(w)
		}
	}
	return strings.Join(out, "\n")
}

func tooNarrow(w, need int, what string) string {
	msg := fmt.Sprintf("terminal too small: %d cols, need %d", w, need)
	sub := fmt.Sprintf("the %s needs %d columns", what, need)
	return frame([]string{pad(msg, w), "", dimStyle.Render(pad(sub, w))}, w, 3)
}

func mmss(d time.Duration) string {
	if d <= 0 {
		return "--:--"
	}
	m := int(d / time.Minute)
	s := int((d % time.Minute) / time.Second)
	return fmt.Sprintf("%d:%02d", m, s)
}
