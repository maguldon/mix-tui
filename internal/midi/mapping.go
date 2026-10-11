package midi

import (
	"bytes"
	"fmt"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/maguldon/mix-tui/internal/engine"
)

// ValueMode describes how a control's Value field is interpreted and whether
// the control is momentary (active while held) or toggle (flips on each press).
type ValueMode int

const (
	ModeAbsolute ValueMode = iota
	ModeRelative
	ModeMomentary
	ModeToggle
)

// ModMode is how a modifier button behaves once engaged.
type ModMode int

const (
	ModMomentary ModMode = iota
	ModLatching
	ModToggleMod
)

// ParseModMode resolves a modifier mode spelling. It is separate from ModMode's
// String because the YAML vocabulary ("momentary") and the internal name differ
// to avoid colliding with ValueMode's ModeMomentary.
func ParseModMode(s string) (ModMode, error) {
	switch strings.ToLower(s) {
	case "momentary", "":
		return ModMomentary, nil
	case "latching":
		return ModLatching, nil
	case "toggle":
		return ModToggleMod, nil
	}
	return 0, fmt.Errorf("unknown modifier mode %q (want momentary, latching, or toggle)", s)
}

// ModSource declares which physical control acts as a named modifier and how it
// latches.
type ModSource struct {
	Kind     MsgKind
	Chan     uint8
	Num      uint8
	Pressed  uint8 // cc: value meaning down
	Released uint8 // cc: value meaning up
	ModMode  ModMode
}

// Control is one bound input.
type Control struct {
	Kinds  MsgKind
	Chan   uint8
	Num    uint8
	Mods   []string
	Action engine.ActionKind
	Target int
	Slot   int
	Mode   ValueMode
}

// MatchKind selects how a device name is compared.
type MatchKind int

const (
	MatchSubstring MatchKind = iota
	MatchExact
)

// DeviceMapping is one parsed mapping file.
type DeviceMapping struct {
	Device    string
	Match     MatchKind
	Modifiers map[string]ModSource
	Controls  []Control
	// Raw retains the source document so a future save can round-trip without
	// discarding fields this schema does not yet model.
	Raw yaml.Node
}

// ModSet is the set of currently-held modifier names. The zero value (a nil
// map) reads as an empty set, so an uninitialised ModSet behaves correctly
// rather than panicking. Methods are in resolver.go.
type ModSet map[string]bool

// msgKey identifies a physical message within a table.
type msgKey struct {
	Kind MsgKind
	Chan uint8
	Num  uint8
}

func (k msgKey) String() string {
	name := "cc"
	if k.Kind == KindNote {
		name = "note"
	}
	return fmt.Sprintf("%s ch%d n%d", name, k.Chan, k.Num)
}

// Table is the hot-path lookup structure. Immutable once built, so hot reload
// swaps it with a single pointer store (Task 5).
type Table struct {
	Device string
	Match  MatchKind
	mods   map[string]ModSource
	// byMods is keyed by canonical modifier-set string, then message.
	byMods map[string]map[msgKey]Control
}

// ---- YAML wire types -------------------------------------------------------

type yamlMapping struct {
	Device    string             `yaml:"device"`
	Match     string             `yaml:"match"`
	Modifiers map[string]yamlMod `yaml:"modifiers"`
	Controls  []yamlControl      `yaml:"controls"`
}

type yamlMod struct {
	Kind     string `yaml:"kind"`
	Chan     uint8  `yaml:"chan"`
	Num      uint8  `yaml:"num"`
	Pressed  *uint8 `yaml:"pressed"`
	Released *uint8 `yaml:"released"`
	Mode     string `yaml:"mode"`
}

type yamlControl struct {
	MIDI      yamlMIDI   `yaml:"midi"`
	Mods      []string   `yaml:"mods"`
	Action    yamlAction `yaml:"action"`
	Slot      int        `yaml:"slot"`
	Mode      string     `yaml:"mode"`
	Momentary bool       `yaml:"momentary"`
}

