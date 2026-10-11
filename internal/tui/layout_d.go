package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/maguldon/mix-tui/internal/engine"
)

// mixerPanel builds the centre mixer panel. Task 5 fills in the real faders.
func mixerPanel(w int) Panel {
	rows := []PanelRow{
		{Kind: RowText, Text: "EQ", Style: &dimStyle},
		{Kind: RowText, Text: "A      B"},
		{Kind: RowText, Text: "||||  ||||"},
		{Kind: RowMeter, Text: "----cross----", Style: &dimStyle},
		{Kind: RowText, Text: " master"},
	}
	return Panel{Title: "MIXER", Width: w, Rows: rows}
}

// minWidthD is the narrowest terminal that can show layout D's three columns.
// Below it we say so rather than drawing overlapping panels, which is what
// review focus #2 describes.
const minWidthD = 90

// clampInt bounds v to [lo, hi].
func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// renderD draws the default layout (spec §6.1): deck A | mixer | deck B, with
// the mixer centred because that matches the Xone K2's physical layout.
func renderD(w, h int, s engine.Snapshot, st ViewState) string {
	if w <= 0 || h <= 0 {
		return ""
	}
	if w < minWidthD {
		return tooSmall(w, minWidthD, "deck + mixer + deck")
	}

	gap := 1
	mixerW := clampInt(w/5, 14, 24)
	deckW := clampInt((w-mixerW-2*gap)/2, 24, 52)

	// If the computed columns do not fill the width exactly, absorb the
	// remainder into the left deck so the frame stays rectangular.
	used := 2*deckW + mixerW + 2*gap
	if used < w {
		deckW += w - used
	}

	panelH := h
	left := RenderPanel(renderDeckPanel(deckW, deckAt(&s, 0), st, st.Focus == 0), panelH)
	mid := RenderPanel(renderMixerPanel(mixerW, s, st), panelH)
	right := RenderPanel(renderDeckPanel(deckW, deckAt(&s, 1), st, st.Focus == 1), panelH)

	cols := RenderColumns([][]string{left, mid, right}, gap, w)
	// Guarantee exactly h rows for a clean full-screen frame.
	for len(cols) < h {
		cols = append(cols, spaces(w))
	}
	return joinLines(cols[:h], w)
}

// tooSmall renders the "make the window bigger" placeholder.
func tooSmall(w, need int, what string) string {
	msg := fmt.Sprintf("terminal too small: %d cols, need %d", w, need)
	sub := fmt.Sprintf("%s layout needs %d columns", what, need)
	return joinLines([]string{titleStyle.Render(padRight(msg, w)), "", dimStyle.Render(padRight(sub, w))}, w)
}

func joinLines(lines []string, w int) string {
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = padRight(l, w)
	}
	return joinWith(out, "\n")
}

func joinWith(lines []string, sep string) string {
	return strings.Join(lines, sep)
}

// deckAt returns deck i, or a zero state when the snapshot has fewer decks.
func deckAt(s *engine.Snapshot, i int) engine.DeckState {
	if s == nil || i < 0 || i >= len(s.Decks) {
		return engine.DeckState{Index: i}
	}
	return s.Decks[i]
}

// deckPanel builds the placeholder panel for one deck. Task 5 fills in the real
// waveform, transport, EQ, and hotcues; what this task delivers is the panel
// structure and the column arithmetic.
func deckPanel(name string, width int, d engine.DeckState, st ViewState, idx int) Panel {
	rows := []PanelRow{
		{Kind: RowText, Text: titleOrDash(d.Title), Style: &titleStyle},
		{Kind: RowText, Text: transportLine(d)},
		{Kind: RowWave, Wave: &WaveSpec{
			Width:   24,
			Height:  4,
			Peaks:   Peaks{},
			PlayCol: playCell(d, 24),
		}},
		{Kind: RowText, Text: positionLine(d)},
	}
	if d.HasBeatGrid {
		rows = append(rows, PanelRow{Kind: RowText, Text: fmt.Sprintf("%3.1f BPM  key %s", d.BPM, "-"), Style: &dimStyle})
	}
	if d.LoopActive {
		rows = append(rows, PanelRow{Kind: RowText, Text: fmt.Sprintf("loop %.0f beats", d.LoopBeatCount), Style: &okStyle})
	}
	if idx == st.Focus {
		rows = append(rows, PanelRow{Kind: RowText, Text: "focus", Style: &okStyle})
	}
	return Panel{Title: name, Width: width, Rows: rows}
}

func titleOrDash(s string) string {
	if s == "" {
		return "--"
	}
	return s
}

func transportLine(d engine.DeckState) string {
	if !d.Loaded {
		return "empty"
	}
	state := "stop"
	if d.Playing {
		state = "PLAY"
	}
	return state
}

func positionLine(d engine.DeckState) string {
	if !d.Loaded || d.Duration <= 0 {
		return "--:-- / --:--"
	}
	pos := d.Position
	return fmt.Sprintf("%s / %s", mmss(pos), mmss(d.Duration))
}

// playCell converts a deck position into a waveform cell index for a wave of
// the given width, or -1 when there is nothing loaded.
func playCell(d engine.DeckState, width int) int {
	if d.Duration <= 0 || width <= 0 {
		return -1
	}
	f := float64(d.Position) / float64(d.Duration)
	return int(clampFloat(f, 0, 1) * float64(width))
}

func clampFloat(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// mmss formats a duration as M:SS, which is how a DJ reads a position.
func mmss(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	m := int(d / time.Minute)
	s := int((d % time.Minute) / time.Second)
	return fmt.Sprintf("%d:%02d", m, s)
}
