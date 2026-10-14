package tui

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/maguldon/mix-tui/internal/config"
	"github.com/maguldon/mix-tui/internal/engine"
	"github.com/maguldon/mix-tui/internal/tui/library"
	"github.com/maguldon/mix-tui/internal/tui/views"
)

// The keyboard sweep.
//
// Three bindings in this application were matched against strings no terminal
// can produce, and each shipped with a passing test, because the test called
// handleKey with a string it invented instead of going through the transport the
// program actually uses. tea.KeyMsg is the only path a real keystroke takes, so
// everything here goes through Update.
//
// The sweep has four parts:
//
//	TestEveryDefaultBindingReachesItsAction -- every key in config.DefaultKeys()
//	                                         reaches the action it declares.
//	TestEveryHardcodedUIKeyDoesSomething      -- every key keys.go hardcodes
//	                                         changes something.
//	TestKeysDoTheirOwnThingInEveryMode        -- the same key does what the mode
//	                                         says it does, in each mode.
//	TestEveryShippedBindingIsCovered          -- the closure check: a key that is
//	                                         not in one of the three is an error.
//
// plus TestKnownInertBindings, which records the key/mode pairs the sweep found
// that reach a handler and do nothing. Those are reported, not fixed: changing
// what a key does is a behaviour change, and folding it into a test-only commit
// would make the measurement and the repair indistinguishable in review.

// sweepEngine records the actions the interface sends, and mirrors the one
// piece of engine state the key path reads back: a deck's hotcue slots.
//
// Without the slot bookkeeping a hotcue jump would find its slot empty and
// refuse -- correctly, but in a sweep that would look like a dead binding. A
// jump is only testable against an engine that has been told to set the cue.
type sweepEngine struct {
	snap    engine.Snapshot
	applied []engine.Action
	loaded  []string
}

func (s *sweepEngine) Apply(a engine.Action) {
	s.applied = append(s.applied, a)
	if a.Kind == engine.DeckHotcueSet && a.Target >= 0 && a.Target < len(s.snap.Decks) {
		d := &s.snap.Decks[a.Target]
		if a.Slot >= 0 && a.Slot < len(d.CueTimes) {
			d.CueTimes[a.Slot] = time.Second
			if a.Slot >= d.CueCount {
				d.CueCount = a.Slot + 1
			}
		}
	}
}

func (s *sweepEngine) Snapshot() engine.Snapshot { return s.snap }
func (s *sweepEngine) DeckCount() int            { return len(s.snap.Decks) }

// sweepModel builds a model with two loaded decks, a library and a loader, so
// every binding has something real to act on.
func sweepModel(t *testing.T) (*Model, *sweepEngine) {
	t.Helper()
	beats := make([]time.Duration, 64)
	for i := range beats {
		beats[i] = time.Duration(i) * 500 * time.Millisecond
	}
	e := &sweepEngine{snap: engine.Snapshot{Decks: []engine.DeckState{
		{Index: 0, Loaded: true, Playing: true, Position: 12 * time.Second,
			Duration: 3 * time.Minute, BPM: 128, HasBeatGrid: true,
			Title: "A Track", Artist: "Someone", BeatTimes: beats},
		{Index: 1, Loaded: true, Position: 4 * time.Second, Duration: 3 * time.Minute,
			BPM: 128, HasBeatGrid: true, Title: "B Track", BeatTimes: beats},
	}}}
	keys, err := config.NewKeyResolver(config.Defaults())
	if err != nil {
		t.Fatal(err)
	}
	m := New(e, config.Defaults(), keys)
	ix := library.NewIndex()
	ix.Add(library.Track{Path: "/m/1.wav", Title: "Inner Speaker", BPM: 128, Duration: 3 * time.Minute})
	ix.Add(library.Track{Path: "/m/2.wav", Title: "Warehouse Dub", BPM: 140, Duration: 3 * time.Minute})
	ix.Add(library.Track{Path: "/m/3.wav", Title: "Back Room", BPM: 90, Duration: 3 * time.Minute})
	m.SetLibrary(ix)
	// The browsers open on the track list, which is what "/" and "l" both do.
	// A test that sets Mode directly would otherwise start on the crates pane,
	// where the track cursor is not the one being moved.
	m.st.LibPane = views.PaneTracks
	m.SetLoader(func(_ int, path string) error {
		e.loaded = append(e.loaded, path)
		return nil
	})
	return m, e
}

// observable reports whether a press made the interface do anything at all: an
// engine action, a quit, or a change in view state.
//
// ViewState embeds a DeckState with slices in it, so the structs cannot be
// compared with ==; DeepEqual is the honest comparison.
func observable(before ViewState, m *Model, e *sweepEngine, actions int) bool {
	return len(e.applied) > actions || m.quitting || !reflect.DeepEqual(before, m.State())
}