type yamlMIDI struct {
	Kind string `yaml:"kind"`
	Chan uint8  `yaml:"chan"`
	Num  uint8  `yaml:"num"`
}

type yamlAction struct {
	Kind   string `yaml:"kind"`
	Target int    `yaml:"target"`
}

// ---- parsing ---------------------------------------------------------------

func parseMsgKind(s string) (MsgKind, error) {
	switch strings.ToLower(s) {
	case "cc":
		return KindCC, nil
	case "note":
		return KindNote, nil
	}
	return 0, fmt.Errorf("unknown message kind %q (want cc or note)", s)
}

// ParseMapping parses one mapping file. Unknown YAML fields are errors, not
// warnings: these files are user-editable, and a silently ignored "mods:" typo
// produces a controller that mysteriously does nothing (spec §5.3).
//
// Errors carry the file name and a line number so a bad mapping is fixable
// without guessing.
func ParseMapping(path string, data []byte) (*DeviceMapping, error) {
	// Decode twice: once into a Node (to learn each control's source line, which
	// our own validation errors must report) and once into the typed struct.
	// yaml.v3 does not surface field line numbers through the typed decoder, and
	// a mapping error without a line number is not fixable without guessing.
	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	controlLines := controlLineNumbers(&root)

	var ym yamlMapping
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&ym); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if ym.Device == "" {
		return nil, fmt.Errorf("%s: device is required", path)
	}

	m := &DeviceMapping{Device: ym.Device, Match: MatchSubstring}
	switch strings.ToLower(ym.Match) {
	case "", "substring":
		m.Match = MatchSubstring
	case "exact":
		m.Match = MatchExact
	default:
		return nil, fmt.Errorf("%s: unknown match mode %q (want substring or exact)", path, ym.Match)
	}

	m.Modifiers = make(map[string]ModSource, len(ym.Modifiers))
	for name, y := range ym.Modifiers {
		kind, err := parseMsgKind(y.Kind)
		if err != nil {
			return nil, fmt.Errorf("%s: modifier %q: %w", path, name, err)
		}
		mode, err := ParseModMode(y.Mode)
		if err != nil {
			return nil, fmt.Errorf("%s: modifier %q: %w", path, name, err)
		}
		src := ModSource{Kind: kind, Chan: y.Chan, Num: y.Num, ModMode: mode}
		// A cc modifier needs to distinguish pressed from released. Default the
		// released value to 0 when only pressed is given, which covers the
		// common "value >= pressed" convention without forcing every file to
		// spell out both.
		if y.Pressed != nil {
			src.Pressed = *y.Pressed
		}
		if y.Released != nil {
			src.Released = *y.Released
		}
		m.Modifiers[name] = src
	}

	for i, yc := range ym.Controls {
		kind, err := parseMsgKind(yc.MIDI.Kind)
		if err != nil {
			return nil, fmt.Errorf("%s: line %d: control %d: %w", path, controlLines.line(i), i, err)
		}
		act, err := ParseActionKind(yc.Action.Kind)
		if err != nil {
			return nil, fmt.Errorf("%s: line %d: control %d: %w", path, controlLines.line(i), i, err)
		}
		// Validate modifier references now: a control naming an undeclared
		// modifier would otherwise compile into an unreachable binding.
		for _, mod := range yc.Mods {
			if _, ok := m.Modifiers[mod]; !ok {
				return nil, fmt.Errorf("%s: line %d: control %d references undeclared modifier %q", path, controlLines.line(i), i, mod)
			}
		}

		mode := ModeAbsolute
		if yc.Momentary {
			mode = ModeMomentary
		} else if yc.Mode != "" {
			mode, err = parseValueMode(yc.Mode)
			if err != nil {
				return nil, fmt.Errorf("%s: line %d: control %d: %w", path, controlLines.line(i), i, err)
			}
		}

		m.Controls = append(m.Controls, Control{
			Kinds:  kind,
			Chan:   yc.MIDI.Chan,
			Num:    yc.MIDI.Num,
			Mods:   yc.Mods,
			Action: act,
			Target: yc.Action.Target,
			Slot:   yc.Slot,
			Mode:   mode,
		})
	}
	return m, nil
}

