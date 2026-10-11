package tui

import (
	"github.com/maguldon/mix-tui/internal/engine"
	"github.com/maguldon/mix-tui/internal/tui/library"
	"github.com/maguldon/mix-tui/internal/tui/views"
)

// handleKey routes a key press.
//
// UI keys are checked BEFORE config lookups. Navigation has to work no matter
// what the user bound: if a config binding could shadow "esc" or "tab", a
// mistyped mapping would lock the user out of the screen they are on, with no
// way back and no error. The cost is that those keys cannot be rebound, which
// is the right trade.
func (m *Model) handleKey(k string) {
	name := bindingName(k)

	// The error screen's way out. Checked before anything else because the
	// interface is already broken here: routing q through the keymap would
	// quantise a deck, since q is bound to Quantize.
	if m.panicked {
		switch name {
		case "q", "ctrl+c":
			m.quitting = true
		}
		return
	}

	// Universal exits.
	switch name {
	case "escape":
		m.st.Mode = ModeMix
		return
	case "tab":
		m.moveFocus(1)
		return
	case "backtab":
		m.moveFocus(-1)
		return
	case "?":
		m.st.Mode = toggleMode(m.st.Mode, ModeHelp)
		return
	case "`":
		m.st.Mode = toggleMode(m.st.Mode, ModeDebug)
		return
	case "/":
		m.st.Mode = toggleMode(m.st.Mode, ModeLibrary)
		return
	case "l":
		// The quick-load overlay, reachable from the mix view. Without this the
		// overlay had no producer at all: a view nothing can open is dead code,
		// and the empty-deck art told the user to press exactly this key.
		if m.st.Mode == ModeMix {
			m.st.Mode = ModeOverlay
		}
		return
	}

	// Mode-relative keys. A key meaning different things in two modes is fine,
	// as long as the footer says which mode you are in.
	if m.st.Mode == ModeLibrary || m.st.Mode == ModeOverlay {
		// The browser gets the RAW key, not the binding name. bindingName maps
		// " " to "space", which is 5 runes, so the printable-rune test below
		// rejected it and the space fell through to the deck-play binding:
		// typing a space in the filter box started the focused deck instead.
		if m.handleBrowserKey(k) {
			return
		}
	}

	switch name {
	case "v":
		m.st.Layout = m.st.Layout.next()
		return
	case "z":
		m.st.Zoom = !m.st.Zoom
		return
	case "Z":
		m.st.ZoomBars = ZoomBarOptions[ZoomIndex(zoomIndexOf(m.st.ZoomBars))]
		return
	case "up":
		m.nudge(-1)
		return
	case "down":
		m.nudge(1)
		return
	}

	if m.keys == nil {
		return
	}
	a, ok := m.keys.Resolve(name)
	if !ok {
		return // unknown key: no state change, no action
	}
	m.applyAction(a)
}

// handleBrowserKey handles the browser's own keys. It reports whether it
// consumed the key.
//
// It takes the RAW key string. Callers must not pass bindingName(k) here:
// bindingName maps " " to "space", which is five runes, so the printable-rune
// test at the bottom rejected it and the space fell through to the deck-play
// binding. Typing a space in the filter box started the focused deck. The
// browser needs to see the character the user actually pressed.
func (m *Model) handleBrowserKey(name string) bool {
	switch name {
	case "up":
		if m.st.TrackSel > 0 {
			m.st.TrackSel--
		}
		return true
	case "down":
		// Clamp at the bottom. Running past the end left scrollOffset beyond the
		// list, which blanked every row: pressing down on a one-track library
		// showed no tracks at all.
		m.st.TrackSel = minInt(m.st.TrackSel+1, maxInt(0, m.trackCount()-1))
		return true
	case "left":
		m.st.Query = dropLastRune(m.st.Query)
		return true
	case "right":
		return true
	case "enter":
		// ENTER loads to the focused deck (spec 6.5). It consumed the key and
		// did nothing before, so the browser had no way to load anything.
		m.loadSelected()
		return true
	case "backspace":
		m.st.Query = dropLastRune(m.st.Query)
		return true
	case "s":
		// The sort menu replaces the detail pane; without a producer it was
		// unreachable in the running application even though it rendered.
		m.st.ShowSort = !m.st.ShowSort
		return true
	}
	// Any other single printable rune filters the list. The full-screen
	// browser previously had no way to narrow a track list at all.
	//
	// r[0] >= ' ' admits the space bar, which is what the user wants when they
	// type a multi-word query like "beat that". Control runes fall below ' '.
	if r := []rune(name); len(r) == 1 && r[0] >= ' ' {
		m.st.Query += name
		return true
	}
	return false
}

