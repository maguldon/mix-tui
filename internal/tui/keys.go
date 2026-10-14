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

	// Mode-relative keys run BEFORE the universal exits. TAB means something
	// different inside the browser (cycle panes) and must reach it first --
	// handled after this, TAB changed which DECK was focused while the user was
	// browsing, which is the moment a mistake is most expensive.
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
			m.resetBrowser()
		}
		m.st.Mode = ModeMix
		return nil
	case "tab":
		if !deckFocusVisible(m.st.Mode) {
			return nil
		}
		m.moveFocus(1)
		return nil
	case "shift+tab":
		// The string bubbletea emits for the back-tab sequence "\x1b[Z". It used
		// to match "backtab", which no terminal has ever sent, so reverse deck
		// focus was unreachable -- the same class of bug as SHIFT+ENTER, missed
		// for the same reason: the test called handleKey with a string the
		// transport cannot produce.
		if !deckFocusVisible(m.st.Mode) {
			return nil
		}
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
		// Zoom is per-deck (spec 6.3). Toggling applies to the focused deck,
		// which is what "per-deck toggle" means; tab then z zooms the other one
		// so both can be compared on a shared time scale.
		m.st.setDeckZoom(m.st.Focus)
		m.retargetZoom()
		return nil
	case "Z":
		m.st.ZoomBars = ZoomBarOptions[ZoomIndex(zoomIndexOf(m.st.ZoomBars))]
		// The bar count sets the shared window length, so it has to be
		// recomputed or both decks keep showing the old scale.
		m.retargetZoom()
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
	root := ""
	if m.cfg != nil {
		root = m.cfg.UI.LibraryRoot
	}
	// The caret lands at the end of the seed, so LEFT walks into the part of the
	// path most likely to be wrong. Opening at the start would make every edit a
	// walk to the end first.
	m.setSettingInput(root)
	m.st.SettingNote = ""
	m.st.Mode = ModeSettings
}

// setQuery replaces the filter and puts the caret at the end of it.
//
// Every writer goes through here rather than assigning Query directly. That is
// the whole failure mode a caret adds to a view that was fine without one: some
// code path clears the query, the caret is left pointing past the end, and the
// field silently swallows the next few keystrokes.
func (m *Model) setQuery(s string) {
	m.st.Query = s
	m.st.QueryCursor = len([]rune(s))
}

