package config

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/maguldon/mix-tui/internal/engine"
	"github.com/maguldon/mix-tui/internal/midi"
)

// KeyResolver maps a key name to an engine.Action. It shares the Action output
// with the MIDI path, so a key and a controller button converge on the same
// behaviour (spec §5.1).
type KeyResolver struct {
	bindings map[string]compiled
	toggles  map[string]bool
	// lines caches the rendered help listing; see DescribeLines.
	lines []string
}

type compiled struct {
	action engine.ActionKind
	target int
	// hasTarget records whether the binding named a deck. A pinned deck always
	// wins; otherwise the action follows whatever deck is focused.
	hasTarget bool
	slot      int
	repeat    bool
	// strategy is how Value is produced: a literal, or a toggle that alternates.
	toggle  bool
	inc     bool
	dec     bool
	literal float64
}

// DefaultKeys is the shipped keymap. It is merged under the user's file, so a
// user rebinding one of these keys overrides it rather than colliding.
func DefaultKeys() map[string]KeyBinding {
	// DeckPlayPause, not DeckPlay: this is the transport button, and DeckPlay
	// only ever starts. With DeckPlay here, pressing space on a rolling deck
	// re-asserted playing=true and nothing happened -- no pause existed at all,
	// while the help screen called this key "play/pause".
	play := KeyBinding{Action: "DeckPlayPause"}
	cue := KeyBinding{Action: "DeckCue", Value: "toggle"}
	// Pitch and the hotcue and loop keys name no deck, so they follow the
	// focused one. "[" and "]" used to pin Target: 0, which as a plain int was
	// indistinguishable from naming no deck at all -- so every untargeted key in
	// this map resolved to deck A and the right-hand deck could not be played,
	// pitched, cued or looped from the keyboard.
	return map[string]KeyBinding{
		"space":  play,
		"c":      cue,
		"s":      {Action: "SyncToggle"},
		"q":      {Action: "Quantize"},
		"[":      {Action: "DeckPitch", Value: "dec"},
		"]":      {Action: "DeckPitch", Value: "inc"},
		"ctrl+c": {Action: "Quit"},
		"z":      {Action: "ZoomToggle"},
		"v":      {Action: "ViewNext"},
		"/":      {Action: "LibraryOpen"},
		"r":      {Action: "LoopRoll", Value: "1"}, // toggled by the TUI; see rollActive

		// ESC is deliberately NOT bound here. The TUI handles it as a universal
		// exit before any binding lookup (internal/tui/keys.go), so a binding on
		// it could never fire: it named LibraryClose and was resolved and
		// discarded on every press. It lived here only to give the help screen a
		// description, and uiKeys does that now. A key the config claims but
		// cannot own is worse than no key at all -- it invites a user to rebind
		// something that will never happen.

		// Hotcues. Without these no cue point was reachable from the keyboard:
		// MIDI mappings carry a slot and key bindings could not, so the whole
		// hotcue feature was keyboard-invisible.
		"1": {Action: "DeckHotcueSet", Slot: 0},
		"2": {Action: "DeckHotcueJump", Slot: 0},
		"3": {Action: "DeckHotcueSet", Slot: 1},
		"4": {Action: "DeckHotcueJump", Slot: 1},
		"5": {Action: "DeckHotcueSet", Slot: 2},
		"6": {Action: "DeckHotcueJump", Slot: 2},
		"7": {Action: "DeckHotcueSet", Slot: 3},
		"8": {Action: "DeckHotcueJump", Slot: 3},

		// Loops.
		"9":  {Action: "LoopIn"},
		"0":  {Action: "LoopOut"},
		"w":  {Action: "LoopBeat", Value: "2"},
		"e":  {Action: "LoopBeat", Value: "4"},
		"y":  {Action: "LoopBeat", Value: "8"},
		"u":  {Action: "LoopBeat", Value: "16"},
		"i":  {Action: "LoopSave", Slot: 0},
		"o":  {Action: "LoopRecall", Slot: 0},
		"\\": {Action: "LoopRelease"},

		// Mixer.
		"m": {Action: "CueMix", Value: "toggle"},
		"-": {Action: "MasterGain", Value: "dec"},
		"=": {Action: "MasterGain", Value: "inc"},
	}
}

