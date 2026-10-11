package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/maguldon/mix-tui/internal/engine"
)

// renderDeckPanel builds one deck's panel.
//
// Rows are assembled then the panel is returned; RenderPanel clips to whatever
// height the layout gives it, so an extra row here costs nothing and a missing
// one leaves a blank line.
func renderDeckPanel(w int, d engine.DeckState, st ViewState, focused bool, contentRows int) Panel {
	inner := w - 2
	if inner < 0 {
		inner = 0
	}
	if !d.Loaded {
		title := string(rune('A' + d.Index))
		if focused {
			title += "*"
		}
		return Panel{Title: title, Width: w, Rows: emptyDeckRows(inner)}
	}

	// rowsAvail is the content height the layout will give this panel. The panel
	// adapts to it rather than emitting a fixed row list that gets clipped.
	rowsAvail := contentRows

	waveH := 4
	if inner < 40 {
		waveH = 2 // a compact panel cannot afford 4 waveform rows
	}
	if rowsAvail < 9 {
		waveH = minInt(waveH, 2)
	}
	if rowsAvail < 7 {
		waveH = 0
	}

	// Zoom replaces the full-track wave with a sliding window when this deck is
	// focused, which is the CDJ behaviour the spec asks for. The unfocused deck
	// keeps the whole track, so a zoom never hides the other deck's context.
	var wave []string
	if focused && st.Zoom && d.HasBeatGrid {
		zwin := WindowFor(d.Position, st.ZoomBars, d.BPM)
		rows := RenderZoomed(d.PCM, zwin, inner, waveH, d.BeatTimes, cueSlice(d), d.SampleRate)
		for i := range rows {
			rows[i] = padRight(rows[i], inner)
		}
		wave = rows
	}
	if wave == nil {
		wave = RenderWave(WaveSpec{
			Width:  inner,
			Height: waveH,
			// Peaks come from the engine's cache, resampled to this panel's
			// width. Deriving them here cost ~33ms per frame per deck; at 30fps
			// that is two CPU-seconds per wall-clock second and it thrashes the
			// cache the audio thread needs.
			Peaks:   resamplePeaks(d.Peaks, inner),
			PlayCol: playCell(d, inner),
			Cues:    cueCells(d, inner),
			Loops:   loopSpans(d, inner),
		})
	}

	rows := make([]PanelRow, 0, 10)
	// A short panel drops the waveform before it drops the transport row: a
	// missing waveform is a smaller loss than a deck that shows no PLAY or CUE
	// indicator at all, which is what happens when the rows are simply
	// clipped by the layout.
	maxWave := waveH
	if rowsAvail <= 6 {
		maxWave = 0
	} else if rowsAvail <= 8 {
		maxWave = minInt(maxWave, 2)
	}
	for i := 0; i < maxWave && i < len(wave); i++ {
		rows = append(rows, PanelRow{Kind: RowText, Text: wave[i]})
	}

	// Progress / playhead bar.
	rows = append(rows, PanelRow{Kind: RowText, Text: progressBar(d, inner), Style: &dimStyle})

	// Title and artist.
	rows = append(rows, PanelRow{Kind: RowText, Text: padRight(TruncateTo(titleOrDash(d.Title), inner), inner), Style: &titleStyle})
	if d.Artist != "" {
		rows = append(rows, PanelRow{Kind: RowText, Text: dimStyle.Render(TruncateTo(d.Artist, inner))})
	}

	// The metadata row drops whole fields in priority order rather than
	// truncating mid-field: a panel showing "128.00 BP" is nonsense, while one
	// showing only "128.00" is merely incomplete.
	rows = append(rows, PanelRow{Kind: RowText, Text: metaRow(d, inner)})

	// EQ bars, dropped before the transport row when the panel is short.
	if rowsAvail >= len(rows)+3 {
		rows = append(rows, PanelRow{Kind: RowText, Text: eqRow(inner), Style: &dimStyle})
	}

	// The full-track overview strip. When zoom is off, the waveform already
	// shows the whole track and an overview of it would be redundant -- but
	// when zoom is on, the waveform is a window and the strip is the only thing
	// showing where in the track that window sits.
	//
	// RenderOverview was written and unit-tested and never called from a view:
	// the first review fixed "it returned a blank strip", which was true and
	// also irrelevant, because no panel ever asked for one.
	if st.Zoom && d.HasBeatGrid && rowsAvail > 6 {
		w0, w1 := overviewWindow(d, inner)
		rows = append(rows, PanelRow{
			Kind: RowText,
			Text: padRight(RenderOverview(resamplePeaks(d.Peaks, inner),
				inner, w0, w1, playCell(d, inner)), inner),
		})
	}

	// Transport.
	rows = append(rows, PanelRow{Kind: RowText, Text: transportRow(d, inner)})

	if d.LoopActive {
		rows = append(rows, PanelRow{
			Kind:  RowText,
			Text:  TruncateTo(fmt.Sprintf("LOOP %g beats", d.LoopBeatCount), inner),
			Style: &okStyle,
		})
	} else if d.Playing {
		// The accent marks a playing deck. It used to show whenever no loop was
		// active, which included every stopped deck -- so a row of arrows sat
		// under an idle deck implying something was happening.
		if accent := firstLine(RenderArt(inner, 1, "accent")); accent != "" {
			rows = append(rows, PanelRow{Kind: RowText, Text: accent, Style: &okStyle})
		}
	}
	// The dBEAT readout is the headline affordance of the zoom view and the way
	// a DJ lines two decks up (spec 6.3). It belongs on the focused deck, since
	// it is measured against the other one. It was computed every frame and then
	// never rendered, so the whole thing cost work and showed nothing.
	if focused && d.HasDelta {
		// The offset text is already styled by RenderDeltaBeat (warn colour when
		// the error is audible), so it is padded rather than re-styled: styling
		// the padded string would make lipgloss count the ANSI padding.
		rows = append(rows, PanelRow{
			Kind: RowText,
			Text: padRight(TruncateTo(RenderDeltaBeat(d, st.OtherDeck), inner), inner),
		})
	}

	// The title names the deck. A focused deck is marked so the user can tell
	// which one TAB will act on; hardcoding "A" would mislabel deck B.
	title := string(rune('A' + d.Index))
	if focused {
		title += "*"
	}
	return Panel{Title: title, Width: w, Rows: rows}
}