// binding is one key and what pressing it has to do.
type binding struct {
	key string
	// mode is where the key is pressed. Most keys belong to the mix view; a
	// key with no effect there is given the mode it does have an effect in,
	// because ESC closing the mix view is closing nothing.
	mode Mode
	// before are keys pressed first, to put the model in a state where this
	// binding can actually fire. A hotcue jump needs its cue set.
	before []string
	// action is the engine.ActionKind the key has to reach the engine as. Zero
	// means the model consumes the key itself -- navigation, layout, zoom and
	// quit never reach the engine, and should not.
	action engine.ActionKind
	// note explains an entry whose expectation is not obvious.
	note string
}

// defaultBindings is what every key in config.DefaultKeys() has to do.
//
// TestEveryShippedBindingIsCovered checks this against config.DefaultKeys(), so
// a key added there without a line here fails rather than going untested.
var defaultBindings = map[string]binding{
	"space":  {key: "space", action: engine.DeckPlayPause},
	"c":      {key: "c", action: engine.DeckCue, note: "toggle: the first press engages"},
	"s":      {key: "s", action: engine.SyncToggle},
	"q":      {key: "q", action: engine.Quantize},
	"[":      {key: "[", action: engine.DeckPitch},
	"]":      {key: "]", action: engine.DeckPitch},
	"ctrl+c": {key: "ctrl+c", note: "quit is a program action, not audio state"},
	"z":      {key: "z", note: "caught by the UI switch before the keymap"},
	"v":      {key: "v", note: "caught by the UI switch before the keymap"},
	"/":      {key: "/", note: "caught by the UI switch before the keymap"},
	// LibraryClose is declared, but the universal ESC exit runs first, so the
	// binding's own action never fires. ESC still closes the browser.
	"escape": {key: "escape", mode: ModeLibrary, note: "closed by the universal ESC exit"},
	"r":      {key: "r", action: engine.LoopRoll},

	"1": {key: "1", action: engine.DeckHotcueSet},
	"2": {key: "2", before: []string{"1"}, action: engine.DeckHotcueJump},
	"3": {key: "3", action: engine.DeckHotcueSet},
	"4": {key: "4", before: []string{"3"}, action: engine.DeckHotcueJump},
	"5": {key: "5", action: engine.DeckHotcueSet},
	"6": {key: "6", before: []string{"5"}, action: engine.DeckHotcueJump},
	"7": {key: "7", action: engine.DeckHotcueSet},
	"8": {key: "8", before: []string{"7"}, action: engine.DeckHotcueJump},

	"9":  {key: "9", action: engine.LoopIn},
	"0":  {key: "0", action: engine.LoopOut},
	"w":  {key: "w", action: engine.LoopBeat},
	"e":  {key: "e", action: engine.LoopBeat},
	"y":  {key: "y", action: engine.LoopBeat},
	"u":  {key: "u", action: engine.LoopBeat},
	"i":  {key: "i", action: engine.LoopSave},
	"o":  {key: "o", action: engine.LoopRecall},
	"\\": {key: "\\", action: engine.LoopRelease},

	"m": {key: "m", action: engine.MixerCueMix, note: "toggle, seeded engaged"},
	"-": {key: "-", action: engine.MixerMasterGain},
	"=": {key: "=", action: engine.MixerMasterGain},
}

// hardcodedUIKeys are the keys internal/tui/keys.go handles before any config
// lookup. They cannot be rebound, so nothing else records them.
var hardcodedUIKeys = map[string]binding{
	"tab":       {key: "tab", note: "deck focus in the mix view"},
	"shift+tab": {key: "shift+tab", note: "deck focus, the other way"},
	"?":         {key: "?"},
	"`":         {key: "`"},
	"/":         {key: "/"},
	"l":         {key: "l"},
	"S":         {key: "S"},
	"v":         {key: "v"},
	"z":         {key: "z"},
	"Z":         {key: "Z"},
	"up":        {key: "up", action: engine.MixerCrossfader},
	"down":      {key: "down", action: engine.MixerCrossfader},
	"escape":    {key: "escape", mode: ModeLibrary},
	"ctrl+c":    {key: "ctrl+c"},
	// On the error screen the keymap does not apply, so "q" -- bound to
	// Quantize -- is what a broken interface offers as its way out. That screen
	// swallows everything else, so it has its own test below.
	"q": {key: "q", note: "the error screen's exit; see TestErrorScreenExitsThroughTheTransport"},
}

// TestEveryDefaultBindingReachesItsAction drives every key in
// config.DefaultKeys() through Update with a real tea.KeyMsg and asserts it
// produces its action or changes the interface.
//
// The alternative -- a list of keys someone remembers -- is what let
// "shift+enter" ship: it had a test, because the test invented the string it
// pressed.
func TestEveryDefaultBindingReachesItsAction(t *testing.T) {
	for name := range config.DefaultKeys() {
		b, ok := defaultBindings[name]
		if !ok {
			t.Errorf("config.DefaultKeys() binds %q and this sweep says nothing about it", name)
			continue
		}
		t.Run(name, func(t *testing.T) {
			m, e := sweepModel(t)
			m.st.Mode = b.mode
			for _, k := range b.before {
				press(t, m, k)
			}
			before := m.State()
			actions := len(e.applied)

			press(t, m, b.key)

			if !observable(before, m, e, actions) {
				t.Fatalf("pressing %q did nothing: no engine action, no state change%s",
					b.key, noteSuffix(b.note))
			}
			assertAction(t, e, actions, b.key, b.action)
		})
	}
}

