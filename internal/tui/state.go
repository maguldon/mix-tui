// Package tui is the terminal interface: four switchable layouts, waveforms,
// and the library browser.
//
// Every view here is a pure function from (width, height, Snapshot) to a
// string, with no terminal I/O and no engine pointer. That is what makes the UI
// testable: assertions run against rendered strings rather than a real terminal.
package tui

import (
	"time"

	"github.com/maguldon/mix-tui/internal/engine"
	"github.com/maguldon/mix-tui/internal/tui/library"
	"github.com/maguldon/mix-tui/internal/tui/views"
)

// Mode is the top-level view.
type Mode int

const (
	ModeMix Mode = iota
	ModeLibrary
	ModeOverlay
	ModeHelp
	ModeDebug
	ModeSettings
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
	// Zoom is each deck's zoom toggle, and ZoomBars the bar count
	// (2/4/8/16/32).
	//
	// It is per-deck rather than a single flag following focus, because spec 6.3
	// says zoom is "a per-deck toggle available in every layout". With one flag,
	// pressing TAB moved the zoom to the other deck and the two could never both
	// be zoomed -- which is the state the beatmatching view is built around.
	Zoom     []bool
	ZoomBars int
	// ZoomLength is the window duration both zoomed decks share, so a column
	// means the same offset from the playhead on each. It is recomputed from the
	// focused deck's tempo whenever zoom is toggled or the bar count changes.
	ZoomLength time.Duration
	// Xfade mirrors the crossfader for display. The engine Snapshot remains
	// authoritative; this is only what the user last asked for.
	Xfade float64
	Cued  int
	// OtherDeck is the non-focused deck's state, carried so the focused panel
	// can render its dBEAT readout against it. It is a value copy, not a
	// pointer into engine state.
	OtherDeck engine.DeckState
	// TrackSel is the library browser's cursor. Query filters it. They live in
	// ViewState so leaving the browser and returning restores where the user was,
	// which is what a DJ expects when they press escape to check something.
	TrackSel int
	Query    string
	// QueryCursor is the filter field's caret, in runes from the start of
	// Query.
	//
	// It exists because LEFT and RIGHT had nowhere to go: LEFT deleted the last
	// character, which in the full browser -- where the filter is not drawn --
	// destroyed something the user could neither see nor have asked to lose. The
	// caret is always clamped into the field and is never set by arithmetic done
	// at the call site; see textfield.go.
	QueryCursor int
	// ShowSort opens the sort menu over the detail pane.
	ShowSort bool
	// SortBy and SortDesc drive the track list order. They were never set
	// from a keypress, so the sort was permanently title-ascending.
	SortBy   library.SortKey
	SortDesc bool
	// Sorted records that the user chose a sort. Without it, a selected crate
	// could not show its own running order, because the title-ascending
	// default overwrote it the moment the crate was picked.
	Sorted bool
	// SortSel is the highlighted entry of the sort menu.
	SortSel int
	// LibPane is the focused pane in the library: 0 crates, 1 tracks.
	// TAB cycles it (spec 6.5). It used to fall through to the global TAB,
	// which moved DECK focus mid-browse, so the deck ENTER would load into
	// changed under the user.
	LibPane views.Pane
	// CrateSel is the highlighted crate.
	CrateSel int
	// LastErr and LastErrAt drive the auto-expiring error line.
	LastErr   string
	LastErrAt time.Time
	// SettingInput is the editable library path in the settings view. It is
	// seeded from config on entry rather than carried across openings, so the
	// field always shows what is actually in effect.
	SettingInput string
	// SettingCursor is the caret in that field, in runes. It opens at the end of
	// the seeded path, so LEFT reaches the part most likely to be wrong without
	// backspacing the whole thing away.
	SettingCursor int
	// SettingNote is the last settings result: what was saved, or what a scan
	// found. It is the only feedback a save produces, and a save is otherwise
	// invisible.
	SettingNote string
	// AudioDown is true when no device is running, and AudioReason why. It is
	// sampled from the binary every tick rather than pushed, so a device that
	// appears in the background clears the banner without the model being told.
	AudioDown   bool
	AudioReason string
	// ScanBusy is true between asking for a rescan and the scan returning. It
	// exists so the view can say what is happening: a scan of a real collection
	// runs for seconds, and without it the interface looks identical whether
	// the key worked or not.
	ScanBusy bool
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

// deckZoomed reports whether deck i has zoom on.
func (v ViewState) deckZoomed(i int) bool {
	if i < 0 || i >= len(v.Zoom) {
		return false
	}
	return v.Zoom[i]
}

// setDeckZoom turns a deck's zoom on or off and returns the new state.
func (v *ViewState) setDeckZoom(i int) bool {
	if i < 0 || i >= len(v.Zoom) {
		return false
	}
	v.Zoom[i] = !v.Zoom[i]
	return v.Zoom[i]
}

// anyZoomed reports whether any deck is zoomed, which is what the shared window
// length is computed for.
func (v ViewState) anyZoomed() bool {
	for _, z := range v.Zoom {
		if z {
			return true
		}
	}
	return false
}

// zoomWindow returns the window for a deck and whether it should be zoomed at
// all.
//
// A deck with no usable beatgrid stays on the full-track view: the window length
// comes from tempo, so a deck without one has no scale to share and showing it
// zoomed would put it on a different time scale from its neighbour -- worse than
// not zooming it at all, because the comparison the view exists for would then
// be quietly wrong.
func (v ViewState) zoomWindow(d engine.DeckState) (ZoomWindow, bool) {
	if !v.anyZoomed() || !v.deckZoomed(d.Index) || !d.HasBeatGrid {
		return ZoomWindow{}, false
	}
	length := v.ZoomLength
	if length <= 0 {
		length = ZoomLength(v.ZoomBars, d.BPM)
	}
	return WindowAt(d.Position, length), true
}

// retargetZoom recomputes the shared window length from the focused deck.
//
// The reference deck is the focused one so that pressing z then tab keeps the
// same scale; the alternative, deriving it per deck, is the bug this replaced.
func (m *Model) retargetZoom() {
	snap := m.eng.Snapshot()
	if m.st.Focus < 0 || m.st.Focus >= len(snap.Decks) {
		return
	}
	d := snap.Decks[m.st.Focus]
	bpm := d.BPM
	if d.HasBeatGrid {
		bpm = d.BPM
	}
	m.st.ZoomLength = ZoomLength(m.st.ZoomBars, bpm)
}

// zoomSpan is how much track time a deck's zoomed window covers.
func (v ViewState) zoomSpan(d engine.DeckState) time.Duration {
	length := v.ZoomLength
	if length <= 0 {
		length = ZoomLength(v.ZoomBars, d.BPM)
	}
	return length
}
