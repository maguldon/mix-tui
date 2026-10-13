package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/maguldon/mix-tui/internal/engine"
)

// layoutDSnapshot is two loaded decks, so both panels have content to clip.
func layoutDSnapshot() engine.Snapshot {
	return engine.Snapshot{Decks: []engine.DeckState{
		{Index: 0, Loaded: true, Playing: true, Title: "DECK A", Duration: 3 * time.Minute},
		{Index: 1, Loaded: true, Playing: true, Title: "DECK B", Duration: 3 * time.Minute},
	}}
}

// TestLayoutDNeverClipsDeckB is the regression test for the reported bug.
//
// The three columns shared one deckW and then absorbed the width remainder
// into it, which adds the remainder to BOTH decks: the frame came out
// w+remainder wide and joinLines truncated the overflow. The overflow always
// falls on the right deck. At 175 columns that cost deck B its closing border
// and the outer half of its waveform, and left the mixer off to the left of a
// terminal it was supposed to be centred in.
func TestLayoutDNeverClipsDeckB(t *testing.T) {
	for w := minWidthD; w <= 300; w++ {
		out := renderD(w, 20, layoutDSnapshot(), ViewState{Layout: LayoutD})
		lines := strings.Split(out, "\n")
		if len(lines) != 20 {
			t.Fatalf("w=%d: %d lines, want 20", w, len(lines))
		}
		for i, l := range lines {
			if got := len([]rune(l)); got != w {
				t.Fatalf("w=%d: line %d is %d columns, want %d", w, i, got, w)
			}
		}
		// Three panels means three openings and three closings on the top row.
		// A clipped deck B loses both of its right-hand ones.
		if got := strings.Count(lines[0], "┌"); got != 3 {
			t.Errorf("w=%d: top row has %d panel openings, want 3 (a panel is clipped)", w, got)
		}
		if got := strings.Count(lines[0], "┐"); got != 3 {
			t.Errorf("w=%d: top row has %d panel closings, want 3 (a panel is clipped)", w, got)
		}
		if got := strings.Count(lines[19], "┘"); got != 3 {
			t.Errorf("w=%d: bottom row has %d panel closings, want 3 (a panel is clipped)", w, got)
		}
	}
}

// TestLayoutDCentresTheMixer checks the promise in renderD's own comment: the
// mixer is centred, because that is where it sits on an Xone K2.
func TestLayoutDCentresTheMixer(t *testing.T) {
	for w := minWidthD; w <= 300; w++ {
		out := renderD(w, 20, layoutDSnapshot(), ViewState{Layout: LayoutD})
		top := []rune(strings.Split(out, "\n")[0])
		start := -1
		for i := 0; i+7 <= len(top); i++ {
			if string(top[i:i+7]) == "┌─MIXER" {
				start = i
				break
			}
		}
		if start < 0 {
			t.Fatalf("w=%d: the mixer header is missing from the frame", w)
		}
		mixerW := clampInt(w/5, 14, 24)
		left := start
		right := len(top) - (start + mixerW)

		// Perfect symmetry is impossible when the deck space is odd: the two
		// deck widths cannot both be equal. Half a column either way is the
		// whole of it.
		if d := left - right; d > 1 || d < -1 {
			t.Errorf("w=%d: mixer is %d columns from the left and %d from the right", w, left, right)
		}
	}
}

// TestLayoutDGivesBothDecksRoom guards the cap: past it the layout centres
// rather than stretching a deck into a mostly-empty panel, but never so far
// that a deck is squeezed below a usable width.
func TestLayoutDGivesBothDecksRoom(t *testing.T) {
	for _, w := range []int{90, 120, 175, 200, 300} {
		out := renderD(w, 20, layoutDSnapshot(), ViewState{Layout: LayoutD})
		top := []rune(strings.Split(out, "\n")[0])
		// A centred block leaves margin on the left, so the panel width is the
		// span up to the first corner, not the offset of it.
		open, close := -1, -1
		for i, r := range top {
			if r == '┌' {
				open = i
				break
			}
		}
		if open < 0 {
			t.Fatalf("w=%d: no deck panel found", w)
		}
		for i := open; i < len(top); i++ {
			if top[i] == '┐' {
				close = i
				break
			}
		}
		if close < 0 {
			t.Fatalf("w=%d: deck panel has no closing corner", w)
		}
		deckW := close + 1 - open
		if deckW < 24 {
			t.Errorf("w=%d: deck panel is %d columns, below the 24 minimum", w, deckW)
		}
		if deckW > maxDeckW {
			t.Errorf("w=%d: deck panel is %d columns, above the %d cap", w, deckW, maxDeckW)
		}
	}
}

// TestLayoutDSurvivesTinyHeights keeps the centred block from indexing past the
// frame when the terminal is short.
func TestLayoutDSurvivesTinyHeights(t *testing.T) {
	for w := minWidthD; w <= 200; w += 7 {
		for h := 1; h <= 8; h++ {
			out := renderD(w, h, layoutDSnapshot(), ViewState{Layout: LayoutD})
			if got := len(strings.Split(out, "\n")); got != h {
				t.Fatalf("w=%d h=%d: %d lines, want %d", w, h, got, h)
			}
		}
	}
}
