package midi

import (
	"testing"

	"github.com/maguldon/mix-tui/internal/engine"
)

func modifierMapping(t *testing.T) *Table {
	return tableFrom(t, `
device: "T"
modifiers:
  shift:
    kind: cc
    chan: 1
    num: 49
    pressed: 127
    released: 0
    mode: momentary
  lock:
    kind: note
    chan: 1
    num: 50
    mode: toggle
controls:
  - midi: {kind: cc, chan: 1, num: 40}
    action: {kind: DeckHotcueSet, target: 0}
    slot: 1
  - midi: {kind: cc, chan: 1, num: 40}
    mods: [shift]
    action: {kind: DeckHotcueDelete, target: 0}
    slot: 1
`)
}

func TestMomentaryModifierPressAndRelease(t *testing.T) {
	h := NewHandler(modifierMapping(t), func(a engine.Action) { last = a })
	h.Handle(Message{Kind: KindCC, Channel: 1, Num: 49, Value: 127}) // shift down
	if !h.ActiveMods()["shift"] {
		t.Fatal("shift not held after press")
	}
	h.Handle(Message{Kind: KindCC, Channel: 1, Num: 40, Value: 127})
	if last.Kind != engine.DeckHotcueDelete {
		t.Errorf("got %v", last.Kind)
	}
	h.Handle(Message{Kind: KindCC, Channel: 1, Num: 49, Value: 0}) // shift up
	if h.ActiveMods()["shift"] {
		t.Error("shift still held after release")
	}
	h.Handle(Message{Kind: KindCC, Channel: 1, Num: 40, Value: 127})
	if last.Kind != engine.DeckHotcueSet {
		t.Errorf("after release got %v", last.Kind)
	}
}

func TestNoteModifierUsesNoteOnOff(t *testing.T) {
	h := NewHandler(modifierMapping(t), func(a engine.Action) { last = a })
	h.Handle(Message{Kind: KindNote, Channel: 1, Num: 50, Velocity: 100}) // lock on
	h.Handle(Message{Kind: KindCC, Channel: 1, Num: 40, Value: 127})
	_ = last // 'lock' is not bound to the action, so it falls back to base
	if last.Kind != engine.DeckHotcueSet {
		t.Errorf("an unbound modifier must fall back, got %v", last.Kind)
	}
	h.Handle(Message{Kind: KindNote, Channel: 1, Num: 50, Velocity: 0}) // lock off
}

func TestToggleModifierTogglesOnEachPress(t *testing.T) {
	// Review Focus #3: note-off must NOT clear a toggled modifier
	h := NewHandler(modifierMapping(t), func(a engine.Action) { last = a })
	h.Handle(Message{Kind: KindNote, Channel: 1, Num: 50, Velocity: 100})
	if !h.ActiveMods()["lock"] {
		t.Fatal("toggle did not engage")
	}
	h.Handle(Message{Kind: KindNote, Channel: 1, Num: 50, Velocity: 0}) // note-off
	if !h.ActiveMods()["lock"] {
		t.Error("note-off cleared a toggled modifier — must only toggle on press")
	}
	h.Handle(Message{Kind: KindNote, Channel: 1, Num: 50, Velocity: 100}) // press again
	if h.ActiveMods()["lock"] {
		t.Error("second press did not disengage")
	}
}

func TestMomentaryActionFiresOnRelease(t *testing.T) {
	m, _ := ParseMapping("x.yaml", []byte(`
device: "T"
controls:
  - midi: {kind: cc, chan: 1, num: 20}
    action: {kind: DeckCue, target: 0}
    momentary: true
`))
	tab, _ := m.Compile()
	h := NewHandler(tab, func(a engine.Action) { last = a })
	h.Handle(Message{Kind: KindCC, Channel: 1, Num: 20, Value: 127})
	if last.Value != 1 {
		t.Errorf("press emitted %v, want 1", last.Value)
	}
	h.Handle(Message{Kind: KindCC, Channel: 1, Num: 20, Value: 0})
	if last.Value != 0 {
		t.Errorf("release emitted %v, want 0", last.Value)
	}
}

// TestModifierMessageDoesNotAlsoFireControl ensures a modifier key press does
// not additionally trigger whatever control happens to share its CC. If it did,
// holding shift would fire the shift control itself.
func TestModifierMessageDoesNotAlsoFireControl(t *testing.T) {
	h := NewHandler(modifierMapping(t), func(a engine.Action) { last = a })
	fired := 0
	h2 := NewHandler(modifierMapping(t), func(a engine.Action) { last = a; fired++ })
	_ = h
	h2.Handle(Message{Kind: KindCC, Channel: 1, Num: 49, Value: 127}) // shift press
	if fired != 0 {
		t.Errorf("modifier press also fired an action (%d)", fired)
	}
}

// TestLatchingModifierTracksOwnParity covers review focus #3's sibling: a
// latching modifier must not follow note-off, and must disengage only on the
// second press.
func TestLatchingModifierTracksOwnParity(t *testing.T) {
	tab := tableFrom(t, `
device: "T"
modifiers:
  lock:
    kind: note
    chan: 1
    num: 50
    mode: latching
controls:
  - midi: {kind: cc, chan: 1, num: 40}
    action: {kind: DeckHotcueSet, target: 0}
`)
	h := NewHandler(tab, func(a engine.Action) { last = a })
	h.Handle(Message{Kind: KindNote, Channel: 1, Num: 50, Velocity: 100})
	if !h.ActiveMods()["lock"] {
		t.Fatal("latching did not engage on first press")
	}
	// Note-off must not disengage a latching modifier.
	h.Handle(Message{Kind: KindNote, Channel: 1, Num: 50, Velocity: 0})
	if !h.ActiveMods()["lock"] {
		t.Error("note-off cleared a latched modifier")
	}
	// Second press disengages.
	h.Handle(Message{Kind: KindNote, Channel: 1, Num: 50, Velocity: 100})
	if h.ActiveMods()["lock"] {
		t.Error("second press did not disengage")
	}
}