// setSettingInput replaces the library-root field and puts its caret at the end.
func (m *Model) setSettingInput(s string) {
	m.st.SettingInput = s
	m.st.SettingCursor = len([]rune(s))
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

	case "backspace":
		m.st.SettingInput, m.st.SettingCursor = deleteBefore(m.st.SettingInput, m.st.SettingCursor)
		return nil, true
	case "delete":
		m.st.SettingInput, m.st.SettingCursor = deleteAfter(m.st.SettingInput, m.st.SettingCursor)
		return nil, true
	case "home":
		m.st.SettingCursor = caretToStart()
		return nil, true
	case "end":
		m.st.SettingCursor = caretToEnd(m.st.SettingInput)
		return nil, true
	case "left":
		m.st.SettingCursor = moveCaret(m.st.SettingInput, m.st.SettingCursor, -1)
		return nil, true
	case "right":
		m.st.SettingCursor = moveCaret(m.st.SettingInput, m.st.SettingCursor, 1)
		return nil, true
	case "ctrl+u":
		m.setSettingInput("")
		return nil, true
	}

	// Anything printable goes in the path. Not just letters: a path is mostly
	// slashes, dots and tildes, and a view that accepted only alphanumerics
	// would be unusable for the thing it exists to do.
	if r := []rune(k); len(r) == 1 && r[0] >= ' ' {
		m.st.SettingInput, m.st.SettingCursor = insertRune(m.st.SettingInput, m.st.SettingCursor, r[0])
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
	//
	// The menu is the BROWSER's, and only the browser can open one now (see
	// "s" below). The guard is not redundant armour: it is the statement that
	// this branch belongs to a screen that draws a menu, which is exactly the
	// assumption that was false before.
	if m.st.ShowSort && m.st.Mode == ModeLibrary {
		switch name {
		case "up":
			m.st.SortSel = maxInt(0, m.st.SortSel-1)
			return true
		case "down":
			m.st.SortSel = minInt(m.st.SortSel+1, len(views.SortKeys)-1)
			return true
		case "enter":
			m.st.SortBy = views.SortKeys[m.st.SortSel]
			// Choosing a sort is what makes the list leave a crate's own order.
			m.st.Sorted = true
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

	// "s" opens the sort menu in the browser; without a producer it was
	// unreachable in the running application even though it rendered.
	//
	// In the quick-load overlay it opens nothing. views.OverlayRows draws a
	// title, a query line and a list of results -- it renders no menu at all --
	// so the toggle moved nothing on screen, and then armed the branch above:
	// with ShowSort set, ENTER picked a sort instead of loading the highlighted
	// track, so "s" then "enter" in the overlay loaded nothing whatsoever.
	//
	// The overlay is a filter box, so "s" there is a letter, and belongs to
	// the filter at the bottom of this function. This is the same class of bug
	// as the overlay TAB fixed in f3c11a5: the key reached a handler that is
	// wrong for the mode, and a list of emittable key names cannot see it.
	if name == "s" && m.st.Mode == ModeLibrary {
		m.st.ShowSort = !m.st.ShowSort
		m.st.SortSel = int(m.st.SortBy)
		return true
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
		m.cycleBrowserPane(delta)
		return true
	case "left":
		// Move the caret. LEFT deleted the last character instead, which in a
		// field the user could not see -- the full browser never drew its filter
		// -- destroyed something they had not asked to lose and could not check.
		m.st.QueryCursor = moveCaret(m.st.Query, m.st.QueryCursor, -1)
		return true
	case "right":
		m.st.QueryCursor = moveCaret(m.st.Query, m.st.QueryCursor, 1)
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
		m.st.Query, m.st.QueryCursor = deleteBefore(m.st.Query, m.st.QueryCursor)
		return true
	case "delete":
		// Forward delete. Its absence was not a missing convenience: with the
		// caret at the start of the filter -- where it lands every time the
		// browser or the overlay is opened -- there was no key that could remove
		// the character under it. Every correction began with stepping right and
		// backspacing.
		m.st.Query, m.st.QueryCursor = deleteAfter(m.st.Query, m.st.QueryCursor)
		return true
	case "home":
		m.st.QueryCursor = caretToStart()
		return true
	case "end":
		m.st.QueryCursor = caretToEnd(m.st.Query)
		return true
	case "ctrl+n":
		// Crate editing.
		//
		// Control keys rather than letters, because every printable key types
		// into the filter: a letter-bound action could never fire from the
		// browser, which is the only place it makes sense. Same trap as
		// shift+enter and backtab.
		m.newCrate()
		return true
	case "ctrl+a":
		m.addSelectedToCrate()
		return true
	case "ctrl+d":
		m.removeSelectedFromCrate()
		return true
	}
	// Any other single printable rune filters the list. The full-screen
	// browser previously had no way to narrow a track list at all.
	//
	// r[0] >= ' ' admits the space bar, which is what the user wants when they
	// type a multi-word query like "beat that". Control runes fall below ' '.
	if r := []rune(name); len(r) == 1 && r[0] >= ' ' {
		m.st.Query, m.st.QueryCursor = insertRune(m.st.Query, m.st.QueryCursor, r[0])
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
// "The other deck" is reached by moving deck focus first and pressing ENTER, not
// by a modifier on ENTER. SHIFT+ENTER was bound here and was unreachable:
// bubbletea has no Shift field, so an ordinary terminal's Shift+Enter arrives as
// "enter" and the case never fired.
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
	m.setQuery("")
	m.resetBrowser()
}

// resetBrowser returns the library view to how it is first opened.
//
// Spec 6.5: sort state resets when the view is left. Escape did this and a load
// did not, so a load left the sort applied and the sort menu possibly covering
// the detail pane, and the next "/" reopened looking as though someone else had
// left it that way.
func (m *Model) resetBrowser() {
	m.st.SortBy = library.SortTitle
	m.st.SortDesc = false
	m.st.Sorted = false
	m.st.SortSel = 0
	m.st.ShowSort = false
	m.st.CrateSel = 0
	m.st.TrackSel = 0
	m.st.LibPane = views.PaneTracks
}

// cycleBrowserPane moves the browser's pane cursor by delta, wrapping at both
// ends.
//
// Shared by TAB and by the LibraryFocusNext action so the two cannot drift: an
// action that cycled somewhere else from the same key would be a mapping file
// that looks right and does something the keyboard does not.
func (m *Model) cycleBrowserPane(delta int) {
	n := int(views.PaneCount)
	m.st.LibPane = views.Pane(((int(m.st.LibPane)+delta)%n + n) % n)
}

// stepZoomBars moves the zoom level one step, clamped at both ends.
//
// ZoomIn is coarser -- fewer bars, more time on screen. Both directions clamp
// rather than wrap, so holding a knob at the end of its travel does not silently
// snap to the other extreme.
func (m *Model) stepZoomBars(coarser bool) {
	i := zoomIndexOf(m.st.ZoomBars)
	if coarser {
		i++
	} else {
		i--
	}
	if i < 0 {
		i = 0
	}
	if i >= len(ZoomBarOptions) {
		i = len(ZoomBarOptions) - 1
	}
	m.st.ZoomBars = ZoomBarOptions[i]
}

// selectedCrateName is the crate the list is currently narrowed to.
func (m *Model) selectedCrateName() string {
	if m.lib == nil {
		return ""
	}
	return views.SelectedCrate(m.lib, m.libState())
}

// newCrate creates an empty crate and selects it.
//
// It does not ask for a name: the crate pane's default naming is predictable
// and the file is the user's to edit, which is the same stance the mappings
// take. Renaming by hand in crates.yaml works immediately.
func (m *Model) newCrate() {
	if m.lib == nil {
		return
	}
	// The index has to actually gain the crate. This used to compute a name and
	// then persist the list unchanged, so ctrl+n reported "created Crate 1" and
	// wrote back the same file it had read.
	name := m.lib.NewCrate()
	if err := m.persistCrates(m.lib.Crates()); err != nil {
		m.setError("crate: " + err.Error())
		return
	}
	m.st.CrateSel = m.crateCount() - 1
	m.st.TrackSel = 0
	m.st.SettingNote = "created " + name
}

// addSelectedToCrate appends the highlighted track to the selected crate,
// creating it if none is selected.
func (m *Model) addSelectedToCrate() {
	tr, ok := m.selectedTrack()
	if !ok {
		m.setError("nothing selected to add to a crate")
		return
	}
	if m.lib == nil {
		return
	}
	name := m.selectedCrateName()
	if name == "" {
		// Adding to nothing is the common case, so make the target rather than
		// refusing: the alternative is a crate the user must create first, and
		// a key that only works once you know that.
		name = m.lib.NextCrateName()
	}
	if !m.lib.AddToCrate(name, tr.Path) {
		m.setError(tr.Title + " is already in " + name)
		return
	}
	if err := m.persistCrates(m.lib.Crates()); err != nil {
		m.setError("crate: " + err.Error())
		return
	}
	m.setError("")
	m.st.CrateSel = m.crateIndexOf(name)
	m.st.SettingNote = "added " + tr.Title + " to " + name
}

// removeSelectedFromCrate drops the highlighted track from the selected crate.
func (m *Model) removeSelectedFromCrate() {
	name := m.selectedCrateName()
	if name == "" {
		m.setError("select a crate first: tab to the crate pane and choose one")
		return
	}
	tr, ok := m.selectedTrack()
	if !ok {
		m.setError("nothing selected to remove")
		return
	}
	if !m.lib.RemoveFromCrate(name, tr.Path) {
		m.setError(tr.Title + " is not in " + name)
		return
	}
	if err := m.persistCrates(m.lib.Crates()); err != nil {
		m.setError("crate: " + err.Error())
		return
	}
	m.setError("")
	m.st.SettingNote = "removed " + tr.Title + " from " + name
}

// crateIndexOf is the crate-pane row for a crate name: "All tracks" is 0, so a
// crate at index i sits at i+1.
func (m *Model) crateIndexOf(name string) int {
	if m.lib == nil {
		return 0
	}
	for i, c := range m.lib.Crates() {
		if c.Name == name {
			return i + 1
		}
	}
	return 0
}

// saveCrates is a small guard so a nil index cannot be written through.
func (m *Model) saveCrates() bool { return m.lib != nil }

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
// one per crate. It used to be a constant 2, matching a pane that rendered
// placeholders -- with real crates the arrow keys have to be able to reach past
// "All tracks" or the crate list cannot be opened.
func (m *Model) crateCount() int {
	if m.lib == nil {
		return 1
	}
	return views.CrateRowCount(m.lib)
}

func (m *Model) trackCount() int {
	if m.lib == nil {
		return 0
	}
	return len(library.Search(m.lib.Tracks(), m.st.Query))
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
		// Leaving the browser, whichever kind it is, and resetting it so the
		// next visit starts clean -- the same state a fresh open establishes,
		// which escape already does and a bare Mode assignment did not.
		if m.st.Mode == ModeLibrary || m.st.Mode == ModeOverlay {
			m.resetBrowser()
		}
		m.st.Mode = ModeMix
		return
	case engine.LibraryLoad:
		// The track under the cursor, into the focused deck. The same thing
		// ENTER does in both browsers, reached by a name a mapping file can use.
		if m.st.Mode != ModeLibrary && m.st.Mode != ModeOverlay {
			return
		}
		m.loadSelected()
		return
	case engine.LibrarySearch:
		// "Start a fresh search": enter the browser if not already there, clear
		// what is typed and put the caret in an empty field.
		//
		// Deliberately not the same as LibraryOpen. Opening the browser and
		// keeping the previous query means a second press narrows from the old
		// text rather than starting over, which is the opposite of what a
		// search key means -- and the previous text is invisible in the overlay,
		// whose only field is the query.
		if m.st.Mode != ModeLibrary {
			m.st.Mode = ModeLibrary
		}
		m.st.LibPane = views.PaneTracks
		m.setQuery("")
		return
	case engine.LibrarySort:
		// Open the sort menu, the same as "s" in the browser. The overlay draws
		// no menu and no sort column, so it is refused there rather than
		// setting state the user cannot see or undo.
		if m.st.Mode == ModeLibrary {
			m.st.ShowSort = !m.st.ShowSort
		}
		return
	case engine.LibraryFocusNext:
		// Cycle the browser's panes, the same as TAB inside it. The overlay
		// renders one pane, so TAB there moves DECK focus on purpose; this
		// action must not pretend otherwise.
		if m.st.Mode == ModeLibrary {
			m.cycleBrowserPane(1)
		}
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
		m.st.setDeckZoom(m.st.Focus)
		m.retargetZoom()
	case engine.ZoomIn, engine.ZoomOut:
		// Spec 6.3 calls the zoom level "a live, MIDI-mappable control".
		// ZoomIn and ZoomOut parsed from a mapping file, resolved, and were
		// then discarded by both applyAction and Engine.Apply -- a knob that did
		// nothing, with no error to say so. They are view state rather than
		// engine state, so the engine cannot apply them; this is where they
		// belong.
		m.stepZoomBars(a.Kind == engine.ZoomIn)
		m.retargetZoom()
	case engine.ViewNext:
		m.st.Layout = m.st.Layout.next()
	case engine.ViewPrev:
		m.st.Layout = m.st.Layout.prev()
	case engine.FocusNext:
		// Subject to the same rule as TAB: a mode that does not draw the decks
		// must not have its deck focus moved behind the user's back, or ENTER
		// would load into a deck they cannot see selected.
		if deckFocusVisible(m.st.Mode) {
			m.moveFocus(1)
		}
	case engine.HelpToggle:
		m.st.Mode = toggleMode(m.st.Mode, ModeHelp)
	case engine.DebugToggle:
		m.st.Mode = toggleMode(m.st.Mode, ModeDebug)
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

// deckFocusVisible reports whether a mode draws the decks, so that moving deck
// focus is something the user can see.
//
// TAB is handled before every config binding, so that a mistyped mapping cannot
// lock the user out of a screen. That rule is right and stays. But it also means
// TAB reached help, debug and settings, which replace the deck view entirely:
// there it moved a focus ring that is on no part of the screen, on three of six
// modes, with nothing in any footer saying so. A focus change the user cannot
// see is a focus change that did not happen, so the key is inert there.
//
// This is the same call nudge() makes for the arrows, for the same reason, and
// the browser is covered by the default because handleBrowserKey consumes TAB
// there first -- the full browser has panes to focus, and the overlay does not.
func deckFocusVisible(m Mode) bool {
	switch m {
	case ModeMix, ModeOverlay:
		// The mix view is the decks. The overlay is drawn over them, which is
		// why TAB is the documented way to aim ENTER at the other deck.
		return true
	default:
		return false
	}
}

// nudge moves the crossfader by one step in the mix view.
//
// It applies the change to the engine. It previously only moved the mirrored
// view state, so pressing the key did nothing a DJ could hear and the fader sat
// visually wherever they last dragged it.
//
// The arrows belong to the mix view and nowhere else. handleBrowserKey claims
// them in the two browsers, where they are the list cursor, but the settings
// screen and the help and debug screens do not claim them, so an arrow pressed
// there fell through to here and moved a crossfader that none of those screens
// draw. The guard was ModeLibrary, which is the one mode that cannot even reach
// this function; the screen it was written for is the mix view.
func (m *Model) nudge(delta int) {
	if m.st.Mode != ModeMix {
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
		return "↑↓ select   ⏎ load focused deck   tab pane   ←→ filter   s sort   esc back"
	case ModeHelp:
		return "esc back"
	case ModeDebug:
		return "` close   esc back"
	case ModeOverlay:
		return "type to filter   ←→ edit   tab other deck   ⏎ load   esc cancel"
	case ModeSettings:
		return "type a path   ←→ edit   ⏎ scan and save   esc cancel"
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
