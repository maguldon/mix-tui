package tui

import (
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