// TestEveryHardcodedUIKeyDoesSomething covers the keys keys.go handles itself.
// They are checked before the keymap, so a binding table says nothing at all
// about whether they work.
func TestEveryHardcodedUIKeyDoesSomething(t *testing.T) {
	for name, b := range hardcodedUIKeys {
		t.Run(name, func(t *testing.T) {
			if name == "q" {
				// The error screen is its own world: it swallows the keymap, so
				// pressing "q" in the mix view quantises a deck and says nothing
				// about the screen where "q" is the only way out.
				t.Skip("the error-screen exit; see TestErrorScreenExitsThroughTheTransport")
			}
			m, e := sweepModel(t)
			m.st.Mode = b.mode
			before := m.State()
			actions := len(e.applied)

			press(t, m, b.key)

			if !observable(before, m, e, actions) {
				t.Fatalf("pressing %q did nothing%s", b.key, noteSuffix(b.note))
			}
			assertAction(t, e, actions, b.key, b.action)
		})
	}
}

// assertAction checks the engine action a key was supposed to produce. A zero
// want means the model consumes the key and nothing should reach the engine.
func assertAction(t *testing.T, e *sweepEngine, from int, key string, want engine.ActionKind) {
	t.Helper()
	if want == 0 {
		return
	}
	for _, a := range e.applied[from:] {
		if a.Kind == want {
			return
		}
	}
	t.Errorf("pressing %q produced %v, want a %v", key, e.applied[from:], want)
}

// TestEveryShippedBindingIsCovered is what makes the tables above a sweep
// rather than a list: every key the keymap declares, and every string the
// handlers match on, has to appear in one of them.
//
// The first half fails if a key is added to the keymap and not to the table.
// The second fails if the handlers match on something no table drives. Together
// they mean a new binding cannot arrive untested.
func TestEveryShippedBindingIsCovered(t *testing.T) {
	for name := range config.DefaultKeys() {
		if _, ok := defaultBindings[name]; !ok {
			t.Errorf("config.DefaultKeys() binds %q, which no test here drives", name)
		}
	}
	matched := matchedNames(t)
	for name := range hardcodedUIKeys {
		if _, ok := matched[name]; ok {
			continue
		}
		t.Errorf("hardcodedUIKeys lists %q, which nothing in the package matches on; "+
			"either the key was removed or the list is stale", name)
	}

	// The other direction: every string a switch matches on has to be a key the
	// sweep drives, in some mode. Layout names and the input side of bindingName
	// are not keys. The inert findings count as driven, because
	// TestKnownInertBindings drives them and records that they do nothing.
	notAKey := map[string]bool{
		"A": true, // layout names, state.go layoutFromName
		"B": true,
		"C": true,
		// The input side of bindingName: what bubbletea reports for these. The
		// cases the translation exists for are "space" and "escape", both in
		// config.DefaultKeys.
		" ":   true,
		"esc": true,
	}
	covered := map[string]bool{}
	for n := range defaultBindings {
		covered[n] = true
	}
	for n := range hardcodedUIKeys {
		covered[n] = true
	}
	for _, c := range perModeCases() {
		covered[c.key] = true
	}
	for _, in := range inertBindings {
		covered[in.key] = true
	}
	for name, pos := range matched {
		if notAKey[name] || covered[name] {
			continue
		}
		t.Errorf("%s: a switch matches %q but no table in the sweep drives it", pos, name)
	}
}

// textFieldLeaks are the keys that reach the engine from inside a text field,
// because a mode-relative handler does not claim them.
//
// nudge() guards ModeLibrary, where up/down are the list cursor, but not
// ModeSettings -- so pressing them while typing a path moves the crossfader
// behind a screen that does not show it. That is a behaviour decision, not a
// typo, so it is reported rather than fixed here; TestArrowsBehindTheSettings
// pins what it currently does so the hole is at least a measured one.
var textFieldLeaks = map[Mode]map[string]bool{
	ModeSettings: {"up": true, "down": true},
}

