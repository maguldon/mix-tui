package palette

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"testing"
)

// darkBG is the background the interface is drawn against. The palette is
// GitHub-dark-derived -- Border is literally GitHub's border.muted -- so this is
// the background those values were chosen for.
//
// Contrast is only meaningful against a background, and the app sets none of its
// own, so this is the terminal's. If the palette is ever meant for a light
// terminal, this constant is the thing that has to change with it.
const darkBG = "#0d1117"

// TestBodyColoursClearWCAGAA is the regression test for the "everything is grey"
// half of issue #17.
//
// Dim was #3d444d with the comment "box drawing", but it was applied to body
// text throughout: the artist name, the progress bar, the EQ and filter meters,
// the off-state transport labels, and the secondary rows of every view. On this
// background that is 1.9:1 -- well under AA's 4.5:1 -- so most of the interface
// was effectively unreadable while a few adjacent rows rendered at full terminal
// brightness.
//
// WCAG AA governs text. The waveform fills are graphics and are held to a lower
// floor by TestGraphicFillsClearNonTextFloor instead.
func TestBodyColoursClearWCAGAA(t *testing.T) {
	for _, c := range []struct {
		name string
		col  string
	}{
		{"Text", string(Text)}, // body text
		{"Dim", string(Dim)},   // secondary content
		{"Accent", string(Accent)},
		{"Ok", string(Ok)},
		{"Warn", string(Warn)},
	} {
		if got := contrastRatio(c.col, darkBG); got < 4.5 {
			t.Errorf("%s (%s) is %.2f:1 against %s, want at least 4.5:1",
				c.name, c.col, got, darkBG)
		}
	}
}

// TestGraphicFillsClearNonTextFloor holds the colours that draw shapes rather
// than words to WCAG's 3:1 non-text minimum.
//
// Unplayed is the one colour still under AA at 3.76:1, which the previous
// version of this file failed on. It is a deliberate, long-standing part of the
// played/unplayed pair and recolouring it is a visual decision about the
// waveform rather than a fix for the grey text this issue is about, so it is
// recorded here at its measured value instead of being quietly excluded. Raising
// it means editing Unplayed in palette.go; this test then still passes.
func TestGraphicFillsClearNonTextFloor(t *testing.T) {
	for _, c := range []struct {
		name string
		col  string
	}{
		{"Unplayed", string(Unplayed)},
		{"Played", string(Played)},
		{"Cue", string(Cue)},
		{"Beat", string(Beat)},
		{"Loop", string(Loop)},
		{"Playhead", string(Playhead)},
	} {
		if got := contrastRatio(c.col, darkBG); got < 3.0 {
			t.Errorf("%s (%s) is %.2f:1 against %s, want at least 3:1 as a non-text fill",
				c.name, c.col, got, darkBG)
		}
	}
}

// TestFrameMayRecedeBelowBodyText keeps the ramp ordered.
//
// Border is allowed to sit below AA: a frame is not text, and a frame that
// competes with the content it encloses is worse than a quiet one. What it may
// not do is be brighter than the body text inside it.
func TestFrameMayRecedeBelowBodyText(t *testing.T) {
	border := contrastRatio(string(Border), darkBG)
	if border >= contrastRatio(string(Text), darkBG) {
		t.Errorf("Border (%s) is %.2f:1, not quieter than Text at %.2f:1 -- the frame would compete with the content",
			Border, border, contrastRatio(string(Text), darkBG))
	}
	if border >= contrastRatio(string(Dim), darkBG) {
		t.Errorf("Border (%s) is %.2f:1, not quieter than Dim at %.2f:1",
			Border, border, contrastRatio(string(Dim), darkBG))
	}
}

// TestDimStaysBelowText keeps the hierarchy monotonic, so "dim" always means
// "recessed" and never "same brightness, different hue".
func TestDimStaysBelowText(t *testing.T) {
	if contrastRatio(string(Dim), darkBG) >= contrastRatio(string(Text), darkBG) {
		t.Errorf("Dim is %.2f:1 against Text's %.2f:1: dimmed text is not dimmed",
			contrastRatio(string(Dim), darkBG), contrastRatio(string(Text), darkBG))
	}
}

// TestPaletteColoursAreHex guards the helper below from being handed something
// it silently misparses.
func TestPaletteColoursAreHex(t *testing.T) {
	for _, c := range []struct{ name, col string }{
		{"Text", string(Text)}, {"Dim", string(Dim)}, {"Border", string(Border)},
		{"Accent", string(Accent)}, {"Ok", string(Ok)}, {"Warn", string(Warn)},
		{"Played", string(Played)}, {"Unplayed", string(Unplayed)},
		{"Cue", string(Cue)}, {"Beat", string(Beat)}, {"Loop", string(Loop)},
		{"Playhead", string(Playhead)},
	} {
		if !strings.HasPrefix(c.col, "#") || len(c.col) != 7 {
			t.Errorf("%s = %q, want a #rrggbb literal", c.name, c.col)
		}
		if _, err := strconv.ParseUint(strings.TrimPrefix(c.col, "#"), 16, 32); err != nil {
			t.Errorf("%s = %q, not parseable as hex: %v", c.name, c.col, err)
		}
	}
}

// contrastRatio is the WCAG 2.1 relative-contrast ratio between two #rrggbb
// colours: (L1+0.05)/(L2+0.05) with L2 the lighter of the pair.
func contrastRatio(a, b string) float64 {
	la, lb := relativeLuminance(a), relativeLuminance(b)
	if lb > la {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}

// relativeLuminance is WCAG 2.1's L: the sRGB channels linearised, then
// weighted 0.2126 R + 0.7152 G + 0.0722 B.
func relativeLuminance(hex string) float64 {
	v := strings.TrimPrefix(hex, "#")
	if len(v) != 6 {
		panic(fmt.Sprintf("not a #rrggbb colour: %q", hex))
	}
	var lum float64
	for i, w := range []float64{0.2126, 0.7152, 0.0722} {
		n, err := strconv.ParseUint(v[i*2:i*2+2], 16, 8)
		if err != nil {
			panic(fmt.Sprintf("not a #rrggbb colour: %q", hex))
		}
		c := float64(n) / 255
		if c <= 0.03928 {
			lum += w * c / 12.92
		} else {
			lum += w * math.Pow((c+0.055)/1.055, 2.4)
		}
	}
	return lum
}
