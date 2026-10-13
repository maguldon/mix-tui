package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/maguldon/mix-tui/internal/config"
	"github.com/maguldon/mix-tui/internal/engine"
	"github.com/maguldon/mix-tui/internal/tui/library"
	"github.com/maguldon/mix-tui/internal/tui/views"
)

// handleKey routes a key press and returns a command to run, if any.
//
// UI keys are checked BEFORE config lookups. Navigation has to work no matter
// what the user bound: if a config binding could shadow "esc" or "tab", a
// mistyped mapping would lock the user out of the screen they are on, with no
// way back and no error. The cost is that those keys cannot be rebound, which
// is the right trade.
func (m *Model) handleKey(k string) tea.Cmd {
	name := bindingName(k)

	// The error screen's way out. Checked before anything else because the
	// interface is already broken here: routing q through the keymap would
	// quantise a deck, since q is bound to Quantize.
	if m.panicked {
		switch name {
		case "q", "ctrl+c":
			m.quitting = true
		}
		return nil
	}

	// Mode-relative keys run BEFORE the universal exits. TAB and SHIFT+ENTER
	// mean something different inside the browser (cycle panes, load to the
	// other deck) and must reach it first -- handled after this, TAB changed
	// which DECK was focused while the user was browsing, which is the moment a
	// mistake is most expensive.
	//
	// Settings is here for the same reason and one more: almost every key is
	// text destined for the path field, so if it ran second, typing "esc" into
	// a path would close the screen instead of typing four characters.
	if m.st.Mode == ModeLibrary || m.st.Mode == ModeOverlay {
		if m.handleBrowserKey(k) {
			return nil
		}
	}
	if m.st.Mode == ModeSettings {
		if cmd, done := m.handleSettingsKey(k); done {
			return cmd
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
		return nil
	case "tab":
		m.moveFocus(1)
		return nil
	case "shift+tab":
		// The string bubbletea emits for the back-tab sequence "\x1b[Z". It used
		// to match "backtab", which no terminal has ever sent, so reverse deck
		// focus was unreachable -- the same class of bug as SHIFT+ENTER, missed
		// for the same reason: the test called handleKey with a string the
		// transport cannot produce.
		m.moveFocus(-1)
		return nil
	case "?":
		m.st.Mode = toggleMode(m.st.Mode, ModeHelp)
		return nil
	case "`":
		m.st.Mode = toggleMode(m.st.Mode, ModeDebug)
		return nil
	case "/":
		// Focus the track list on entry, for the same reason LibraryOpen does.
		m.st.LibPane = views.PaneTracks
		m.st.Mode = toggleMode(m.st.Mode, ModeLibrary)
		return nil
	case "l":
		// The quick-load overlay, reachable from the mix view. Without this the
		// overlay had no producer at all: a view nothing can open is dead code,
		// and the empty-deck art told the user to press exactly this key.
		if m.st.Mode == ModeMix {
			m.st.Mode = ModeOverlay
			// Focus the track list, exactly as "/" does for the browser.
			//
			// LibPane defaults to the crates pane, and the overlay renders no
			// crate list at all. Left at the default, every arrow key moved a
			// cursor the overlay never draws, so the list could not be scrolled
			// and only the first result was ever reachable.
			m.st.LibPane = views.PaneTracks
		}
		return nil
	case "S":
		// The settings view, from which the library root can be set.
		//
		// Uppercase on purpose. Every lowercase letter in this application is
		// either a deck control or bound by DefaultKeys, and shadowing one to
		// reach a settings screen would take a control away rather than add a
		// way in.
		if m.st.Mode == ModeMix {
			m.openSettings()
		}
		return nil
	}

	switch name {
	case "v":
		m.st.Layout = m.st.Layout.next()
		return nil
	case "z":
		m.st.Zoom = !m.st.Zoom
		return nil
	case "Z":
		m.st.ZoomBars = ZoomBarOptions[ZoomIndex(zoomIndexOf(m.st.ZoomBars))]
		return nil
	case "up":
		m.nudge(-1)
		return nil
	case "down":
		m.nudge(1)
		return nil
	}

	if m.keys == nil {
		return nil
	}
	a, ok := m.keys.Resolve(name)
	if !ok {
		return nil // unknown key: no state change, no action
	}
	m.applyAction(a)
	return nil
}

// openSettings enters the settings view, seeding the path field with what is
// currently configured.
//
// Seeding rather than starting blank matters: the user arrives at this screen
// to edit an existing value, and an empty field invites them to retype a path
// they can already see one line above.
func (m *Model) openSettings() {
	if m.cfg != nil {
		m.st.SettingInput = m.cfg.UI.LibraryRoot
	}
	m.st.SettingNote = ""
	m.st.Mode = ModeSettings
}

// handleSettingsKey handles the settings view's own keys. It reports whether it
// consumed the key.
//
// Like handleBrowserKey it takes the RAW key string, so the space bar types a
// space. Library directories named "DJ Music" or "My Tracks" are ordinary, and
// a field that silently drops the space in the path the user is typing is a
// field that produces a path that does not exist.
func (m *Model) handleSettingsKey(k string) (tea.Cmd, bool) {
	switch k {
	case "enter":
		root := strings.TrimSpace(m.st.SettingInput)
		if root == "" {
			// Clearing the library is a real request, so it needs a real path to
			// clear to. Treating an empty field as "scan nothing" would make
			// backspacing a directory away silently empty the library.
			m.setError("type a directory, or esc to cancel")
			return nil, true
		}
		root = config.ExpandHome(root)
		if m.saveFunc != nil {
			if err := m.saveFunc(root); err != nil {
				m.setError(err.Error())
				return nil, true
			}
		}
		if m.cfg != nil {
			m.cfg.UI.LibraryRoot = root
		}
		m.setError("")
		if m.scanFunc == nil {
			m.st.SettingNote = "saved; rescanning is unavailable in this build"
			return nil, true
		}
		m.st.SettingNote = ""
		m.st.ScanBusy = true
		return m.scanCmd(root), true

	case "backspace", "left":
		m.st.SettingInput = dropLastRune(m.st.SettingInput)
		return nil, true
	case "ctrl+u":
		m.st.SettingInput = ""
		return nil, true
	}

	// Anything printable goes in the path. Not just letters: a path is mostly
	// slashes, dots and tildes, and a view that accepted only alphanumerics
	// would be unusable for the thing it exists to do.
	if r := []rune(k); len(r) == 1 && r[0] >= ' ' {
		m.st.SettingInput += k
		return nil, true
	}
	return nil, false
}

// scanCmd runs a rescan off the UI goroutine.
//
// It has to: a scan of a real collection walks tens of thousands of files and
// takes seconds. Run inline, it would stop the tick that drives the level
// meters, so a DJ pressing enter to repoint their library would watch the
// interface freeze with no indication anything was happening -- the worst
// possible read of a keypress that did work.
func (m *Model) scanCmd(root string) tea.Cmd {
	scan := m.scanFunc
	return func() tea.Msg {
		if scan == nil {
			return scanDoneMsg{root: root, err: fmt.Errorf("rescanning is not available")}
		}
		ix, err := scan(root)
		return scanDoneMsg{root: root, index: ix, err: err}
	}
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
	case "tab", "shift+tab":
		// The full browser renders three panes, so TAB cycles between them
		// (spec 6.5). It used to fall through to the global TAB, which moved
		// DECK focus -- so browsing a library and pressing tab silently changed
		// which deck ENTER would load into.
		//
		// The overlay renders one. views.OverlayRows draws only the track list:
		// it references no pane at all, so a pane cursor there was a focus the
		// user could neither see nor act on, and pressing TAB did nothing
		// visible whatsoever. The overlay returns false instead, letting the key
		// reach the global TAB and move DECK focus -- which is what someone
		// pressing it mid-search wants, since that is how they aim ENTER at the
		// other deck.
		//
		// SHIFT+TAB is handled here for the same reason TAB is: it was not
		// matched at all, so it fell through to the global handler and moved
		// deck focus while the user was browsing. The pane-cycling test only
		// ever exercised TAB.
		if m.st.Mode != ModeLibrary {
			return false
		}
		delta := 1
		if name == "shift+tab" {
			delta = -1
		}
		n := int(views.PaneCount)
		m.st.LibPane = views.Pane(((int(m.st.LibPane)+delta)%n + n) % n)
		return true
	case "left":
		m.st.Query = dropLastRune(m.st.Query)
		return true
	case "right":
		return true
	case "enter":
		// ENTER loads to the focused deck (spec 6.5). It consumed the key and
		// did nothing before, so the browser had no way to load anything.
		//
		// The "other deck" is reached by moving deck focus first -- TAB in the
		// mix view, or TAB inside the overlay -- not by a modifier on ENTER.
		// SHIFT+ENTER was bound here and was unreachable: bubbletea has no
		// Shift field, and only reports "shift+enter" under the kitty keyboard
		// protocol. An ordinary terminal sends ENTER's bytes for both, so
		// bubbletea reported "enter" and the case never fired. The test passed
		// only because it called handleKey with the string directly, bypassing
		// the transport the application actually uses.
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
		// Say so. This returned silently, so ENTER on an empty library was
		// indistinguishable from ENTER not being wired to anything -- and the
		// usual reason the list is empty is that no library root is set, which
		// is exactly what the message should point at.
		m.setError(m.nothingToLoadReason())
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

// nothingToLoadReason explains an ENTER that loaded nothing, naming whichever of
// the three causes applies rather than reporting the same non-answer each time.
func (m *Model) nothingToLoadReason() string {
	if m.trackCount() == 0 && m.st.Query != "" {
		return fmt.Sprintf("no tracks match %q -- backspace to clear the filter", m.st.Query)
	}
	if m.lib == nil || m.lib.Len() == 0 {
		if m.cfg != nil && m.cfg.UI.LibraryRoot == "" {
			return "the library is empty -- press S to set a library root"
		}
		return "the library is empty -- press S to check the library root"
	}
	return "no track selected"
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
	// Resolve "no deck named" to the focused deck.
	//
	// A keyboard binding that names no deck carries Target -1; a MIDI mapping
	// always names one. Nothing filled that in, so the engine read -1 as an
	// out-of-range index and dropped the action, or -- worse, before Target
	// became signed, as deck 0. Either way space played deck A while the deck
	// panel said B was focused: the right-hand deck could not be played at all.
	// Only deck-scoped actions consult the target, so assigning it to a mixer
	// or view action would be meaningless rather than harmful.
	if a.Target < 0 && a.TakesDeck() {
		a.Target = m.st.Focus
	}
	// Hotcue jumps are the one action the engine can refuse: jumpToHotcue
	// returns quietly when the slot is empty, and the reply is lost through the
	// command queue. The snapshot already lists which slots are set, so check
	// here -- otherwise jumping to a cue that was never set is indistinguishable
	// from a dead key.
	if a.Kind == engine.DeckHotcueJump {
		if m.slotHasNoHotcue(a.Target, a.Slot) {
			m.setError(fmt.Sprintf("no hotcue in slot %d on deck %c", a.Slot+1, 'A'+rune(a.Target)))
			return
		}
	}
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
		return "↑↓ select   ⏎ load focused deck   tab pane   s sort   esc back"
	case ModeHelp:
		return "esc back"
	case ModeDebug:
		return "` close   esc back"
	case ModeOverlay:
		return "type to filter   tab other deck   ⏎ load   esc cancel"
	case ModeSettings:
		return "type a path   ⏎ scan and save   esc cancel"
	default:
		return "tab focus   v layout   z zoom   / library   l load   S settings   ? help"
	}
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// slotHasNoHotcue reports whether the given hotcue slot is empty on a deck.
//
// Slot N is set when the snapshot carries at least N+1 cue times: the snapshot
// fills them densely from slot 0, so a gap cannot appear in the middle.
func (m *Model) slotHasNoHotcue(deck, slot int) bool {
	if m.eng == nil || deck < 0 || slot < 0 {
		return false
	}
	snap := m.eng.Snapshot()
	if deck >= len(snap.Decks) {
		return false
	}
	d := snap.Decks[deck]
	n := d.CueCount
	if n > len(d.CueTimes) {
		n = len(d.CueTimes)
	}
	return slot >= n
}
