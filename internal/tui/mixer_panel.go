package tui

import (
	"github.com/maguldon/mix-tui/internal/engine"
)

// renderMixerPanel builds the centre mixer: per-channel level, the crossfader,
// and master. Per spec §6.1 the centre column stays narrow, so per-deck EQ
// lives in the deck panels rather than here.
func renderMixerPanel(w int, s engine.Snapshot, st ViewState) Panel {
	inner := w - 2
	if inner < 0 {
		inner = 0
	}
	rows := make([]PanelRow, 0, 8)

	// Two channel columns: label plus a level bar each.
	barW := (inner - 3) / 2
	if barW < 1 {
		barW = 1
	}
	lvl := func(i int) float64 {
		if i < 0 || i >= len(s.Decks) {
			return 0
		}
		return s.Decks[i].Level
	}
	// Header labels the two channel columns. Built by padding, not slicing a
	// shorter string, so it cannot overrun when the panel is narrow.
	head := padRight("A", barW) + padRight("B", barW)
	rows = append(rows, PanelRow{Kind: RowText, Text: TruncateTo(head, inner), Style: &dimStyle})
	rows = append(rows, PanelRow{Kind: RowText, Text: TruncateTo(channelBar("A", lvl(0), barW)+channelBar("B", lvl(1), barW), inner)})

	rows = append(rows, PanelRow{Kind: RowText, Text: TruncateTo("xfade", inner), Style: &dimStyle})
	rows = append(rows, PanelRow{Kind: RowText, Text: crossfaderRow(s.Crossfader, inner)})

	rows = append(rows, PanelRow{Kind: RowText, Text: TruncateTo("MASTER", inner), Style: &dimStyle})
	master := 0.0
	for i := range s.Decks {
		if lvl(i) > master {
			master = lvl(i)
		}
	}
	rows = append(rows, PanelRow{Kind: RowText, Text: RenderPeakHold(master, master, inner)})

	return Panel{Title: "MIXER", Width: w, Rows: rows}
}

// channelBar renders "X" plus a level bar, exactly barW columns wide.
func channelBar(label string, level float64, barW int) string {
	if barW <= 0 {
		return ""
	}
	inner := barW - 1 // the label occupies one column
	if inner < 0 {
		inner = 0
	}
	return padRight(label+RenderMeter(level, inner), barW)
}

// crossfaderRow draws the crossfader as A <track> B with a centre-relative
// marker, which is how a DJ reads which side they are on.
func crossfaderRow(pos float64, w int) string {
	if w <= 0 {
		return ""
	}
	track := w - 4 // leave room for "A " and " B"
	if track < 1 {
		track = 1
	}
	pct := clampFloat((pos+1)/2.0, 0, 1)
	mark := int(pct * float64(track-1))
	b := make([]rune, track)
	for i := range b {
		b[i] = '·'
	}
	if mark >= 0 && mark < track {
		b[mark] = '┃'
	}
	return "A " + string(b) + " B"
}
