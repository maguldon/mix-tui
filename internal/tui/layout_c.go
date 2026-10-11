package tui

import "github.com/maguldon/mix-tui/internal/engine"

// renderC stacks deck A above deck B with the mixer in the footer
// (spec §6.1 layout C, ~80 cols).
func renderC(w, h int, s engine.Snapshot, st ViewState) string {
	if w <= 0 || h <= 0 {
		return ""
	}
	if w < minWidthC {
		return tooSmall(w, minWidthC, "stacked")
	}

	// The mixer footer needs 4 rows: the crossfader and master are what a DJ
	// reads at a glance in a stacked layout. It used to take 6, which at the
	// standard 80x24 left each deck six content rows -- enough for a waveform
	// but not for the transport row, so no deck showed PLAY or CUE at all.
	// Every row the footer takes is a row a deck loses.
	footerH := 5
	if h < 14 {
		// Below this there is not room for two decks and a mixer; the footer
		// goes first because a mixer-less layout still lets a DJ play.
		footerH = maxInt(0, h-8)
	}

	// deckH is the number of CONTENT rows a deck panel gets. The panel spends
	// two on its border, so a deck needs deckH+2 lines.
	//
	// deckRows is the number a panel actually needs to show its transport row;
	// below that the transport is clipped and a deck shows no PLAY/CUE
	// indicator at all, which is worse than a smaller waveform.
	const deckRows = 11
	avail := h - footerH
	panelH := (avail - 2) / 2 // two panels, one border row each
	content := panelH - 2
	if content < 5 {
		panelH = 7
		content = 5
	}
	if content > deckRows {
		content = deckRows
	}
	panelH = content + 2

	// Respect the height. The stacked layout composes three blocks and previously
	// concatenated them without clamping, so at h=2 it emitted seven lines: a
	// full-screen app given a frame taller than its terminal scrolls, and the
	// display corrupts (review focus #1).
	if panelH*2+footerH > h {
		footerH = maxInt(0, h-2*panelH)
	}
	if panelH*2+footerH > h {
		panelH = maxInt(0, (h-footerH)/2)
		content = maxInt(0, panelH-2)
	}

	top := RenderPanel(renderDeckPanel(w, deckAt(&s, 0), st, st.Focus == 0, content), panelH)
	bottom := RenderPanel(renderDeckPanel(w, deckAt(&s, 1), st, st.Focus == 1, content), panelH)
	// The real mixer panel, not the Task-2 placeholder: the placeholder shows
	// no levels, no crossfader position and no master, so layout C looked
	// broken next to A and D.
	footer := padColumns(RenderPanel(renderMixerPanel(w, s, st), footerH), w)

	return joinLines(append(append(top, bottom...), footer...), w)
}
