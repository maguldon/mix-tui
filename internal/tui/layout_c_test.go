package tui

import (
	"strings"
	"testing"

	"github.com/maguldon/mix-tui/internal/engine"
)

// litBrailleCells counts lit braille glyphs, which is how a waveform or an
// overview strip shows up in the output.
func litBrailleCells(lines []string) int {
	n := 0
	for _, l := range lines {
		for _, r := range l {
			if r >= 0x2800 && r <= 0x28FF && r != 0x2800 {
				n++
			}
		}
	}
	return n
}

// TestLayoutCUsesTheWholeTerminal is the regression test for the stacked
// layout at the standard size.
//
// renderC split the available rows with (avail-2)/2, discarding the remainder.
// At 80x24 that produced 21 lines of 24 and left each deck six content rows --
// exactly the height at which deck_panel drops the waveform. The layout
// designed for an 80-column terminal therefore showed no waveform at all, and
// left three rows of the user's screen blank.
func TestLayoutCUsesTheWholeTerminal(t *testing.T) {
	for _, h := range []int{24, 26, 28, 30} {
		out := Render(80, h, snapTwoDecks(), ViewState{Layout: LayoutC})
		lines := splitLines(out)
		if len(lines) != h {
			t.Errorf("h=%d: rendered %d lines, want %d (the remainder is being discarded)",
				h, len(lines), h)
		}
	}
}

// TestLayoutCShowsAWaveformAtTheStandardSize is the other half: using the space
// has to actually produce a waveform. Deck panels share the height, so this
// checks both of them.
func TestLayoutCShowsAWaveformAtTheStandardSize(t *testing.T) {
	// Real peak data, so the waveform has something to draw.
	snap := snapWithPeaks()

	for _, h := range []int{24, 30} {
		out := Render(80, h, snap, ViewState{Layout: LayoutC})
		if got := litBrailleCells(splitLines(out)); got == 0 {
			t.Errorf("h=%d: the stacked layout drew no waveform at all\n%s", h, out)
		}
	}
}

// TestLayoutCIsRectangular guards the borders at the sizes where the odd-row
// split applies, since that is where an off-by-one would show.
func TestLayoutCIsRectangular(t *testing.T) {
	for _, h := range []int{23, 24, 25, 27, 31} {
		out := Render(80, h, snapTwoDecks(), ViewState{Layout: LayoutC})
		for i, line := range splitLines(out) {
			if n := len([]rune(line)); n != 80 {
				t.Errorf("h=%d line %d is %d runes wide, want 80", h, i, n)
			}
		}
	}
}

// TestLayoutCTransportRowSurvives. The point of the height arithmetic is that a
// deck keeps its PLAY/CUE indicator; that is the reason the waveform is the
// thing dropped when space runs out.
func TestLayoutCTransportRowSurvives(t *testing.T) {
	for _, h := range []int{24, 30, 40} {
		out := Render(80, h, snapTwoDecks(), ViewState{Layout: LayoutC})
		if !strings.Contains(out, "PLAY") && !strings.Contains(out, "CUE") {
			t.Errorf("h=%d: neither deck shows a transport indicator\n%s", h, out)
		}
	}
}

// snapWithPeaks is a two-deck snapshot carrying a real envelope, so a waveform
// has something to draw. The plain test snapshot has empty peaks, which would
// make "is a waveform drawn?" unanswerable.
func snapWithPeaks() engine.Snapshot {
	s := snapTwoDecks()
	var p engine.Peaks
	p.Min = make([]float32, 128)
	p.Max = make([]float32, 128)
	for i := range p.Max {
		p.Max[i] = 0.8
		p.Min[i] = -0.8
	}
	s.Decks[0].Peaks = p
	s.Decks[0].PeaksWidth = 64
	s.Decks[1].Peaks = p
	s.Decks[1].PeaksWidth = 64
	return s
}
