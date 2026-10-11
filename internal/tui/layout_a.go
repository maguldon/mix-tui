package tui

import (
	"fmt"

	"github.com/maguldon/mix-tui/internal/engine"
)

// renderA draws deck A and deck B side by side with the mixer beside them, and
// the crossfader spanning a full-width footer (spec §6.1 layout A).
func renderA(w, h int, s engine.Snapshot, st ViewState) string {
	if w <= 0 || h <= 0 {
		return ""
	}
	if w < minWidthA {
		return tooSmall(w, minWidthA, "side-by-side")
	}

	gap := 1
	footerH := 3 // crossfader spans the footer
	bodyH := h - footerH
	if bodyH < 4 {
		bodyH = 4
	}

	mixerW := clampInt(w/5, 14, 24)
	deckW := clampInt((w-mixerW-3*gap)/2, 24, 52)
	if used := 2*deckW + mixerW + 3*gap; used < w {
		deckW += (w - used) / 2
	}

	left := RenderPanel(renderDeckPanel(deckW, deckAt(&s, 0), st, st.Focus == 0, bodyH-2), bodyH)
	right := RenderPanel(renderDeckPanel(deckW, deckAt(&s, 1), st, st.Focus == 1, bodyH-2), bodyH)
	mid := RenderPanel(renderMixerPanel(mixerW, s, st), bodyH)
	body := RenderColumns([][]string{left, right, mid}, gap, w)

	footer := RenderPanel(Panel{
		Title: "CROSSFADER",
		Width: w,
		Rows: []PanelRow{
			{Kind: RowMeter, Text: fmt.Sprintf("A %s|%s B", meter(st.Xfade, -1), meter(st.Xfade, 1))},
		},
	}, footerH)
	footer = padColumns(footer, w)

	return joinLines(append(body, footer...), w)
}

// meter renders a crossfader track with a position marker centred on zero.
//
// A DJ reads the marker position, not a filled bar, so the track stays uniform
// and only the marker moves. pos is the crossfader's -1..1 position; side is
// -1 for the A half and +1 for B.
func meter(pos, side float64) string {
	const width = 10
	centre := width / 2
	// Half-track for this side, 0..width/2.
	half := clampFloat(side, -1, 1) * float64(centre)
	if side < 0 {
		half = -half
	}
	pct := clampFloat((pos+1)/2.0, 0, 1)
	mark := int(pct * float64(width-1))

	b := make([]byte, width)
	for i := range b {
		b[i] = '.'
	}
	b[mark] = '|'
	_ = half
	return string(b)
}

// padColumns pads each already-rendered line to exactly w.
func padColumns(lines []string, w int) []string {
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = padRight(l, w)
	}
	return out
}
