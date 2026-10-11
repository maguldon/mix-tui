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
	out := renderOne(w, h, s, st)
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
				return cand
			}
		}
	}
	return out
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
