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
	slot   int
	repeat bool
	// strategy is how Value is produced: a literal, or a toggle that alternates.
	toggle  bool
	inc     bool
	dec     bool
	literal float64
}

// DefaultKeys is the shipped keymap. It is merged under the user's file, so a
// user rebinding one of these keys overrides it rather than colliding.
func DefaultKeys() map[string]KeyBinding {
	play := KeyBinding{Action: "DeckPlay"}
	cue := KeyBinding{Action: "DeckCue", Value: "toggle"}
	return map[string]KeyBinding{
		"space":  play,
		"c":      cue,
		"s":      {Action: "SyncToggle"},
		"q":      {Action: "Quantize"},
		"[":      {Action: "DeckPitch", Target: 0, Value: "dec"},
		"]":      {Action: "DeckPitch", Target: 0, Value: "inc"},
		"ctrl+c": {Action: "Quit"},
		"z":      {Action: "ZoomToggle"},
		"v":      {Action: "ViewNext"},
		"/":      {Action: "LibraryOpen"},
		"escape": {Action: "LibraryClose"},
		"r":      {Action: "LoopRoll"},
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
		action: kind,
		target: kb.Target,
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
	if c.inc {
		value = 1
	}
	if c.dec {
		value = -1
	}
	return engine.Action{Kind: c.action, Target: c.target, Value: value}, true
}

// Repeats reports whether a binding should fire on auto-repeat while held.
func (r *KeyResolver) Repeats(key string) bool {
	c, ok := r.bindings[key]
	return ok && c.repeat
}

// keyDescriptions give the help view something to say about each key. Without
// these the help screen is a list of bare key names, which tells a user
// nothing they did not already know.
var keyDescriptions = map[string]string{
	"space":  "play/pause focused deck",
	"c":      "cue focused deck",
	"s":      "sync toggle",
	"q":      "quantize to beat",
	"[":      "pitch down",
	"]":      "pitch up",
	"r":      "loop roll",
	"z":      "zoom toggle",
	"Z":      "zoom level",
	"v":      "cycle layout",
	"/":      "library",
	"l":      "quick-load overlay",
	"tab":    "move deck focus",
	"escape": "close overlay",
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

// DescribeLines returns the help lines as "key  description", sorted.
//
// The result is cached: the binding table only changes when a new resolver is
// built, and the view asked for this on every rendered frame.
func (r *KeyResolver) DescribeLines() []string {
	if r.lines != nil {
		return r.lines
	}
	out := make([]string, 0, len(r.bindings))
	for k := range r.bindings {
		desc := keyDescriptions[k]
		if desc == "" {
			desc = r.bindings[k].action.String()
		}
		out = append(out, k+"  "+desc)
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