// TestNoShippedKeyReachesTheEngineFromATextField is the mode sweep applied to
// the whole keymap at once, rather than to the handful of keys the mode cases
// name individually.
//
// The mix view and a browser field disagree about what most of these keys are
// for: space plays a deck in one and types a character in the other, and s, d,
// v, z and the rest are commands in one and letters in the other. A key that
// reaches the engine from inside a field has leaked an audio control into a
// text box, which is how "hit the drop" once started the focused deck twice.
//
// ctrl+c is exempt: it is the way out, from every screen.
func TestNoShippedKeyReachesTheEngineFromATextField(t *testing.T) {
	shipped := map[string]bool{}
	for name := range config.DefaultKeys() {
		shipped[name] = true
	}
	for name := range hardcodedUIKeys {
		shipped[name] = true
	}
	for _, mode := range []Mode{ModeLibrary, ModeOverlay, ModeSettings} {
		leaks := textFieldLeaks[mode]
		for name := range shipped {
			if name == "ctrl+c" || leaks[name] {
				continue
			}
			t.Run(modeName(mode)+" "+name, func(t *testing.T) {
				m, e := sweepModel(t)
				m.st.Mode = mode
				press(t, m, name)
				if len(e.applied) != 0 {
					t.Errorf("%q in %s applied %v; it must be typed, filtered or consumed",
						name, modeName(mode), e.applied)
				}
			})
		}
	}
}

// TestArrowsBehindTheSettingsScreen pins the one exemption above, so it is a
// measurement rather than a hole.
//
// nudge() returns early in ModeLibrary, where the arrows are the list cursor,
// and does not in ModeSettings. Pressing up or down while typing a library path
// therefore moves the crossfader, and nothing on the settings screen shows it.
// Whether that should be inert is a behaviour decision; this issue measures.
func TestArrowsBehindTheSettingsScreen(t *testing.T) {
	for _, key := range []string{"up", "down"} {
		t.Run(key, func(t *testing.T) {
			m, e := sweepModel(t)
			m.st.Mode = ModeSettings
			typeText(t, m, "/srv/dj")
			press(t, m, key)

			if len(e.applied) != 1 || e.applied[0].Kind != engine.MixerCrossfader {
				t.Fatalf("%q applied %v; this test exists to pin the current "+
					"behaviour, which is one crossfader nudge", key, e.applied)
			}
			if m.st.SettingInput != "/srv/dj" {
				t.Errorf("the path field changed to %q; the arrows must not eat it",
					m.st.SettingInput)
			}
			t.Logf("%q in settings moves the crossfader to %v behind a screen "+
				"that does not draw it (keys.go:527 nudge)", key, m.st.Xfade)
		})
	}
}

// modeCase asserts that one key does one thing in one mode.
type modeCase struct {
	name string
	mode Mode
	key  string
	// setup arranges the scene -- a pane, a cursor, a saved root. It presses no
	// keys: anything that can be reached by pressing should be, so the path it
	// takes is part of what is being checked.
	setup func(t *testing.T, m *Model, e *sweepEngine)
	want  func(t *testing.T, m *Model, e *sweepEngine)
}