// firstLine returns the first line, or "" when there are none. RenderArt
// returns nil for a zero width, so indexing it directly would panic.
func firstLine(lines []string) string {
	if len(lines) == 0 {
		return ""
	}
	return lines[0]
}

// resamplePeaks narrows a cached envelope to w cells.
//
// Two entries per cell, matching Peaks' stereo layout. Narrowing an envelope
// takes the extreme of the samples it collapses, so a transient between cells
// still shows rather than disappearing.
//
// Everything below indexes ENTRIES, not cells. Peaks stores 2 entries per cell
// (left/right channel), so an envelope for a w-cell panel is 2w entries long.
// The first version computed its source window in cells and then used those
// indices against the entry arrays, which both under-covered the tail and mixed
// the two coordinate systems. For every panel narrower than the 64 cells the
// cache holds -- which is every default layout -- the right half of the envelope
// was sampled from the wrong place.
func resamplePeaks(p engine.Peaks, w int) Peaks {
	if w <= 0 || len(p.Max) == 0 {
		return Peaks{Min: make([]float32, w*2), Max: make([]float32, w*2)}
	}
	n := len(p.Max)
	outN := w * 2
	if n == outN {
		return p // already the right resolution
	}
	out := Peaks{Min: make([]float32, outN), Max: make([]float32, outN)}
	for c := 0; c < outN; c++ {
		lo := c * n / outN
		hi := (c + 1) * n / outN
		if hi <= lo {
			hi = lo + 1
		}
		if hi > n {
			hi = n
		}
		var mn, mx float32
		mn, mx = p.Min[lo], p.Max[lo]
		for i := lo + 1; i < hi; i++ {
			if p.Min[i] < mn {
				mn = p.Min[i]
			}
			if p.Max[i] > mx {
				mx = p.Max[i]
			}
		}
		out.Min[c], out.Max[c] = mn, mx
	}
	return out
}

// cueSlice bounds CueCount before slicing. CueCount is a plain exported int on
// DeckState, so a value beyond the array length panics -- and a panic in a view
// takes the interface down.
func cueSlice(d engine.DeckState) []time.Duration {
	n := d.CueCount
	if n < 0 {
		n = 0
	}
	if n > len(d.CueTimes) {
		n = len(d.CueTimes)
	}
	return d.CueTimes[:n]
}

// cueCells maps hotcue times to waveform cells.
func cueCells(d engine.DeckState, w int) []int {
	if w <= 0 || d.CueCount == 0 || d.Duration <= 0 {
		return nil
	}
	n := d.CueCount
	if n > len(d.CueTimes) {
		n = len(d.CueTimes)
	}
	out := make([]int, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, int(clampFloat(float64(d.CueTimes[i])/float64(d.Duration), 0, 1)*float64(w)))
	}
	return out
}

// loopSpans maps an active loop to waveform cells, so a deck playing past the
// end of its track visibly reads as looping rather than stalled (spec §6.2.1).
func loopSpans(d engine.DeckState, w int) []LoopSpan {
	if w <= 0 || !d.LoopActive || d.Duration <= 0 || d.LoopEnd <= d.LoopStart {
		return nil
	}
	at := func(t time.Duration) int {
		return int(clampFloat(float64(t)/float64(d.Duration), 0, 1) * float64(w))
	}
	return []LoopSpan{{Start: at(d.LoopStart), End: at(d.LoopEnd)}}
}

