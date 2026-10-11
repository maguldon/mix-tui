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
	focus := st.Focus
	if focus < 0 || (focus == 0 && len(s.Decks) > 1) {
		focus = 1
	}
	if len(s.Decks) == 1 {
		focus = 0
	}

	// Label each panel with the deck it actually shows. Labelling both "A" is
	// the kind of small wrongness that makes a user distrust every other label
	// on screen.
	other := 1 - focus
	big := RenderPanel(deckPanel(string(rune('A'+focus)), w, deckAt(&s, focus), st, focus), bodyH)

	strip := RenderPanel(Panel{
		Title: string(rune('A' + other)),
		Width: w,
		Rows: []PanelRow{
			{Kind: RowText, Text: titleOrDash(deckAt(&s, other).Title), Style: titleStyle},
			{Kind: RowText, Text: transportLine(deckAt(&s, other))},
		},
	}, stripH)

	return joinLines(append(big, strip...), w)
}
