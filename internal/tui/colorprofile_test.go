package tui

import (
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

// forceColour makes lipgloss emit escape sequences for the duration of a test.
//
// Without it the default renderer detects a non-TTY and degrades to the Ascii
// profile, so every style renders as plain text and any assertion about colour
// passes or fails for reasons that have nothing to do with the code. This is
// why the waveform's missing colour went unnoticed: in a terminal it is
// absent, and headless the renderer hides even the attempt.
func forceColour(t *testing.T) {
	t.Helper()
	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(prev) })
}