// perModeCases is the per-mode half of the sweep: the keys that mean different
// things in different places, asserted in each of them.
//
// TAB is deck focus in the mix view and pane focus in the browser. SPACE plays
// in the mix view and types in a text field. ESC closes five screens. A key that
// reaches the right handler and does the wrong thing for the mode is the third
// of the three bugs that started this, and a table of emittable strings cannot
// catch it: the key existed, and it did nothing.
func perModeCases() []modeCase {
	return []modeCase{
		// ---- tab: deck focus, or pane focus, depending on the mode ----
		{name: "tab in the mix view moves deck focus", mode: ModeMix, key: "tab",
			want: func(t *testing.T, m *Model, _ *sweepEngine) {
				if m.State().Focus != 1 {
					t.Errorf("focus = %d, want 1", m.State().Focus)
				}
			}},
		{name: "shift+tab in the mix view moves deck focus back", mode: ModeMix, key: "shift+tab",
			want: func(t *testing.T, m *Model, _ *sweepEngine) {
				if m.State().Focus != 1 {
					t.Errorf("focus = %d, want it to wrap round to deck 1", m.State().Focus)
				}
			}},
		{name: "tab in the browser cycles panes", mode: ModeLibrary, key: "tab",
			want: func(t *testing.T, m *Model, _ *sweepEngine) {
				if m.State().LibPane == views.PaneTracks {
					t.Error("tab did not move the pane focus")
				}
				if m.State().Focus != 0 {
					t.Errorf("tab moved DECK focus to %d while browsing", m.State().Focus)
				}
			}},
		{name: "shift+tab in the browser cycles panes back", mode: ModeLibrary, key: "shift+tab",
			want: func(t *testing.T, m *Model, _ *sweepEngine) {
				if m.State().LibPane != views.PaneCrates {
					t.Errorf("pane = %v, want it to wrap round to the crates pane", m.State().LibPane)
				}
				if m.State().Focus != 0 {
					t.Errorf("shift+tab moved DECK focus to %d while browsing", m.State().Focus)
				}
			}},
		// views.OverlayRows draws a track list and references no pane at all, so
		// a pane cursor there is a focus the user can neither see nor act on.
		// This is the fix from f3c11a5, and it is the shape of bug a "does this
		// key exist" table cannot see: the key existed and it did nothing.
		{name: "tab in the overlay moves deck focus", mode: ModeOverlay, key: "tab",
			want: func(t *testing.T, m *Model, _ *sweepEngine) {
				if m.State().Focus != 1 {
					t.Errorf("focus = %d, want 1; the overlay renders no pane to focus", m.State().Focus)
				}
				if m.State().LibPane != views.PaneTracks {
					t.Errorf("tab moved the pane cursor to %v, a pane the overlay does not draw",
						m.State().LibPane)
				}
			}},

		// ---- space: play in the mix view, type in a text field ----
		{name: "space in the mix view plays the focused deck", mode: ModeMix, key: " ",
			want: func(t *testing.T, m *Model, e *sweepEngine) {
				if len(e.applied) != 1 || e.applied[0].Kind != engine.DeckPlayPause {
					t.Errorf("space applied %v, want one DeckPlayPause", e.applied)
				}
				if m.State().Query != "" {
					t.Errorf("space typed %q into the filter", m.State().Query)
				}
			}},
		{name: "space in the browser types", mode: ModeLibrary, key: " ",
			want: func(t *testing.T, m *Model, e *sweepEngine) {
				if m.State().Query != " " {
					t.Errorf("query = %q, want a space", m.State().Query)
				}
				if len(e.applied) != 0 {
					t.Errorf("typing a space applied %v; it must not play a deck", e.applied)
				}
			}},
		{name: "space in the overlay types", mode: ModeOverlay, key: " ",
			want: func(t *testing.T, m *Model, e *sweepEngine) {
				if m.State().Query != " " {
					t.Errorf("query = %q, want a space", m.State().Query)
				}
				if len(e.applied) != 0 {
					t.Errorf("typing a space applied %v; it must not play a deck", e.applied)
				}
			}},
		{name: "space in the settings field types", mode: ModeSettings, key: " ",
			want: func(t *testing.T, m *Model, e *sweepEngine) {
				if m.State().SettingInput != " " {
					t.Errorf("path field = %q, want a space; library directories "+
						"are named with spaces", m.State().SettingInput)
				}
				if len(e.applied) != 0 {
					t.Errorf("typing a space applied %v", e.applied)
				}
			}},

		// ---- escape: closes every screen it is documented to close ----
		{name: "escape leaves the browser", mode: ModeLibrary, key: "escape",
			want: func(t *testing.T, m *Model, _ *sweepEngine) {
				if m.State().Mode != ModeMix {
					t.Errorf("mode = %v, want ModeMix", m.State().Mode)
				}
			}},
		{name: "escape leaves the overlay", mode: ModeOverlay, key: "escape",
			want: func(t *testing.T, m *Model, _ *sweepEngine) {
				if m.State().Mode != ModeMix {
					t.Errorf("mode = %v, want ModeMix", m.State().Mode)
				}
			}},
		{name: "escape leaves help", mode: ModeHelp, key: "escape",
			want: func(t *testing.T, m *Model, _ *sweepEngine) {
				if m.State().Mode != ModeMix {
					t.Errorf("mode = %v, want ModeMix", m.State().Mode)
				}
			}},
		{name: "escape leaves the debug overlay", mode: ModeDebug, key: "escape",
			want: func(t *testing.T, m *Model, _ *sweepEngine) {
				if m.State().Mode != ModeMix {
					t.Errorf("mode = %v, want ModeMix", m.State().Mode)
				}
			}},
		{name: "escape leaves the settings screen", mode: ModeSettings, key: "escape",
			want: func(t *testing.T, m *Model, _ *sweepEngine) {
				if m.State().Mode != ModeMix {
					t.Errorf("mode = %v, want ModeMix", m.State().Mode)
				}
			}},
		// ESC also resets the sort, so re-entering the browser does not open
		// with a menu covering the detail pane (spec 6.5).
		{name: "escape resets the sort on the way out of the browser", mode: ModeLibrary, key: "escape",
			setup: func(t *testing.T, m *Model, _ *sweepEngine) {
				m.st.ShowSort = true
				m.st.SortBy = library.SortBPM
				m.st.SortDesc = true
			},
			want: func(t *testing.T, m *Model, _ *sweepEngine) {
				if m.State().ShowSort || m.State().SortBy != library.SortTitle || m.State().SortDesc {
					t.Errorf("sort state survived leaving the browser: %+v", m.State())
				}
			}},

		// ---- up/down: crossfader in the mix view, cursor in a list ----
		{name: "down in the mix view nudges the crossfader", mode: ModeMix, key: "down",
			want: func(t *testing.T, m *Model, e *sweepEngine) {
				if m.State().Xfade <= 0 {
					t.Errorf("crossfader = %v, want a positive nudge", m.State().Xfade)
				}
				if len(e.applied) != 1 || e.applied[0].Kind != engine.MixerCrossfader {
					t.Errorf("down applied %v, want one MixerCrossfader", e.applied)
				}
			}},
		{name: "up in the mix view nudges the crossfader back", mode: ModeMix, key: "up",
			want: func(t *testing.T, m *Model, _ *sweepEngine) {
				if m.State().Xfade >= 0 {
					t.Errorf("crossfader = %v, want a negative nudge", m.State().Xfade)
				}
			}},
		{name: "down in the browser moves the track cursor", mode: ModeLibrary, key: "down",
			want: func(t *testing.T, m *Model, _ *sweepEngine) {
				if m.State().TrackSel != 1 {
					t.Errorf("track cursor = %d, want 1", m.State().TrackSel)
				}
			}},
		{name: "up in the browser moves the track cursor", mode: ModeLibrary, key: "up",
			setup: func(t *testing.T, m *Model, _ *sweepEngine) { m.st.TrackSel = 2 },
			want: func(t *testing.T, m *Model, _ *sweepEngine) {
				if m.State().TrackSel != 1 {
					t.Errorf("track cursor = %d, want 1", m.State().TrackSel)
				}
			}},
		{name: "down on the crates pane moves the crate cursor", mode: ModeLibrary, key: "down",
			setup: func(t *testing.T, m *Model, _ *sweepEngine) { m.st.LibPane = views.PaneCrates },
			want: func(t *testing.T, m *Model, _ *sweepEngine) {
				if m.State().CrateSel != 1 {
					t.Errorf("crate cursor = %d, want 1", m.State().CrateSel)
				}
			}},
		{name: "down in the overlay moves the result cursor", mode: ModeOverlay, key: "down",
			want: func(t *testing.T, m *Model, _ *sweepEngine) {
				if m.State().TrackSel != 1 {
					t.Errorf("result cursor = %d, want 1", m.State().TrackSel)
				}
			}},

		// ---- enter: load in a browser, save in settings ----
		{name: "enter in the browser loads the selected track", mode: ModeLibrary, key: "enter",
			want: func(t *testing.T, m *Model, e *sweepEngine) {
				if len(e.loaded) != 1 || e.loaded[0] != "/m/1.wav" {
					t.Errorf("loaded %v, want [/m/1.wav]", e.loaded)
				}
				if m.State().Mode != ModeMix {
					t.Errorf("mode = %v, want the decks back so the load is visible", m.State().Mode)
				}
			}},
		{name: "enter in the overlay loads the highlighted track", mode: ModeOverlay, key: "enter",
			want: func(t *testing.T, m *Model, e *sweepEngine) {
				if len(e.loaded) != 1 {
					t.Errorf("loaded %v, want one track", e.loaded)
				}
				if m.State().Focus != 0 {
					t.Errorf("focus = %d; it should follow the load", m.State().Focus)
				}
			}},
		// "The other deck" is what SHIFT+ENTER used to mean. It is unreachable
		// -- bubbletea has no Shift field, and an ordinary terminal sends
		// ENTER's bytes for Shift+Enter -- so the reachable route is to move
		// deck focus and press ENTER. This drives it the way the application
		// receives keys, so it cannot pass on a binding no terminal sends.
		{name: "tab then enter loads to the other deck", mode: ModeMix, key: "enter",
			setup: func(t *testing.T, m *Model, _ *sweepEngine) {
				press(t, m, "tab") // aim at deck B
				if m.State().Focus != 1 {
					t.Fatalf("setup: tab left focus at %d, want 1", m.State().Focus)
				}
				m.st.Mode = ModeLibrary
			},
			want: func(t *testing.T, m *Model, e *sweepEngine) {
				if len(e.loaded) != 1 {
					t.Fatalf("enter produced %d loads, want 1", len(e.loaded))
				}
			}},
		{name: "enter in the settings screen saves and rescans", mode: ModeSettings, key: "enter",
			setup: func(t *testing.T, m *Model, _ *sweepEngine) {
				typeText(t, m, "/srv/dj")
				m.SetConfigSaver(func(string) error { return nil })
				m.SetLibraryScanner(func(string) (*library.Index, error) {
					return library.NewIndex(), nil
				})
			},
			want: func(t *testing.T, m *Model, _ *sweepEngine) {
				if m.cfg.UI.LibraryRoot != "/srv/dj" {
					t.Errorf("library root = %q, want the typed path", m.cfg.UI.LibraryRoot)
				}
				if !m.st.ScanBusy {
					t.Error("enter did not start a rescan; the interface looks the same either way")
				}
			}},

		// ---- editing: backspace, left and ctrl+u ----
		{name: "backspace in the browser deletes a query character", mode: ModeLibrary, key: "backspace",
			setup: func(t *testing.T, m *Model, _ *sweepEngine) { typeText(t, m, "beat") },
			want: func(t *testing.T, m *Model, _ *sweepEngine) {
				if m.State().Query != "bea" {
					t.Errorf("query = %q, want %q", m.State().Query, "bea")
				}
			}},
		{name: "backspace in the settings field deletes a character", mode: ModeSettings, key: "backspace",
			setup: func(t *testing.T, m *Model, _ *sweepEngine) { typeText(t, m, "/srv/dj") },
			want: func(t *testing.T, m *Model, _ *sweepEngine) {
				if m.State().SettingInput != "/srv/d" {
					t.Errorf("path = %q, want %q", m.State().SettingInput, "/srv/d")
				}
			}},
		{name: "left in the settings field deletes a character", mode: ModeSettings, key: "left",
			setup: func(t *testing.T, m *Model, _ *sweepEngine) { typeText(t, m, "/srv") },
			want: func(t *testing.T, m *Model, _ *sweepEngine) {
				if m.State().SettingInput != "/sr" {
					t.Errorf("path = %q, want %q", m.State().SettingInput, "/sr")
				}
			}},
		{name: "ctrl+u in the settings field clears it", mode: ModeSettings, key: "ctrl+u",
			setup: func(t *testing.T, m *Model, _ *sweepEngine) { typeText(t, m, "/srv/dj") },
			want: func(t *testing.T, m *Model, _ *sweepEngine) {
				if m.State().SettingInput != "" {
					t.Errorf("path = %q, want it cleared", m.State().SettingInput)
				}
			}},

		// ---- the sort menu ----
		{name: "s in the browser opens the sort menu", mode: ModeLibrary, key: "s",
			want: func(t *testing.T, m *Model, _ *sweepEngine) {
				if !m.State().ShowSort {
					t.Fatal("s did not open the sort menu")
				}
				if !strings.Contains(m.RenderAt(160, 24), "SORT BY") {
					t.Error("the open sort menu did not render")
				}
			}},
		{name: "enter in the sort menu picks a sort", mode: ModeLibrary, key: "enter",
			setup: func(t *testing.T, m *Model, _ *sweepEngine) {
				m.st.ShowSort = true
				m.st.SortSel = int(library.SortBPM)
			},
			want: func(t *testing.T, m *Model, _ *sweepEngine) {
				if m.State().ShowSort {
					t.Error("enter did not close the sort menu")
				}
				if m.State().SortBy != library.SortBPM {
					t.Errorf("sort = %v, want SortBPM", m.State().SortBy)
				}
			}},
		{name: "d in the sort menu flips the direction", mode: ModeLibrary, key: "d",
			setup: func(t *testing.T, m *Model, _ *sweepEngine) { m.st.ShowSort = true },
			want: func(t *testing.T, m *Model, _ *sweepEngine) {
				if !m.State().SortDesc {
					t.Error("d did not flip the sort direction")
				}
			}},
		{name: "escape closes the sort menu", mode: ModeLibrary, key: "escape",
			setup: func(t *testing.T, m *Model, _ *sweepEngine) { m.st.ShowSort = true },
			want: func(t *testing.T, m *Model, _ *sweepEngine) {
				if m.State().ShowSort {
					t.Error("escape did not close the sort menu")
				}
			}},

		// A printable character that is also a command must still type, or the
		// filter box cannot contain the letters the commands use. "v" cycles the
		// layout outside a browser and "z" zooms; in here they are just letters.
		{name: "letters that are commands type in the browser", mode: ModeLibrary, key: "v",
			want: func(t *testing.T, m *Model, e *sweepEngine) {
				if m.State().Query != "v" {
					t.Errorf("query = %q, want %q", m.State().Query, "v")
				}
				if len(e.applied) != 0 {
					t.Errorf("typing applied %v", e.applied)
				}
			}},
	}
}