// renderDeckStrip renders a deck as a single compact line, for the collapsed
// strip in layout B. A full panel clipped to two rows looks identical to the
// expanded deck, which defeats the point of a focused layout: the user must be
// able to tell at a glance which deck TAB will act on.
func renderDeckStrip(w int, d engine.DeckState) Panel {
	inner := w - 2
	if inner < 0 {
		inner = 0
	}
	state := "stop"
	if d.Loaded {
		if d.Playing {
			state = "PLAY"
		} else {
			state = "stop"
		}
	}
	line := fmt.Sprintf("%-16s %-6s", titleOrDash(d.Title), state)
	rows := []PanelRow{{Kind: RowText, Text: TruncateTo(line, inner), Style: &dimStyle}}
	return Panel{Title: string(rune('A' + d.Index)), Width: w, Rows: rows}
}

// emptyDeckRows shows the load prompt rather than a blank panel.
func emptyDeckRows(inner int) []PanelRow {
	art := RenderArt(inner, 5, "empty")
	rows := make([]PanelRow, 0, len(art))
	for _, l := range art {
		rows = append(rows, PanelRow{Kind: RowText, Text: l, Style: &dimStyle})
	}
	return rows
}

// progressBar draws the playhead position across the panel width.
func progressBar(d engine.DeckState, w int) string {
	if w <= 0 {
		return ""
	}
	if d.Duration <= 0 {
		return spaces(w)
	}
	frac := clampFloat(float64(d.Position)/float64(d.Duration), 0, 1)
	pos := int(frac * float64(w-1))
	b := make([]rune, w)
	for i := range b {
		if i < pos {
			b[i] = '━'
		} else {
			b[i] = '─'
		}
	}
	if pos >= 0 && pos < w {
		b[pos] = '┃'
	}
	return string(b)
}

// metaRow builds the BPM · KEY · pitch · remaining row, dropping the least
// important whole fields that do not fit.
func metaRow(d engine.DeckState, w int) string {
	if w <= 0 {
		return ""
	}
	bpm := "--"
	if d.HasBeatGrid && d.BPM > 0 {
		bpm = fmt.Sprintf("%.2f", d.BPM)
	}
	key := "--"
	// A displayed key is believed, so a low-confidence one is not shown at
	// all. The engine already blanks these below threshold; the view gates too
	// so a directly-attached low-confidence key cannot leak through.
	if d.KeyCamelot != "" && d.KeyConfidence >= keyConfidenceThreshold {
		key = d.KeyCamelot
	}
	rem := "--:--"
	if d.Loaded && d.Duration > 0 {
		rem = mmss(d.Duration - d.Position)
	}
	pitch := fmt.Sprintf("%+.0f%%", d.Pitch*100)

	// Priority: BPM first, then pitch, then key, then remaining time.
	for _, fields := range [][]string{
		{bpm, key, pitch, rem},
		{bpm, pitch, rem},
		{bpm, pitch},
		{bpm},
	} {
		line := strings.Join(fields, "  ")
		if lipgloss.Width(line) <= w {
			return line
		}
	}
	return bpm
}

const keyConfidenceThreshold = 0.55

// eqRow draws low/mid/high EQ bars.
func eqRow(w int) string {
	if w < 12 {
		return spaces(w)
	}
	each := (w - 2) / 3
	if each < 1 {
		return spaces(w)
	}
	parts := make([]string, 3)
	for i, c := range []string{"L", "M", "H"} {
		parts[i] = c + RenderMeter(0, each-1)
	}
	return strings.Join(parts, "")
}

// transportRow renders the CUE/PLAY/SYNC/SLIP/QUANT indicator row.
func transportRow(d engine.DeckState, w int) string {
	on := func(b bool, label string) string {
		if b {
			return okStyle.Render(label)
		}
		return dimStyle.Render(label)
	}
	s := on(d.Playing, "PLAY")
	if w >= lipgloss.Width(s)+6 {
		s += " " + on(d.Cued, "CUE")
	}
	return TruncateTo(s, w)
}

// overviewWindow maps the deck's duration into the cells of an overview strip.
//
// The window covers one eighth of the track, which is the widest zoom level
// the view offers: at that setting the strip is most useful, because the window
// is smallest relative to the whole.
func overviewWindow(d engine.DeckState, width int) (start, end int) {
	if width <= 0 || d.Duration <= 0 {
		return -1, -2
	}
	span := d.Duration / 8
	if span <= 0 {
		span = d.Duration
	}
	start = int(float64(d.Position) / float64(d.Duration) * float64(width))
	end = int(float64(d.Position+span) / float64(d.Duration) * float64(width))
	start = minInt(maxInt(0, start), width-1)
	end = minInt(maxInt(start, end), width-1)
	return start, end
}