// NewKeyResolver compiles a config's keybindings into a resolver.
//
// The defaults are merged first and the user's file second, so a user
// rebinding a shipped key overrides it. A key bound twice *within the user's own
// map* is a mistake worth reporting rather than silently resolving to one of
// them.
func NewKeyResolver(cfg *Config) (*KeyResolver, error) {
	merged := DefaultKeys()
	for k, v := range cfg.Keys {
		merged[k] = v
	}

	r := &KeyResolver{
		bindings: make(map[string]compiled, len(merged)),
		toggles:  make(map[string]bool),
	}
	// A toggle alternates 1/0 and starts from "off", which means its FIRST
	// press always emits 1. For most controls that is right, because the
	// engine starts them at 0. For these the engine starts them already at 1,
	// so the first press re-sent the value they already had and the key looked
	// dead until it was pressed twice.
	for k := range engagedByDefault {
		if b, ok := merged[k]; ok && strings.EqualFold(strings.TrimSpace(b.Value), "toggle") {
			r.toggles[k] = true
		}
	}
	for name, kb := range merged {
		c, err := compileBinding(name, kb)
		if err != nil {
			return nil, err
		}
		r.bindings[name] = c
	}
	return r, nil
}

func compileBinding(name string, kb KeyBinding) (compiled, error) {
	kind, err := parseAction(kb.Action)
	if err != nil {
		return compiled{}, fmt.Errorf("key %q: %w", name, err)
	}
	c := compiled{
		action:    kind,
		target:    kb.DeckTarget(),
		hasTarget: kb.Target != nil,
		// Slot was declared on compiled and never assigned, so a keybinding
		// could not name a hotcue or loop slot. Every slot-taking action was
		// therefore MIDI-only, and the README's claim that every action is
		// reachable from the keyboard was false for all of them.
		slot:   kb.Slot,
		repeat: kb.Repeat,
	}
	switch strings.ToLower(strings.TrimSpace(kb.Value)) {
	case "":
		c.literal = 1
	case "toggle":
		c.toggle = true
	case "inc":
		c.inc = true
	case "dec":
		c.dec = true
	default:
		v, err := strconv.ParseFloat(kb.Value, 64)
		if err != nil {
			return compiled{}, fmt.Errorf("key %q: value %q is not toggle/inc/dec or a number", name, kb.Value)
		}
		c.literal = v
	}
	return c, nil
}

// parseAction maps a config action name to an engine.ActionKind via the midi
// vocabulary, so keys and controllers speak exactly one language and cannot
// drift apart.
func parseAction(name string) (engine.ActionKind, error) {
	return midi.ParseAction(name)
}

// Resolve returns the Action for a key press, advancing any toggle state.
//
// An unbound key reports false rather than a zero action, so a caller can tell
// "no such binding" from "bound to nothing".
func (r *KeyResolver) Resolve(key string) (engine.Action, bool) {
	c, ok := r.bindings[key]
	if !ok {
		return engine.Action{}, false
	}
	value := c.literal
	if c.toggle {
		// Alternate 1/0 per press so a cue does not stick on.
		r.toggles[key] = !r.toggles[key]
		if r.toggles[key] {
			value = 1
		} else {
			value = 0
		}
	}
	relative := false
	if c.inc {
		value = 1
		relative = true
	}
	if c.dec {
		value = -1
		relative = true
	}
	return engine.Action{Kind: c.action, Target: c.target, Value: value,
		Slot: c.slot, Relative: relative}, true
}

// Repeats reports whether a binding should fire on auto-repeat while held.
func (r *KeyResolver) Repeats(key string) bool {
	c, ok := r.bindings[key]
	return ok && c.repeat
}