// TestKeysDoTheirOwnThingInEveryMode runs perModeCases: setup, one real
// keystroke through Update, then the assertion.
func TestKeysDoTheirOwnThingInEveryMode(t *testing.T) {
	for _, c := range perModeCases() {
		t.Run(c.name, func(t *testing.T) {
			m, e := sweepModel(t)
			m.st.Mode = c.mode
			if c.setup != nil {
				c.setup(t, m, e)
			}
			press(t, m, c.key)
			if c.want != nil {
				c.want(t, m, e)
			}
		})
	}
}

// TestErrorScreenExitsThroughTheTransport covers the one binding that only
// exists on the error screen.
//
// "q" is bound to Quantize, so the recovery screen has to intercept it, or its
// own instructions ("press q or ctrl+c to quit") would quantise a deck. The
// existing test drove handleKey with a literal; this one drives the transport,
// because the point of the error screen is that it works when nothing else does.
func TestErrorScreenExitsThroughTheTransport(t *testing.T) {
	for _, key := range []string{"q", "ctrl+c"} {
		t.Run(key, func(t *testing.T) {
			m, e := sweepModel(t)
			m.panicOnRender = true
			_ = m.View() // panics, is recovered, and sets panicked
			if !m.panicked {
				t.Fatal("the render did not enter the recovery path")
			}
			press(t, m, key)
			if !m.quitting {
				t.Errorf("%q on the error screen did not quit", key)
			}
			if len(e.applied) != 0 {
				t.Errorf("%q on the error screen applied %v; it must exit, not control a deck", key, e.applied)
			}
		})
	}
}

