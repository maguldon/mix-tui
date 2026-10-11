package tui

import (
	"strings"

	"github.com/maguldon/mix-tui/internal/engine"
)

// Minimum widths each layout renders meaningfully at (spec §6.1 column).
//
// These are the single source of truth: a layout refuses to draw below its own
// minimum rather than degrading into overlapping panels, so the advertised
// number and the enforced threshold can never disagree.
const (
	minWidthA = 100
	minWidthB = 70
	minWidthC = 80
)

// MinWidth returns the narrowest terminal a layout renders meaningfully at.
func MinWidth(k LayoutKind) int {
	switch k {
	case LayoutA:
		return minWidthA
	case LayoutB:
		return minWidthB
	case LayoutC:
		return minWidthC
	default:
		return minWidthD
	}
}

// Render dispatches to the layout named by st.Layout.
//
// Every branch checks its own minimum and renders the "too small" panel below
// it rather than drawing something broken: a user who maximises the window
// mid-set gets a usable layout back instead of a corrupted one (review focus
// #1), and a user who is simply too narrow is told what to do (#2).
func Render(w, h int, s engine.Snapshot, st ViewState) string {
	// Every layout is clamped to exactly h lines here, in one place. The layouts
	// compose panels whose heights add up to more than the terminal in short
	// windows, and each one clamped only its own pieces: at 200x2 layout A
	// emitted seven lines. A frame taller than the terminal makes a full-screen
	// app scroll and corrupt (review focus #1).
	out := clampHeight(renderOne(w, h, s, st), h)
	// Graceful degradation: if the chosen layout does not fit, fall back to the
	// most spacious one that does rather than refusing to draw.
	//
	// 80x24 is the standard terminal size and layout D needs 90 columns. Showing
	// a "too small" notice there means the common case gets no interface at
	// all, which is worse than showing a layout the user did not pick. The
	// notice is still shown when nothing fits.
	if strings.Contains(out, "too small") {
		for _, alt := range []LayoutKind{LayoutC, LayoutB, LayoutA} {
			try := st
			try.Layout = alt
			if cand := renderOne(w, h, s, try); !strings.Contains(cand, "too small") {
				return clampHeight(cand, h)
			}
		}
	}
	return out
}

// clampHeight trims or pads output to exactly h lines.
func clampHeight(s string, h int) string {
	if h <= 0 {
		return ""
	}
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > h {
		lines = lines[:h]
	}
	for len(lines) < h {
		lines = append(lines, "")
	}
	return strings.Join(lines, "\n")
}

func renderOne(w, h int, s engine.Snapshot, st ViewState) string {
	switch st.Layout {
	case LayoutA:
		return renderA(w, h, s, st)
	case LayoutB:
		return renderB(w, h, s, st)
	case LayoutC:
		return renderC(w, h, s, st)
	default:
		return renderD(w, h, s, st)
	}
}