// keyDescriptions give the help view something to say about each key. Without
// these the help screen is a list of bare key names, which tells a user
// nothing they did not already know.
//
// Only rebindable keys belong here. The navigation keys are listed in uiKeys
// instead: a row here would be consulted even for a key the user has rebound,
// so binding "l" to a real action would still be described as the quick-load
// overlay -- a help screen that contradicts what the key does.
var keyDescriptions = map[string]string{
	"space":  "play/pause focused deck",
	"c":      "cue focused deck",
	"s":      "sync toggle",
	"q":      "quantize to beat",
	"[":      "pitch down",
	"]":      "pitch up",
	"r":      "loop roll",
	"z":      "zoom toggle",
	"v":      "cycle layout",
	"/":      "library",
	"ctrl+c": "quit",
}

// Binding describes one key and what it does, for the help view.
type Binding struct {
	Key         string
	Description string
}

// Describe returns the bound keys with descriptions, sorted by key.
func (r *KeyResolver) Describe() []Binding {
	out := make([]Binding, 0, len(r.bindings))
	for _, line := range r.DescribeLines() {
		k, desc := splitBinding(line)
		out = append(out, Binding{Key: k, Description: desc})
	}
	return out
}

func splitBinding(b string) (key, desc string) {
	if i := strings.Index(b, "  "); i >= 0 {
		return strings.TrimSpace(b[:i]), strings.TrimSpace(b[i:])
	}
	return strings.TrimSpace(b), ""
}

// uiKeys are the navigation keys the TUI handles before any config lookup.
//
// They are checked first and cannot be rebound (see internal/tui/keys.go), so
// nothing else records them: a key that a user can press but that appears in
// no binding table is a key the help screen would omit. "l" and "tab" were
// already in this position and missing from the help listing. "escape" joined
// them when its dead LibraryClose binding was removed.
var uiKeys = []struct{ key, desc string }{
	{"tab", "move deck focus"},
	{"shift+tab", "move deck focus back"},
	{"l", "quick-load overlay"},
	{"S", "settings: library root"},
	{"?", "this help"},
	{"`", "debug overlay"},
	{"escape", "close the browser, overlay or settings"},
	{"Z", "zoom level"},
}

// DescribeLines returns the help lines as "key  description", sorted.
//
// The result is cached: the binding table only changes when a new resolver is
// built, and the view asked for this on every rendered frame.
func (r *KeyResolver) DescribeLines() []string {
	if r.lines != nil {
		return r.lines
	}
	seen := make(map[string]bool, len(r.bindings)+len(uiKeys))
	out := make([]string, 0, len(r.bindings)+len(uiKeys))
	for k := range r.bindings {
		desc := keyDescriptions[k]
		if desc == "" {
			desc = r.bindings[k].action.String()
		}
		seen[k] = true
		out = append(out, k+"  "+desc)
	}
	// A rebindable binding wins the description. If the user moved "l" onto a
	// real action, the hardcoded row for "l" would be wrong, and the binding
	// table is the one that actually fires.
	for _, u := range uiKeys {
		if seen[u.key] {
			continue
		}
		out = append(out, u.key+"  "+u.desc)
	}
	sort.Strings(out)
	r.lines = out
	return out
}

// Bindings returns the bound key names, sorted, for a help view.
func (r *KeyResolver) Bindings() []string {
	out := make([]string, 0, len(r.bindings))
	for k := range r.bindings {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// engagedByDefault lists the toggle bindings whose engine control starts at 1
// rather than 0.
//
// engine.Mixer's cueMix is documented as "0 is cue-only, 1 is program" and
// initialised to 1. Seeding the toggle from that makes the first press move
// the control to 0, which is what pressing it means. Without the seed, `m`
// sent the value the mixer was already at and nothing on screen moved.
var engagedByDefault = map[string]bool{
	"m": true,
}