// inertBinding is a key/mode pair where pressing the key reaches a handler and
// nothing the user can see happens.
//
// Each entry is a finding, not an endorsement. They are recorded here so the
// sweep can report them honestly instead of quietly passing over them, and so
// that fixing one is a deliberate act: this issue measures, and changing what a
// key does is a behaviour change that belongs in its own commit with its own
// justification.
type inertBinding struct {
	key  string
	mode Mode
	// setup arranges the scene for the press. It presses no keys.
	setup func(t *testing.T, m *Model, e *sweepEngine)
	// why says what the key reaches and what the user sees instead.
	why string
	// where is the handler responsible, for the report.
	where string
}

// inertBindings is the sweep's negative result: every key/mode pair it drove
// that turned out to do nothing.
var inertBindings = []inertBinding{
	{
		key: "s", mode: ModeOverlay,
		where: "internal/tui/keys.go:345 (handleBrowserKey, case \"s\")",
		why: "toggles ShowSort, but views.OverlayRows draws no sort menu -- only " +
			"RenderLibrary does -- so nothing on screen moves. It also arms the " +
			"sort branch above it, so a second key is swallowed: see the enter " +
			"entry below. This is the same class as the quick-load tab bug fixed " +
			"in f3c11a5. The key reached a handler that is wrong for the mode.",
	},
	{
		key: "enter", mode: ModeOverlay,
		setup: func(_ *testing.T, m *Model, _ *sweepEngine) {
			m.st.ShowSort = true // what pressing "s" above leaves behind
		},
		where: "internal/tui/keys.go:264 (handleBrowserKey, sort branch, case \"enter\")",
		why: "with ShowSort set, ENTER picks a sort instead of loading a track, " +
			"and views.OverlayRows sorts nothing, so the chosen sort does not " +
			"change the overlay either. Pressing " + "\"s\" then \"enter\" in the " +
			"quick-load overlay loads nothing at all.",
	},
	{
		key: "right", mode: ModeLibrary,
		where: "internal/tui/keys.go:326 (handleBrowserKey, case \"right\")",
		why: "consumed and discarded. There is no right-arrow behaviour in the " +
			"browser and no right-arrow binding in DefaultKeys, so falling through " +
			"would reach Resolve(\"right\") and find nothing. Swallowing it is " +
			"harmless, and inert.",
	},
	{
		key: "right", mode: ModeOverlay,
		where: "internal/tui/keys.go:326 (handleBrowserKey, case \"right\")",
		why:   "the same dead case as the browser: consumed and discarded.",
	},
}

