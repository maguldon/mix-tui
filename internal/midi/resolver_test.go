package midi

import (
	"sort"
	"testing"

	"github.com/maguldon/mix-tui/internal/engine"
)

func tableFrom(t *testing.T, y string) *Table {
	t.Helper()
	m, err := ParseMapping("x.yaml", []byte(y))
	if err != nil {
		t.Fatal(err)
	}
	tab, err := m.Compile()
	if err != nil {
		t.Fatal(err)
	}
	return tab
}

const chordMapping = `
device: "T"
modifiers:
  shift: {kind: cc, chan: 1, num: 49}
  ctrl: {kind: cc, chan: 1, num: 50}
controls:
  - midi: {kind: cc, chan: 1, num: 40}
    action: {kind: DeckHotcueSet, target: 0}
    slot: 1
  - midi: {kind: cc, chan: 1, num: 40}
    mods: [shift]
    action: {kind: DeckHotcueDelete, target: 0}
    slot: 1
  - midi: {kind: cc, chan: 1, num: 40}
    mods: [shift, ctrl]
    action: {kind: DeckHotcueJump, target: 0}
    slot: 1
`

func TestResolveNoModifiers(t *testing.T) {
	tab := tableFrom(t, chordMapping)
	c, ok := tab.Resolve(ModSet{}, Message{Kind: KindCC, Channel: 1, Num: 40})
	if !ok {
		t.Fatal("unmodified not bound")
	}
	if c.Action != engine.DeckHotcueSet {
		t.Errorf("got %v", c.Action)
	}
}

func TestResolveShiftOverridesBase(t *testing.T) {
	// Review Focus #2: one physical button, three behaviours
	tab := tableFrom(t, chordMapping)
	c, _ := tab.Resolve(ModSet{"shift": true}, Message{Kind: KindCC, Channel: 1, Num: 40})
	if c.Action != engine.DeckHotcueDelete {
		t.Errorf("shift gave %v, want DeckHotcueDelete", c.Action)
	}
}

func TestResolveShiftCtrlBeatsShift(t *testing.T) {
	tab := tableFrom(t, chordMapping)
	c, _ := tab.Resolve(ModSet{"shift": true, "ctrl": true},
		Message{Kind: KindCC, Channel: 1, Num: 40})
	if c.Action != engine.DeckHotcueJump {
		t.Errorf("shift+ctrl gave %v, want DeckHotcueJump", c.Action)
	}
}

func TestResolveCtrlAloneFallsBackToBase(t *testing.T) {
	// ctrl is held but not bound: must fall through shift to empty, not fail
	tab := tableFrom(t, chordMapping)
	c, ok := tab.Resolve(ModSet{"ctrl": true}, Message{Kind: KindCC, Channel: 1, Num: 40})
	if !ok {
		t.Fatal("ctrl-only should fall back to the base binding")
	}
	if c.Action != engine.DeckHotcueSet {
		t.Errorf("ctrl-only gave %v, want the base binding", c.Action)
	}
}

func TestResolveUnboundModifierSetFallsBack(t *testing.T) {
	tab := tableFrom(t, chordMapping)
	_, ok := tab.Resolve(ModSet{"alt": true}, Message{Kind: KindCC, Channel: 1, Num: 40})
	if !ok {
		t.Fatal("unknown modifiers must still reach the base binding")
	}
}

func TestResolveUnmappedMessageReportsFalse(t *testing.T) {
	tab := tableFrom(t, chordMapping)
	if _, ok := tab.Resolve(ModSet{}, Message{Kind: KindCC, Channel: 1, Num: 99}); ok {
		t.Error("an unbound CC must not resolve")
	}
}

func TestResolveIsIndependentOfHeldModOrder(t *testing.T) {
	tab := tableFrom(t, chordMapping)
	a, _ := tab.Resolve(ModSet{"ctrl": true, "shift": true}, Message{Kind: KindCC, Channel: 1, Num: 40})
	b, _ := tab.Resolve(ModSet{"shift": true, "ctrl": true}, Message{Kind: KindCC, Channel: 1, Num: 40})
	if a.Action != b.Action {
		t.Error("resolution depends on map iteration order")
	}
}