// TestUnmappedMessageIsCounted ensures a bound table that does not describe the
// controller in hand reports it, rather than doing nothing silently.
func TestUnmappedMessageIsCounted(t *testing.T) {
	h := NewHandler(modifierMapping(t), func(a engine.Action) { last = a })
	err := h.Handle(Message{Kind: KindCC, Channel: 1, Num: 99, Value: 64})
	if err == nil {
		t.Error("unmapped CC must be reported")
	}
	if h.Counts()["unmapped"] == 0 {
		t.Error("unmapped message was not tallied")
	}
}

// TestIgnoredCategoryIsCountedAndReported covers spec §7: sysex and clock are
// ignored but counted.
func TestIgnoredCategoryIsCountedAndReported(t *testing.T) {
	h := NewHandler(modifierMapping(t), func(a engine.Action) { last = a })
	if err := h.Handle(Message{Kind: KindOther, Other: OtherClock}); err == nil {
		t.Error("clock must be reported as ignored")
	}
	if h.Counts()["clock"] == 0 {
		t.Error("clock was not tallied")
	}
}

// TestActiveModsIsACopy ensures the caller cannot mutate handler state through
// the returned set.
func TestActiveModsIsACopy(t *testing.T) {
	h := NewHandler(modifierMapping(t), func(a engine.Action) { last = a })
	h.Handle(Message{Kind: KindCC, Channel: 1, Num: 49, Value: 127})
	got := h.ActiveMods()
	got["injected"] = true
	if h.ActiveMods()["injected"] {
		t.Error("ActiveMods returned the live set, not a copy")
	}
}

// TestSetTableSwapsMappings covers the hot-reload entry point at the handler
// level: a swapped table must immediately govern resolution.
func TestSetTableSwapsMappings(t *testing.T) {
	h := NewHandler(modifierMapping(t), func(a engine.Action) { last = a })
	h.Handle(Message{Kind: KindCC, Channel: 1, Num: 40, Value: 127})
	if last.Kind != engine.DeckHotcueSet {
		t.Fatalf("initial table gave %v", last.Kind)
	}
	replaced := tableFrom(t, `
device: "T"
controls:
  - midi: {kind: cc, chan: 1, num: 40}
    action: {kind: DeckPlay, target: 0}
`)
	h.SetTable(replaced)
	h.Handle(Message{Kind: KindCC, Channel: 1, Num: 40, Value: 127})
	if last.Kind != engine.DeckPlay {
		t.Errorf("after swap got %v, want DeckPlay", last.Kind)
	}
}

// TestReleasedIsHonoured covers a field that parsed, validated, documented and
// did nothing.
//
// applyModifier tested only against Pressed, so `released:` had no reader. A CC
// whose rest value is not its pressed value -- a toggle, a latching button, or
// anything sending 1 on press and 127 on release -- could never disengage, and
// the modifier stayed ENGAGED for the rest of the session: every shift-modified
// action behaved as if shift were held, with no way to clear it and no error.
func TestReleasedIsHonoured(t *testing.T) {
	h := NewHandler(tableFrom(t, `
device: "T"
modifiers:
  shift:
    kind: cc
    chan: 1
    num: 49
    pressed: 1
    released: 127
    mode: momentary
controls:
  - midi: {kind: cc, chan: 1, num: 40}
    mods: [shift]
    action: {kind: DeckHotcueDelete, target: 0}
    slot: 1
`), func(a engine.Action) { last = a })

	h.Handle(Message{Kind: KindCC, Channel: 1, Num: 49, Value: 1})
	if !h.ActiveMods()["shift"] {
		t.Fatal("value 1 did not engage the modifier, though 127 is its released value")
	}

	h.Handle(Message{Kind: KindCC, Channel: 1, Num: 49, Value: 127})
	if h.ActiveMods()["shift"] {
		t.Error("the modifier is still engaged after its released value: `released:` " +
			"was ignored, so it could never disengage")
	}
}

// A control travelling between the two thresholds -- a pot, or a button
// mid-travel -- holds whatever state it is already in rather than guessing.
func TestAValueBetweenThresholdsHoldsTheState(t *testing.T) {
	h := NewHandler(tableFrom(t, `
device: "T"
modifiers:
  shift:
    kind: cc
    chan: 1
    num: 49
    pressed: 64
    released: 0
    mode: momentary
controls:
  - midi: {kind: cc, chan: 1, num: 40}
    mods: [shift]
    action: {kind: DeckHotcueDelete, target: 0}
    slot: 1
`), func(a engine.Action) { last = a })

	h.Handle(Message{Kind: KindCC, Channel: 1, Num: 49, Value: 64})
	if !h.ActiveMods()["shift"] {
		t.Fatal("the pressed value did not engage the modifier")
	}

	h.Handle(Message{Kind: KindCC, Channel: 1, Num: 49, Value: 32}) // neither
	if !h.ActiveMods()["shift"] {
		t.Error("a value between the two thresholds disengaged the modifier")
	}

	h.Handle(Message{Kind: KindCC, Channel: 1, Num: 49, Value: 0})
	if h.ActiveMods()["shift"] {
		t.Error("the released value did not disengage the modifier")
	}
}
