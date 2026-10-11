package tui

import (
	"testing"

	"github.com/charmbracelet/lipgloss"
)

func TestRenderBoundedAcrossManySizes(t *testing.T) {
	m, _ := testModel(t)
	for w := 1; w <= 60; w++ {
		for h := 1; h <= 20; h++ {
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