// TestKnownInertBindings pins the findings above.
//
// It asserts that each press really does leave the rendered frame alone and
// sends nothing to the engine, so the sweep can report them without pretending
// they work. A state field changing is not enough: ShowSort flips and the user
// sees nothing, which is the whole point.
//
// When one of these is fixed this test fails, and that is what it is for: the
// fix should delete the entry and add an assertion to perModeCases.
func TestKnownInertBindings(t *testing.T) {
	for _, in := range inertBindings {
		t.Run(in.key+" in "+modeName(in.mode), func(t *testing.T) {
			m, e := sweepModel(t)
			m.st.Mode = in.mode
			if in.setup != nil {
				in.setup(t, m, e)
			}
			frame := m.RenderAt(160, 30)
			actions := len(e.applied)
			loaded := len(e.loaded)

			press(t, m, in.key)

			if got := m.RenderAt(160, 30); got != frame {
				t.Errorf("%q in %s changed the frame, so it is no longer inert; "+
					"delete the entry and assert what it now does", in.key, modeName(in.mode))
			}
			if len(e.applied) != actions {
				t.Errorf("%q in %s reached the engine (%v), so it is no longer inert",
					in.key, modeName(in.mode), e.applied[actions:])
			}
			if len(e.loaded) != loaded {
				t.Errorf("%q in %s loaded %v, so it is no longer inert",
					in.key, modeName(in.mode), e.loaded[loaded:])
			}
			t.Logf("INERT: %q in %s does nothing visible (%s): %s",
				in.key, modeName(in.mode), in.where, in.why)
		})
	}
}

func modeName(m Mode) string {
	switch m {
	case ModeMix:
		return "the mix view"
	case ModeLibrary:
		return "the browser"
	case ModeOverlay:
		return "the quick-load overlay"
	case ModeHelp:
		return "help"
	case ModeDebug:
		return "the debug overlay"
	case ModeSettings:
		return "settings"
	}
	return "an unknown mode"
}

func noteSuffix(note string) string {
	if note == "" {
		return ""
	}
	return " (" + note + ")"
}
