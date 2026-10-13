package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/maguldon/mix-tui/internal/engine"
)

// minWidthD is the narrowest terminal that can show layout D's three columns.
// Below it we say so rather than drawing overlapping panels, which is what
// review focus #2 describes.
const minWidthD = 90

// maxDeckW is the widest a deck panel gets before the layout stops growing and
// centres itself instead. Past this a deck is mostly empty waveform, and a
// three-column block centred on a wide monitor reads better than one stretched
// across all of it.
const maxDeckW = 80

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
	avail := w - mixerW - 2*gap

	// Each deck gets half the space, and the odd column goes to the right deck.
	//
	// These two must be computed independently and must sum to exactly avail.
	// They used to share one deckW and then absorb the remainder into it:
	//
	//	used := 2*deckW + mixerW + 2*gap
	//	if used < w { deckW += w - used }
	//
	// which adds the remainder to BOTH decks, so the frame came out w+remainder
	// wide and joinLines truncated the overflow. The overflow is always the
	// right deck: at 175 columns deckW clamped to 52, used was 130, the
	// remainder 45 went to both decks, and deck B lost its right border and the
	// outer half of its waveform. The mixer was left at columns 99-122 of 175,
	// which is the opposite of the centring this layout documents.
	//
	// Equal deck widths keep the mixer centred. When avail is odd the two cannot
	// both be equal, so the mixer sits half a column off true centre -- which is
	// the best available and not worth a special case.
	leftW := clampInt(avail/2, 24, maxDeckW)
	rightW := clampInt(avail-leftW, 24, maxDeckW)

	// Anything the caps could not absorb is left over on a wide terminal. Centre
	// the block in it rather than stretching a deck past maxDeckW.
	margin := 0
	if spare := w - (leftW + mixerW + rightW + 2*gap); spare > 0 {
		margin = spare / 2
	}

	panelH := h
	left := RenderPanel(renderDeckPanel(leftW, deckAt(&s, 0), st, st.Focus == 0, panelH-2), panelH)
	mid := RenderPanel(renderMixerPanel(mixerW, s, st), panelH)
	right := RenderPanel(renderDeckPanel(rightW, deckAt(&s, 1), st, st.Focus == 1, panelH-2), panelH)

	block := RenderColumns([][]string{left, mid, right}, gap, leftW+mixerW+rightW+2*gap)
	// Guarantee exactly h rows, with the margin on both sides, so the frame is
	// an exact rectangle and a full-screen app does not repaint noise.
	for len(block) < h {
		block = append(block, spaces(leftW+mixerW+rightW+2*gap))
	}
	block = block[:h]
	if margin == 0 {
		return joinLines(block, w)
	}
	out := make([]string, h)
	for i, l := range block {
		out[i] = spaces(margin) + l + spaces(w-margin-len([]rune(l)))
	}
	return joinLines(out, w)
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

func titleOrDash(s string) string {
	if s == "" {
		return "--"
	}
	return s
}
