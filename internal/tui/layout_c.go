package tui

import (
	"github.com/maguldon/mix-tui/internal/engine"
)

// renderC stacks deck A above deck B with the mixer in the footer
// (spec §6.1 layout C, ~80 cols).
func renderC(w, h int, s engine.Snapshot, st ViewState) string {
	if w <= 0 || h <= 0 {
		return ""
	}
	if w < minWidthC {
		return tooSmall(w, minWidthC, "stacked")
	}

	footerH := 6 // mixer needs more vertical room than a single footer line
	// Split what is left between the two decks evenly; each needs at least 5
	// rows to show a title, transport, and waveform.
	rest := h - footerH
	half := rest / 2
	if half < 5 {
		half = 5
	}
	topH := half
	bottomH := rest - half
	if bottomH < 5 {
		bottomH = 5
		topH = maxInt(5, rest-bottomH)
	}

	top := RenderPanel(deckPanel("A", w, deckAt(&s, 0), st, 0), topH)
	bottom := RenderPanel(deckPanel("B", w, deckAt(&s, 1), st, 1), bottomH)
	footer := RenderPanel(mixerPanel(w), footerH)
	footer = padColumns(footer, w)

	return joinLines(append(append(top, bottom...), footer...), w)
}
