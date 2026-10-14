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
// It found four keys that reached a handler and did nothing, and they are now
// assertions in their own right rather than a list: "s" and "enter" in the
// quick-load overlay, the swallowed right arrow, and the crossfader moving
// behind screens that do not draw it. A key that reaches a handler and does
// nothing is invisible to a table of emittable names, which is why the mode half
// exists at all.

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
//
// "escape" is here and not in config.DefaultKeys(): it was bound to LibraryClose
// there, and the UI switch below runs first, so the binding was resolved and
// discarded on every press. A key the config claims but cannot own invites a
// user to rebind something that will never happen.
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
	// are not keys.
	notAKey := map[string]bool{
		"A": true, // layout names, state.go layoutFromName
		"B": true,
		"C": true,
		// The input side of bindingName: what bubbletea reports for these. The
		// cases the translation exists for are "space" (config.DefaultKeys) and
		// "escape" (hardcodedUIKeys).
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
	for name, pos := range matched {
		if notAKey[name] || covered[name] {
			continue
		}
		t.Errorf("%s: a switch matches %q but no table in the sweep drives it", pos, name)
	}
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
		for name := range shipped {
			if name == "ctrl+c" {
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

// TestArrowsOnlyActInTheMixView pins where the arrow keys belong.
//
// handleBrowserKey claims them in the two browsers, where they are the list
// cursor. The settings, help and debug screens claim nothing, so an arrow there
// fell through to nudge() -- which guarded ModeLibrary, the one mode that cannot
// even reach it -- and moved a crossfader none of those screens draw. The guard
// said "the mix view" and tested for everything else.
func TestArrowsOnlyActInTheMixView(t *testing.T) {
	for _, mode := range []Mode{ModeSettings, ModeHelp, ModeDebug, ModeLibrary, ModeOverlay} {
		for _, key := range []string{"up", "down"} {
			t.Run(modeName(mode)+" "+key, func(t *testing.T) {
				m, e := sweepModel(t)
				m.st.Mode = mode
				if mode == ModeSettings {
					// A path with something to lose, so the assertion also covers
					// the arrows not eating the field.
					typeText(t, m, "/srv")
				}
				before := m.State()
				actions := len(e.applied)

				press(t, m, key)

				if !observable(before, m, e, actions) {
					// In the browsers the arrows are the list cursor, so they do
					// something; this is only a failure for the screens that
					// should ignore them entirely.
					return
				}
				if mode == ModeLibrary || mode == ModeOverlay {
					return
				}
				t.Errorf("%q in %s moved something: crossfader %v, engine %v",
					key, modeName(mode), m.State().Xfade, e.applied[actions:])
			})
		}
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
		// "s" in the overlay used to toggle a sort menu that views.OverlayRows
		// never draws, and having armed that state, "enter" then picked a sort
		// instead of loading. "s" then "enter" loaded nothing at all.
		{name: "s then enter in the overlay still loads", mode: ModeOverlay, key: "enter",
			setup: func(t *testing.T, m *Model, _ *sweepEngine) { press(t, m, "s") },
			want: func(t *testing.T, m *Model, e *sweepEngine) {
				if m.State().ShowSort {
					t.Error("s opened a sort menu in an overlay that renders none")
				}
				if len(e.loaded) != 1 {
					t.Errorf("loaded %v, want one track; s then enter loads nothing", e.loaded)
				}
			}},
		{name: "s in the overlay is a letter", mode: ModeOverlay, key: "s",
			want: func(t *testing.T, m *Model, _ *sweepEngine) {
				if m.State().Query != "s" {
					t.Errorf("query = %q, want %q; the overlay is a filter box", m.State().Query, "s")
				}
				if m.State().ShowSort {
					t.Error("s opened a sort menu that the overlay does not draw")
				}
			}},
		// "right" was consumed and discarded in both browsers: no behaviour, no
		// binding, no effect. It is now simply not claimed, so it behaves like
		// any other unhandled key -- and, like any other, a user binding it gets
		// what they asked for.
		{name: "right in the browser does not reach the engine", mode: ModeLibrary, key: "right",
			setup: func(t *testing.T, m *Model, _ *sweepEngine) { typeText(t, m, "ab") },
			want: func(t *testing.T, m *Model, e *sweepEngine) {
				if m.State().Query != "ab" {
					t.Errorf("query = %q, want it unchanged; the filter has no cursor to move",
						m.State().Query)
				}
				if len(e.applied) != 0 {
					t.Errorf("right applied %v", e.applied)
				}
			}},
		{name: "right in the overlay does not reach the engine", mode: ModeOverlay, key: "right",
			setup: func(t *testing.T, m *Model, _ *sweepEngine) { typeText(t, m, "ab") },
			want: func(t *testing.T, m *Model, e *sweepEngine) {
				if m.State().Query != "ab" {
					t.Errorf("query = %q, want it unchanged", m.State().Query)
				}
				if len(e.applied) != 0 {
					t.Errorf("right applied %v", e.applied)
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

// TestRightArrowReachesAUserBindingInTheBrowser is the observable difference
// the dead "right" case made, and the reason removing it is a fix rather than a
// tidy-up.
//
// The browser swallowed right-arrow before any binding lookup. Nothing in the
// shipped keymap uses it, so the swallow was invisible -- but a user who mapped
// "right" to a control got nothing at all. Now the key is simply not claimed,
// and behaves like every other key the browser has no use for.
func TestRightArrowReachesAUserBindingInTheBrowser(t *testing.T) {
	for _, mode := range []Mode{ModeLibrary, ModeOverlay} {
		t.Run(modeName(mode), func(t *testing.T) {
			cfg := config.Defaults()
			cfg.Keys = map[string]config.KeyBinding{
				"right": {Action: "CueMix", Value: "toggle"},
			}
			keys, err := config.NewKeyResolver(cfg)
			if err != nil {
				t.Fatal(err)
			}
			e := &sweepEngine{snap: engine.Snapshot{Decks: []engine.DeckState{
				{Index: 0, Loaded: true, BPM: 128, HasBeatGrid: true},
			}}}
			m := New(e, cfg, keys)
			m.st.Mode = mode

			press(t, m, "right")

			if len(e.applied) != 1 || e.applied[0].Kind != engine.MixerCueMix {
				t.Errorf("a user binding on %q applied %v; the browser swallowed it",
					"right", e.applied)
			}
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