// loadSelected loads the highlighted track into the focused deck and returns to
// the decks so the user sees the result.
func (m *Model) loadSelected() {
	tr, ok := m.selectedTrack()
	if !ok {
		return
	}
	if err := m.LoadSelected(m.st.Focus, tr.Path); err != nil {
		return // the error is already recorded and auto-expires
	}
	m.st.Mode = ModeMix
	m.st.TrackSel = 0
	m.st.Query = ""
}

// selectedTrack resolves the cursor against the filtered library.
func (m *Model) selectedTrack() (library.Track, bool) {
	if m.lib == nil {
		return library.Track{}, false
	}
	return views.MatchTrack(m.lib.Tracks(), m.st.Query, m.st.TrackSel)
}

// trackCount is how many tracks the filtered library holds.
func (m *Model) trackCount() int {
	if m.lib == nil {
		return 0
	}
	return len(library.Search(m.lib.Tracks(), m.st.Query))
}

// dropLastRune removes one rune, not one byte. Deleting a byte from a
// multi-byte query leaves invalid UTF-8, which renders as U+FFFD at one column
// per byte.
func dropLastRune(s string) string {
	r := []rune(s)
	if len(r) == 0 {
		return ""
	}
	return string(r[:len(r)-1])
}

// applyAction applies a resolved Action, updating view state for the controls
// it mirrors.
func (m *Model) applyAction(a engine.Action) {
	switch a.Kind {
	case engine.Quit:
		// Handled here rather than in the engine: quitting is a property of
		// the program, not of the audio engine. The default keymap binds
		// ctrl+c to Quit, and until this existed the only way out of a live
		// session was SIGTERM -- ctrl+c did nothing at all.
		m.quitting = true
		return
	case engine.LibraryOpen:
		m.st.Mode = ModeLibrary
		return
	case engine.LibraryClose:
		m.st.Mode = ModeMix
		return
	case engine.LoopRoll:
		// Toggle rather than engage: there is no key-release to trigger the
		// PopRoll a momentary pad would send.
		if m.rollActive {
			a.Value = 0 // returns to whatever was playing
		}
		m.rollActive = !m.rollActive
		m.eng.Apply(a)
		return
	case engine.ZoomToggle:
		m.st.Zoom = !m.st.Zoom
	case engine.ViewNext:
		m.st.Layout = m.st.Layout.next()
	case engine.MixerCrossfader:
		m.st.Xfade = a.Value
	}
	m.eng.Apply(a)
}

// moveFocus cycles the focused deck, wrapping.
func (m *Model) moveFocus(delta int) {
	n := m.eng.DeckCount()
	if n <= 0 {
		return
	}
	m.st.Focus = ((m.st.Focus+delta)%n + n) % n
}

// nudge moves the crossfader by one step in the mix view.
//
// It applies the change to the engine. It previously only moved the mirrored
// view state, so pressing the key did nothing a DJ could hear and the fader sat
// visually wherever they last dragged it.
func (m *Model) nudge(delta int) {
	if m.st.Mode == ModeLibrary {
		return
	}
	step := 0.02
	next := clampFloat(m.st.Xfade+float64(delta)*step, -1, 1)
	m.st.Xfade = next
	m.eng.Apply(engine.Action{Kind: engine.MixerCrossfader, Value: next})
}

// toggleMode returns next if currently showing cur, else cur.
func toggleMode(cur, next Mode) Mode {
	if cur == next {
		return ModeMix
	}
	return next
}

// zoomIndexOf finds a bar count in the options, defaulting to the first.
func zoomIndexOf(bars int) int {
	for i, b := range ZoomBarOptions {
		if b == bars {
			return i
		}
	}
	return 0
}

// modeFooter describes the active mode's keys, so a key that means different
// things in two places is never a surprise (spec §6.5).
func modeFooter(m Mode) string {
	switch m {
	case ModeLibrary:
		return "↑↓ select   ⏎ load   / close   esc back"
	case ModeHelp:
		return "esc back"
	case ModeDebug:
		return "` close   esc back"
	case ModeOverlay:
		return "type to filter   ⏎ load   esc cancel"
	default:
		return "tab focus   v layout   z zoom   / library   ? help   ` debug"
	}
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