func TestModSetCopiesAreIndependent(t *testing.T) {
	base := ModSet{"shift": true}
	other := base.With("ctrl")
	if base["ctrl"] {
		t.Error("With mutated the receiver")
	}
	if !other["shift"] || !other["ctrl"] {
		t.Error("With did not copy")
	}
}

// TestResolveShiftCtrlSameSizeTieIsDeterministic covers the ambiguous case the
// plan calls out: two subsets of the same size both potentially matching must
// resolve identically regardless of map order. With ctrl+alt held and only
// ctrl bound, resolution must still be stable.
func TestResolveSameSizeTieIsDeterministic(t *testing.T) {
	y := `
device: "T"
modifiers:
  ctrl: {kind: cc, chan: 1, num: 49}
  alt: {kind: cc, chan: 1, num: 50}
controls:
  - midi: {kind: cc, chan: 1, num: 40}
    mods: [ctrl]
    action: {kind: DeckHotcueSet, target: 0}
  - midi: {kind: cc, chan: 1, num: 40}
    mods: [alt]
    action: {kind: DeckHotcueJump, target: 0}
`
	tab := tableFrom(t, y)
	msg := Message{Kind: KindCC, Channel: 1, Num: 40}
	a, _ := tab.Resolve(ModSet{"ctrl": true, "alt": true}, msg)
	b, _ := tab.Resolve(ModSet{"alt": true, "ctrl": true}, msg)
	if a.Action != b.Action {
		t.Errorf("same held set resolved differently: %v vs %v", a.Action, b.Action)
	}
	// Determinism means: within one subset size, subsets are tried in sorted
	// name order. "alt" sorts before "ctrl", so alt's binding is reached first
	// and wins the tie. The point is that it wins the SAME way every time, not
	// that a particular one wins.
	if a.Action != engine.DeckHotcueJump {
		t.Errorf("tie broke to %v, want the alphabetically-first (alt) binding", a.Action)
	}
}

// TestModSetWithoutAndEmpty covers the remaining ModSet API.
func TestModSetWithoutAndEmpty(t *testing.T) {
	m := ModSet{"shift": true, "ctrl": true}
	if m.Empty() {
		t.Error("non-empty reported empty")
	}
	w := m.Without("shift")
	if w["shift"] {
		t.Error("Without did not remove")
	}
	if !w["ctrl"] {
		t.Error("Without removed the wrong key")
	}
	if !m["shift"] {
		t.Error("Without mutated the receiver")
	}
	if got := m.Canonical(); got != "ctrl,shift" {
		t.Errorf("Canonical = %q, want %q", got, "ctrl,shift")
	}
	if got := (ModSet{}).Canonical(); got != "" {
		t.Errorf("empty Canonical = %q, want empty", got)
	}
}

// TestResolveEmptySetReachesBase documents that resolution always terminates at
// the empty modifier set, which is what makes a mapping file total: no matter
// which modifiers are stuck, a bound message still does something.
func TestResolveEmptySetReachesBase(t *testing.T) {
	tab := tableFrom(t, chordMapping)
	// Five modifiers held, none bound except base: must still resolve.
	_, ok := tab.Resolve(ModSet{"a": true, "b": true, "c": true, "d": true, "e": true},
		Message{Kind: KindCC, Channel: 1, Num: 40})
	if !ok {
		t.Fatal("many unbound modifiers must still fall back to base")
	}
}

// TestModSetNamesIsSorted guards the determinism input to Resolve.
func TestModSetNamesIsSorted(t *testing.T) {
	n := ModSet{"z": true, "a": true, "m": true}.Names()
	if !sort.StringsAreSorted(n) {
		t.Errorf("Names not sorted: %v", n)
	}
}
