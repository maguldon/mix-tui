// Package tui is the terminal interface: four switchable layouts, waveforms,
// and the library browser.
//
// Every view here is a pure function from (width, height, Snapshot) to a
// string, with no terminal I/O and no engine pointer. That is what makes the UI
// testable: assertions run against rendered strings rather than a real terminal.
package tui

import "time"

// Mode is the top-level view.
type Mode int

const (
	ModeMix Mode = iota
	ModeLibrary
	ModeOverlay
	ModeHelp
)

// LayoutKind selects one of the four layouts (spec §6.1). D is the default: the
// mixer centred, closest to the Xone K2's physical layout.
type LayoutKind int

const (
	LayoutD LayoutKind = iota
	LayoutA
	LayoutB
	LayoutC
)

// errTTL is how long an error stays on screen. An error that never expires
// leaves stale text on the display long after it stopped being true, which at
// 2am reads as a fault that is still happening.
const errTTL = 4 * time.Second

// ViewState is interaction state only. It never holds audio data: the engine
// Snapshot is the single source of truth for anything audible.
type ViewState struct {
	Mode   Mode
	Layout LayoutKind
	// Focus is the focused deck index.
	Focus int
	// Zoom is the focused deck's zoom toggle, and ZoomBars its bar count
	// (2/4/8/16/32).
	Zoom     bool
	ZoomBars int
	// Xfade mirrors the crossfader for display. The engine Snapshot remains
	// authoritative; this is only what the user last asked for.
	Xfade float64
	Cued  int
	// LastErr and LastErrAt drive the auto-expiring error line.
	LastErr   string
	LastErrAt time.Time
}

// nextLayout cycles A->B->C->D->A.
func (l LayoutKind) next() LayoutKind {
	return LayoutKind((int(l) + 1) % 4)
}

// layoutFromName maps the config spelling to a LayoutKind, defaulting to D.
func layoutFromName(s string) LayoutKind {
	switch s {
	case "A":
		return LayoutA
	case "B":
		return LayoutB
	case "C":
		return LayoutC
	default:
		return LayoutD
	}
}
