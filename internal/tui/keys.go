package tui

import (
	"strings"

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
	if m.st.Mode == ModeLibrary {
		if m.handleLibraryKey(name) {
			return
		}
	}
	if m.st.Mode == ModeOverlay {
		if m.handleOverlayKey(name) {
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

// handleLibraryKey handles the browser's own keys. It reports whether it
// consumed the key.
func (m *Model) handleLibraryKey(name string) bool {
	switch name {
	case "up":
		if m.st.TrackSel > 0 {
			m.st.TrackSel--
		}
		return true
	case "down":
		m.st.TrackSel++
		return true
	case "left", "backspace":
		m.st.Query = dropLastRune(m.st.Query)
		return true
	case "right":
		return true
	case "enter":
		// ENTER loads to the focused deck (spec 6.5). It consumed the key and
		// did nothing before, so the browser had no way to load anything.
		m.loadSelected()
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

// handleOverlayKey handles the quick-load overlay: printable characters filter,
// enter loads into the focused deck.
func (m *Model) handleOverlayKey(name string) bool {
	switch name {
	case "enter":
		m.loadSelected()
		return true
	case "backspace":
		m.st.Query = dropLastRune(m.st.Query)
		return true
	}
	// Any other single printable rune is appended to the query.
	if r := []rune(name); len(r) == 1 && r[0] >= ' ' {
		m.st.Query += name
		return true
	}
	return false
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
	case engine.LibraryOpen:
		m.st.Mode = ModeLibrary
		return
	case engine.LibraryClose:
		m.st.Mode = ModeMix
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

// nudge moves a value by one step in the mix view.
func (m *Model) nudge(delta int) {
	if m.st.Mode == ModeLibrary {
		return
	}
	step := 0.02
	m.st.Xfade = clampFloat(m.st.Xfade+float64(delta)*step, -1, 1)
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

// keyDisplayName normalises a bubbletea key string for display.
func keyDisplayName(k string) string {
	if strings.TrimSpace(k) == "" {
		return ""
	}
	return k
}
