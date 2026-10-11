package tui

import (
	"testing"

	"github.com/charmbracelet/lipgloss"
)

func TestRenderBoundedAcrossManySizes(t *testing.T) {
	m, _ := testModel(t)
	// The range must actually exercise the layouts. The previous sweep stopped
	// at 60 columns, below layout D's 90-column floor, so not one of the 1200
	// renders drew a panel, a waveform or a deck: 817 hit the too-small notice
	// and the rest were too short to render anything. A bounding test that
	// never reaches the code it claims to bound is decoration.
	for w := 1; w <= 160; w++ {
		for h := 1; h <= 40; h++ {
			out := m.RenderAt(w, h)
			lines := splitLines(out)
			if len(lines) > h {
				t.Fatalf("%dx%d -> %d lines", w, h, len(lines))
			}
			for i, l := range lines {
				if got := lipgloss.Width(l); got > w {
					t.Fatalf("%dx%d line %d width %d", w, h, i, got)
				}
			}
		}
	}
}
