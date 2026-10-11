package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// Art is embedded data, never generated at runtime (global constraint). Each
// variant is a fixed-width string constant; RenderArt selects the widest that
// fits and pads with spaces. Padding the art itself would corrupt it under
// variable-width glyphs, so the art is never reflowed — only clipped or
// centred.
var art = map[string][]string{
	// The empty-deck prompt. Deliberately small so it fits a narrow deck panel.
	"empty": {
		"  .--------.  ",
		"  |  EMPTY  |  ",
		"|   DECK   |  ",
		"  `--------'  ",
		"  press L    ",
	},
	// A short accent shown under a playing deck.
	"accent": {
		">>>",
	},
	"splash": {
		"  __  __ _   _ ___   ___ ___ __  __ ",
		" |  \\/  | | | | _ \\ / __|_ _|  \\/  |",
		" | |\\/| | |_| |   / | (_ | || |\\/| |",
		" |_|  |_|\\___/|_|_\\ \\___||___|  |_|",
	},
}

// artOrder is the preferred variant order per name, widest first. RenderArt
// picks the widest that fits rather than scaling, since scaling ASCII art by
// string operations breaks it (global constraint).
var artOrder = map[string][]string{
	"empty":   {"empty"},
	"accent":  {"accent"},
	"splash":  {"splash"},
	"unknown": {"empty"},
}

// RenderArt returns the named art fitted to w columns and h rows, padded with
// spaces. An unknown name falls back to the empty-deck art rather than
// returning nothing, so a typo shows something rather than a blank panel.
func RenderArt(w, h int, name string) []string {
	if w <= 0 || h <= 0 {
		return nil
	}
	variants := artOrder[name]
	if len(variants) == 0 {
		variants = artOrder["empty"]
	}
	var chosen []string
	for _, v := range variants {
		lines := art[v]
		if widestLine(lines) <= w {
			chosen = lines
			break
		}
	}
	if chosen == nil {
		// Nothing fits; return a blank block of the right size so the caller
		// still reserves the space.
		chosen = []string{""}
	}

	out := make([]string, h)
	for i := 0; i < h; i++ {
		if i < len(chosen) {
			out[i] = centre(chosen[i], w)
		} else {
			out[i] = spaces(w)
		}
	}
	return out
}

func widestLine(lines []string) int {
	w := 0
	for _, l := range lines {
		if lw := lipgloss.Width(l); lw > w {
			w = lw
		}
	}
	return w
}

// centre pads a line to exactly w with the remainder split evenly.
func centre(s string, w int) string {
	s = truncatePlain(s, w)
	gap := w - lipgloss.Width(s)
	if gap <= 0 {
		return s
	}
	left := gap / 2
	return strings.Repeat(" ", left) + s + strings.Repeat(" ", gap-left)
}
