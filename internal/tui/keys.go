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

	// Mode-relative keys run BEFORE the universal exits. TAB and SHIFT+ENTER
	// mean something different inside the browser (cycle panes, load to the
	// other deck) and must reach it first -- handled after this, TAB changed
	// which DECK was focused while the user was browsing, which is the moment a
	// mistake is most expensive.
	if m.st.Mode == ModeLibrary || m.st.Mode == ModeOverlay {
		if m.handleBrowserKey(k) {
			return
		}
	}

	// Universal exits.
	switch name {
	case "escape":
		// Spec 6.5: sort state resets when the view is left. It did not, so
		// re-entering the library opened with the sort menu still covering the
		// detail pane and the previous sort silently applied.
		if m.st.Mode == ModeLibrary {
			m.st.ShowSort = false
			m.st.SortBy = library.SortTitle
			m.st.SortDesc = false
			m.st.SortSel = 0
			m.st.LibPane = views.PaneTracks
		}
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
		// Focus the track list on entry, for the same reason LibraryOpen does.
		m.st.LibPane = views.PaneTracks
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
	// With the sort menu open, up/down/enter drive the menu rather than the
	// track list. The menu replaced the detail pane, so a user choosing a sort
	// needs the selection keys to reach it -- otherwise "s" opens a menu with no
	// way to pick anything, which is what happened before.
	if m.st.ShowSort {
		switch name {
		case "up":
			m.st.SortSel = maxInt(0, m.st.SortSel-1)
			return true
		case "down":
			m.st.SortSel = minInt(m.st.SortSel+1, len(views.SortKeys)-1)
			return true
		case "enter":
			m.st.SortBy = views.SortKeys[m.st.SortSel]
			m.st.ShowSort = false
			return true
		case "s", "escape":
			m.st.ShowSort = false
			return true
		case "d":
			m.st.SortDesc = !m.st.SortDesc
			return true
		}
	}

	switch name {
	case "up":
		if m.st.LibPane == views.PaneCrates {
			m.st.CrateSel = maxInt(0, m.st.CrateSel-1)
			return true
		}
		m.st.TrackSel = maxInt(0, m.st.TrackSel-1)
		return true
	case "down":
		// Clamp at the bottom. Running past the end left scrollOffset beyond the
		// list, which blanked every row: pressing down on a one-track library
		// showed no tracks at all.
		if m.st.LibPane == views.PaneCrates {
			m.st.CrateSel = minInt(m.st.CrateSel+1, maxInt(0, m.crateCount()-1))
			return true
		}
		m.st.TrackSel = minInt(m.st.TrackSel+1, maxInt(0, m.trackCount()-1))
		return true
	case "tab":
		// Spec 6.5: TAB cycles pane focus inside the library. It previously fell
		// through to the global TAB, which moved DECK focus -- so browsing and
		// pressing tab silently changed which deck ENTER would load into.
		m.st.LibPane = (m.st.LibPane + 1) % views.PaneCount
		return true
	case "left":
		m.st.Query = dropLastRune(m.st.Query)
		return true
	case "right":
		return true
	case "shift+enter":
		// Spec 6.5: SHIFT+ENTER loads to the OTHER deck. It was absent
		// entirely, so every load went to the focused deck.
		m.loadSelectedInto(1 - m.st.Focus)
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
		m.st.SortSel = int(m.st.SortBy)
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
func (m *Model) loadSelected() { m.loadSelectedInto(m.st.Focus) }

// loadSelectedInto loads the highlighted track into a specific deck and
// returns to the decks so the user sees the result.
//
// SHIFT+ENTER targets the other deck (spec 6.5). With two decks that is the
// whole of "load to the other deck"; with N it is the next one round, which is
// what the two-deck MVP needs and does not pretend to be more.
func (m *Model) loadSelectedInto(deck int) {
	tr, ok := m.selectedTrack()
	if !ok {
		return
	}
	if n := m.eng.DeckCount(); n > 0 {
		deck = ((deck % n) + n) % n
	}
	if err := m.LoadSelected(deck, tr.Path); err != nil {
		return // the error is already recorded and auto-expires
	}
	// Focus follows the load: the deck that just changed is the one the user
	// is now looking at.
	m.st.Focus = deck
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
// crateCount reports how many rows the crates pane offers: "All tracks" plus
// the scan summary. There is no crate store on the Model, so the pane cannot
// offer a real crate picker yet; it at least navigates what it renders rather
// than running off the end.
func (m *Model) crateCount() int { return 2 }

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
		// Focus the track list on entry. The crates pane is a picker the user
		// comes to deliberately; entering the browser is about the tracks, and
		// starting on the crates pane meant the first arrow key moved a crate
		// cursor rather than the one the user could see.
		m.st.LibPane = views.PaneTracks
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
		return "↑↓ select   ⏎ load deck   shift+⏎ other deck   tab pane   s sort   esc back"
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
