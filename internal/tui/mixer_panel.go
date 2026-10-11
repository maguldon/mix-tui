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
	peak := func(i int) float64 {
		if i < 0 || i >= len(s.Decks) {
			return 0
		}
		return s.Decks[i].Peak
	}
	// Header labels the two channel columns. Built by padding, not slicing a
	// shorter string, so it cannot overrun when the panel is narrow.
	head := padRight("A", barW) + padRight("B", barW)
	// The crossfader comes first: a 5-row footer shows only 3 content rows, and
	// the crossfader is what a DJ reads in a stacked layout. The level bars are
	// the first thing that can go.
	rows = append(rows, PanelRow{Kind: RowText, Text: crossfaderRow(s.Crossfader, inner), Style: &dimStyle})
	rows = append(rows, PanelRow{Kind: RowText, Text: TruncateTo(head, inner), Style: &dimStyle})
	rows = append(rows, PanelRow{Kind: RowText, Text: TruncateTo(channelBar("A", lvl(0), peak(0), barW)+channelBar("B", lvl(1), peak(1), barW), inner)})

	// Master level, from the Snapshot's master bus rather than the loudest
	// deck: the mixer output is not the peak of its inputs once the crossfader
	// and channel faders have had their say.
	rows = append(rows, PanelRow{Kind: RowText, Text: "MASTER " + RenderPeakHold(s.MasterPeak, s.MasterLevel, maxInt(0, inner-7))})

	return Panel{Title: "MIXER", Width: w, Rows: rows}
}

// channelBar renders "X" plus a level bar, exactly barW columns wide.
//
// The bar carries a peak-hold marker at peak: without it a short transient is
// invisible by the time the eye returns to the meter, which is the one thing a
// gain-reduction display is for.
func channelBar(label string, level, peak float64, barW int) string {
	if barW <= 0 {
		return ""
	}
	inner := barW - 1 // the label occupies one column
	if inner < 0 {
		inner = 0
	}
	return padRight(label+RenderPeakHold(peak, level, inner), barW)
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
