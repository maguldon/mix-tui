package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/maguldon/mix-tui/internal/engine"
)

// renderDeckPanel builds one deck's panel.
//
// Rows are assembled then the panel is returned; RenderPanel clips to whatever
// height the layout gives it, so an extra row here costs nothing and a missing
// one leaves a blank line.
func renderDeckPanel(w int, d engine.DeckState, st ViewState, focused bool) Panel {
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

	waveH := 4
	if inner < 40 {
		waveH = 2 // a compact panel cannot afford 4 waveform rows
	}
	wave := RenderWave(WaveSpec{
		Width:   inner,
		Height:  waveH,
		Peaks:   Peaks{},
		PlayCol: playCell(d, inner),
	})

	rows := make([]PanelRow, 0, 10)
	for i, r := range wave {
		rows = append(rows, PanelRow{Kind: RowText, Text: r})
		_ = i
	}

	// Progress / playhead bar.
	rows = append(rows, PanelRow{Kind: RowText, Text: progressBar(d, inner), Style: dimStyle})

	// Title and artist.
	rows = append(rows, PanelRow{Kind: RowText, Text: padRight(TruncateTo(titleOrDash(d.Title), inner), inner), Style: titleStyle})
	if d.Artist != "" {
		rows = append(rows, PanelRow{Kind: RowText, Text: dimStyle.Render(TruncateTo(d.Artist, inner))})
	}

	// The metadata row drops whole fields in priority order rather than
	// truncating mid-field: a panel showing "128.00 BP" is nonsense, while one
	// showing only "128.00" is merely incomplete.
	rows = append(rows, PanelRow{Kind: RowText, Text: metaRow(d, inner)})

	// EQ bars.
	rows = append(rows, PanelRow{Kind: RowText, Text: eqRow(inner), Style: dimStyle})

	// Transport.
	rows = append(rows, PanelRow{Kind: RowText, Text: transportRow(d, inner)})

	if d.LoopActive {
		rows = append(rows, PanelRow{
			Kind:  RowText,
			Text:  TruncateTo(fmt.Sprintf("LOOP %g beats", d.LoopBeatCount), inner),
			Style: okStyle,
		})
	} else if accent := firstLine(RenderArt(inner, 1, "accent")); accent != "" {
		rows = append(rows, PanelRow{Kind: RowText, Text: accent, Style: okStyle})
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
	rows := []PanelRow{{Kind: RowText, Text: TruncateTo(line, inner), Style: dimStyle}}
	return Panel{Title: string(rune('A' + d.Index)), Width: w, Rows: rows}
}

// emptyDeckRows shows the load prompt rather than a blank panel.
func emptyDeckRows(inner int) []PanelRow {
	art := RenderArt(inner, 5, "empty")
	rows := make([]PanelRow, 0, len(art))
	for _, l := range art {
		rows = append(rows, PanelRow{Kind: RowText, Text: l, Style: dimStyle})
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
