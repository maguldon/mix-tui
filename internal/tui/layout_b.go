package tui

import (
	"github.com/maguldon/mix-tui/internal/engine"
)

// renderB draws one deck filling the upper region with the other collapsed to a
// two-line strip at the bottom. st.Focus selects which deck is expanded
// (spec §6.1 layout B, ~70 cols).
func renderB(w, h int, s engine.Snapshot, st ViewState) string {
	if w <= 0 || h <= 0 {
		return ""
	}
	if w < minWidthB {
		return tooSmall(w, minWidthB, "focused")
	}

	stripH := 4 // the collapsed deck keeps a title row and a status row
	bodyH := h - stripH
	if bodyH < 4 {
		bodyH = 4
	}

	// Focus picks the big deck; the other becomes the strip. Clamp so an
	// out-of-range focus cannot index off the end of the snapshot.
	// Focus selects the expanded deck. It is clamped rather than defaulted:
	// forcing focus 0 to 1 (which this once did) meant TAB could never return
	// to the left deck, so the focused layout never actually followed focus.
	focus := st.Focus
	if len(s.Decks) == 0 {
		focus = 0
	} else if focus < 0 || focus >= len(s.Decks) {
		focus = 0
	}
	other := 0
	if focus == 0 && len(s.Decks) > 1 {
		other = 1
	}
	big := RenderPanel(renderDeckPanel(w, deckAt(&s, focus), st, true, bodyH-2), bodyH)

	strip := RenderPanel(renderDeckStrip(w, deckAt(&s, other)), stripH)

	return joinLines(append(big, strip...), w)
}
