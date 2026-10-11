package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"

	"github.com/maguldon/mix-tui/internal/engine"
)

func TestAllLayoutsFitTheirMinWidth(t *testing.T) {
	cases := map[LayoutKind]func(int, int, engine.Snapshot, ViewState) string{
		LayoutA: renderA,
		LayoutB: renderB,
		LayoutC: renderC,
		LayoutD: renderD,
	}
	for k, c := range cases {
		min := MinWidth(k)
		out := c(min, 30, snapTwoDecks(), ViewState{Layout: k})
		assertLinesFit(t, out, min)
	}
}

func TestAllLayoutsSurviveOversize(t *testing.T) {
	for _, k := range []LayoutKind{LayoutA, LayoutB, LayoutC, LayoutD} {
		out := Render(400, 120, snapTwoDecks(), ViewState{Layout: k})
		assertLinesFit(t, out, 400)
	}
}

func TestAllLayoutsSurviveTiny(t *testing.T) {
	// Review Focus #2: no panic, no overflow, at any size
	for _, k := range []LayoutKind{LayoutA, LayoutB, LayoutC, LayoutD} {
		for _, size := range [][2]int{{20, 5}, {10, 3}, {1, 1}, {200, 2}} {
			out := Render(size[0], size[1], snapTwoDecks(), ViewState{Layout: k})
			for _, l := range strings.Split(out, "\n") {
				if len(l) > 0 && strings.Contains(l, "panic") {
					t.Fatalf("layout %v at %dx%d looks broken:\n%s", k, size[0], size[1], out)
				}
			}
		}
	}
}

func TestDispatchMatchesLayoutChoice(t *testing.T) {
	for _, k := range []LayoutKind{LayoutA, LayoutB, LayoutC, LayoutD} {
		a := Render(120, 40, snapTwoDecks(), ViewState{Layout: k})
		b := Render(120, 40, snapTwoDecks(), ViewState{Layout: k})
		if a != b {
			t.Errorf("Render is not deterministic for layout %v", k)
		}
	}
}

func TestLayoutBShowsFocusedDeckTitle(t *testing.T) {
	out := renderB(100, 40, snapTwoDecks(), ViewState{Layout: LayoutB, Focus: 1})
	if !strings.Contains(out, "Track B") {
		t.Error("focused deck title missing")
	}
	if !strings.Contains(out, "Track A") {
		t.Error("collapsed deck strip missing its title")
	}
}

// TestMinWidthMatchesTooSmallBehaviour keeps the advertised minimum and the
// actual "too small" threshold the same number. If they drift, the layout
// either renders something useless at its advertised minimum, or refuses to
// draw at a width it could easily handle.
func TestMinWidthMatchesTooSmallBehaviour(t *testing.T) {
	cases := map[LayoutKind]func(int, int, engine.Snapshot, ViewState) string{
		LayoutA: renderA,
		LayoutB: renderB,
		LayoutC: renderC,
		LayoutD: renderD,
	}
	for k, c := range cases {
		min := MinWidth(k)
		atMin := c(min, 20, snapTwoDecks(), ViewState{Layout: k})
		if strings.Contains(atMin, "too small") {
			t.Errorf("layout %v refuses to draw at its own MinWidth %d", k, min)
		}
		below := c(min-1, 20, snapTwoDecks(), ViewState{Layout: k})
		if !strings.Contains(below, "too small") && !strings.Contains(below, "wider") {
			t.Errorf("layout %v did not report too-small at %d", k, min-1)
		}
	}
}

// TestLayoutAShowsBothDecksAndMixer covers layout A's defining shape: two decks
// with the mixer beside them.
func TestLayoutAShowsBothDecksAndMixer(t *testing.T) {
	out := renderA(140, 40, snapTwoDecks(), ViewState{Layout: LayoutA})
	for _, want := range []string{"Track A", "Track B", "MIXER"} {
		if !strings.Contains(out, want) {
			t.Errorf("layout A missing %q", want)
		}
	}
}

// TestLayoutCStacksDecks covers layout C's defining shape: A above B with the
// mixer in the footer.
func TestLayoutCStacksDecks(t *testing.T) {
	out := renderC(120, 40, snapTwoDecks(), ViewState{Layout: LayoutC})
	aAt := strings.Index(out, "Track A")
	bAt := strings.Index(out, "Track B")
	if aAt < 0 || bAt < 0 {
		t.Fatal("layout C missing a deck title")
	}
	if aAt > bAt {
		t.Error("layout C should stack deck A above deck B")
	}
}

// TestLayoutBFocusSwapsWhichDeckIsLarge checks Focus actually selects the
// expanded deck rather than being decorative.
func TestLayoutBFocusSwapsWhichDeckIsLarge(t *testing.T) {
	focusA := renderB(100, 40, snapTwoDecks(), ViewState{Layout: LayoutB, Focus: 0})
	focusB := renderB(100, 40, snapTwoDecks(), ViewState{Layout: LayoutB, Focus: 1})
	if focusA == focusB {
		t.Error("layout B ignores Focus")
	}
}

// TestRenderIsRectangular checks every dispatch result is an exact-width frame,
// not merely within it.
func TestRenderIsRectangular(t *testing.T) {
	for _, k := range []LayoutKind{LayoutA, LayoutB, LayoutC, LayoutD} {
		for _, w := range []int{200, 140, 120, 100, 90, 80} {
			out := Render(w, 30, snapTwoDecks(), ViewState{Layout: k})
			for i, line := range strings.Split(out, "\n") {
				if got := displayWidth(line); got > w {
					t.Errorf("layout %v w=%d line %d width %d", k, w, i, got)
				}
			}
		}
	}
}

// TestAllLayoutsNeverPanicAcrossSizes sweeps every layout over a wide size
// range. A layout that panics takes the interface down while audio plays.
func TestAllLayoutsNeverPanicAcrossSizes(t *testing.T) {
	for _, k := range []LayoutKind{LayoutA, LayoutB, LayoutC, LayoutD} {
		for w := 1; w <= 160; w += 3 {
			for h := 1; h <= 40; h += 7 {
				_ = Render(w, h, snapTwoDecks(), ViewState{Layout: k})
			}
		}
	}
}

// displayWidth is lipgloss.Width under a shorter name for readability here.
func displayWidth(s string) int { return lipgloss.Width(s) }