func parseValueMode(s string) (ValueMode, error) {
	switch strings.ToLower(s) {
	case "absolute":
		return ModeAbsolute, nil
	case "relative":
		return ModeRelative, nil
	case "momentary":
		return ModeMomentary, nil
	case "toggle":
		return ModeToggle, nil
	}
	return 0, fmt.Errorf("unknown mode %q (want absolute, relative, momentary, or toggle)", s)
}

// lineIndex reports the source line of the nth item in a sequence, or 0 when
// unknown. Error text falls back gracefully if the document shape is odd.
type lineIndex []int

func (l lineIndex) line(i int) int {
	if i < 0 || i >= len(l) {
		return 0
	}
	return l[i]
}

// controlLineNumbers extracts the line of each entry under the top-level
// "controls" sequence.
func controlLineNumbers(root *yaml.Node) lineIndex {
	var idx lineIndex
	if root == nil || len(root.Content) == 0 {
		return idx
	}
	doc := root.Content[0]
	if doc.Kind != yaml.MappingNode {
		return idx
	}
	for i := 0; i+1 < len(doc.Content); i += 2 {
		key, val := doc.Content[i], doc.Content[i+1]
		if key.Value != "controls" {
			continue
		}
		if val.Kind == yaml.SequenceNode {
			for _, item := range val.Content {
				idx = append(idx, item.Line)
			}
		}
		return idx
	}
	return idx
}

// canonicalMods sorts a modifier set into a stable comma-joined key so
// ["shift","ctrl"] and ["ctrl","shift"] resolve to the same bucket (and are
// caught as duplicates at compile time).
func canonicalMods(mods []string) string {
	if len(mods) == 0 {
		return ""
	}
	s := append([]string(nil), mods...)
	sort.Strings(s)
	return strings.Join(s, ",")
}

// Compile turns a parsed mapping into a hot-path Table, rejecting duplicate
// (modifier-set, message) bindings.
func (m *DeviceMapping) Compile() (*Table, error) {
	t := &Table{
		Device: m.Device,
		Match:  m.Match,
		mods:   m.Modifiers,
		byMods: make(map[string]map[msgKey]Control),
	}
	for _, c := range m.Controls {
		key := canonicalMods(c.Mods)
		bucket := t.byMods[key]
		if bucket == nil {
			bucket = make(map[msgKey]Control)
			t.byMods[key] = bucket
		}
		mk := msgKey{Kind: c.Kinds, Chan: c.Chan, Num: c.Num}
		if _, dup := bucket[mk]; dup {
			return nil, fmt.Errorf("duplicate binding for %s with mods [%s]", mk, key)
		}
		bucket[mk] = c
	}
	return t, nil
}

// Matches reports whether a connected device name is governed by this table.
// Substring matching is the default because controllers append a port suffix
// ("Xone K2 96") that varies between reconnects (review focus #5).
func (t *Table) Matches(name string) bool {
	if t.Match == MatchExact {
		return name == t.Device
	}
	return strings.Contains(name, t.Device)
}

// Lookup finds the control bound to msg under the held modifier set mods.
func (t *Table) Lookup(mods ModSet, msg Message) (Control, bool) {
	// Try the held set first, then progressively drop modifiers. Task 3
	// generalises this to "most specific wins"; here we only need the direct
	// hit plus the empty-set fallback.
	keys := []string{""}
	if len(mods) > 0 {
		held := make([]string, 0, len(mods))
		for name, on := range mods {
			if on {
				held = append(held, name)
			}
		}
		keys = append([]string{canonicalMods(held)}, keys...)
	}
	k := msgKey{Kind: msg.Kind, Chan: msg.Channel, Num: msg.Num}
	for _, key := range keys {
		if bucket := t.byMods[key]; bucket != nil {
			if c, ok := bucket[k]; ok {
				return c, true
			}
		}
	}
	return Control{}, false
}

// ModSources exposes the modifier table (for the UI/state machine).
func (t *Table) ModSources() map[string]ModSource { return t.mods }
