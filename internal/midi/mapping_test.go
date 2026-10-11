package midi

import (
	"os"
	"strings"
	"testing"
)

func writeTmp(t *testing.T, body string) string {
	t.Helper()
	p := t.TempDir() + "/m.yaml"
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

const goodMapping = `
device: "Test Controller"
match: substring
modifiers:
  shift:
    kind: cc
    chan: 1
    num: 49
    pressed: 127
    released: 0
    mode: momentary
controls:
  - midi: {kind: cc, chan: 1, num: 9}
    action: {kind: DeckPitch, target: 0}
  - midi: {kind: cc, chan: 1, num: 20}
    action: {kind: CueAssign, target: 0}
    momentary: true
  - midi: {kind: note, chan: 1, num: 40}
    mods: [shift]
    action: {kind: DeckHotcueDelete, target: 0}
    slot: 1
`

func TestParseGoodMapping(t *testing.T) {
	m, err := ParseMapping("x.yaml", []byte(goodMapping))
	if err != nil {
		t.Fatal(err)
	}
	if m.Device != "Test Controller" {
		t.Errorf("device = %q", m.Device)
	}
	if len(m.Controls) != 3 {
		t.Fatalf("controls = %d", len(m.Controls))
	}
	if _, ok := m.Modifiers["shift"]; !ok {
		t.Error("shift modifier missing")
	}
	if m.Controls[2].Slot != 1 {
		t.Errorf("slot = %d", m.Controls[2].Slot)
	}
}

func TestParseRejectsUnknownField(t *testing.T) {
	// A typo must fail loudly rather than silently disable the control.
	bad := strings.Replace(goodMapping,
		"    action: {kind: DeckPitch, target: 0}",
		"    actoin: {kind: DeckPitch, target: 0}", 1)
	_, err := ParseMapping("x.yaml", []byte(bad))
	if err == nil {
		t.Fatal("unknown field must be an error")
	}
	if !strings.Contains(err.Error(), "x.yaml") {
		t.Errorf("error must name the file: %v", err)
	}
}

func TestParseErrorCarriesLineNumber(t *testing.T) {
	bad := "device: \"X\"\ncontrols:\n  - midi: {kind: cc, chan: 1, num: 9}\n    action: {kind: NotARealAction, target: 0}\n"
	_, err := ParseMapping("x.yaml", []byte(bad))
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "line") {
		t.Errorf("error must carry a line number: %v", err)
	}
}

func TestParseRejectsUnknownModifierReference(t *testing.T) {
	bad := strings.Replace(goodMapping, "    mods: [shift]", "    mods: [shfit]", 1)
	_, err := ParseMapping("x.yaml", []byte(bad))
	if err == nil {
		t.Fatal("a control referencing an undeclared modifier must error")
	}
}

func TestMomentaryShorthandSetsMode(t *testing.T) {
	m, _ := ParseMapping("x.yaml", []byte(goodMapping))
	m2, _ := m.Compile()
	c, ok := m2.Lookup(ModSet{}, Message{Kind: KindCC, Channel: 1, Num: 20, Value: 127})
	if !ok {
		t.Fatal("cue control not found")
	}
	if c.Mode != ModeMomentary {
		t.Errorf("momentary: true did not set ModeMomentary, got %v", c.Mode)
	}
}

func TestDeviceMatchingSubstring(t *testing.T) {
	// Review Focus #5: controllers rename themselves across reconnects.
	m, _ := ParseMapping("x.yaml", []byte(goodMapping))
	tab, _ := m.Compile()
	// The fixture device is "Test Controller"; a reconnect that appends a port
	// suffix must still match.
	if !tab.Matches("Test Controller 96") {
		t.Error("substring match failed on a suffixed name")
	}
	if tab.Matches("Unrelated Pad") {
		t.Error("substring match too loose")
	}
}

func TestDeviceMatchingExact(t *testing.T) {
	y := strings.Replace(goodMapping, "match: substring", "match: exact", 1)
	m, _ := ParseMapping("x.yaml", []byte(y))
	tab, _ := m.Compile()
	if tab.Matches("Test Controller 96") {
		t.Error("exact match must not match a longer name")
	}
	if !tab.Matches("Test Controller") {
		t.Error("exact match failed on the exact name")
	}
}

func TestShippedMappingsParse(t *testing.T) {
	for _, p := range []string{
		"../testdata/mappings/xone-k2.yaml",
		"../testdata/mappings/generic-2deck.yaml",
	} {
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("%s: %v", p, err)
		}
		if _, err := ParseMapping(p, data); err != nil {
			t.Errorf("shipped mapping %s does not parse: %v", p, err)
		}
	}
}

// TestCompileRejectsDuplicateBinding covers review focus: two controls bound to
// the same (mods, kind, chan, num) means the second is dead code, almost always
// a typo. Silently keeping the first hides the mistake until a button "does
// nothing" mid-set.
func TestCompileRejectsDuplicateBinding(t *testing.T) {
	dup := `
device: "X"
controls:
  - midi: {kind: cc, chan: 1, num: 9}
    action: {kind: DeckPitch, target: 0}
  - midi: {kind: cc, chan: 1, num: 9}
    action: {kind: DeckCue, target: 0}
`
	m, err := ParseMapping("dup.yaml", []byte(dup))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if _, err := m.Compile(); err == nil {
		t.Fatal("duplicate binding must be rejected at compile time")
	}
}

// TestLookupDistinguishesModifierSets ensures the same physical message can map
// to different actions depending on held modifiers — the SHIFT+hotcue-delete vs
// bare hotcue-jump case. A single flat map would make one of them unreachable.
func TestLookupDistinguishesModifierSets(t *testing.T) {
	m, _ := ParseMapping("x.yaml", []byte(goodMapping))
	tab, err := m.Compile()
	if err != nil {
		t.Fatal(err)
	}
	msg := Message{Kind: KindNote, Channel: 1, Num: 40, Velocity: 127}
	// goodMapping binds note 40 only under shift, so a bare press must NOT match.
	if _, ok := tab.Lookup(ModSet{}, msg); ok {
		t.Fatal("bare note matched a shift-only binding")
	}
	withShift, ok := tab.Lookup(ModSet{"shift": true}, msg)
	if !ok {
		t.Fatal("shift+note not bound")
	}
	if withShift.Action.String() != "DeckHotcueDelete" {
		t.Errorf("shift+note resolved to %v, want DeckHotcueDelete", withShift.Action)
	}
}

// TestModifierSetsAreCanonicalOrderIndependent ensures "shift" and "ctrl+shift"
// and "shift+ctrl" produce the same key, so two controls listing the same
// modifiers in different orders are not treated as distinct bindings.
func TestModifierSetsAreCanonicalOrderIndependent(t *testing.T) {
	body := `
device: "X"
modifiers:
  shift: {kind: cc, chan: 1, num: 49}
  ctrl: {kind: cc, chan: 1, num: 50}
controls:
  - midi: {kind: note, chan: 1, num: 40}
    mods: [shift, ctrl]
    action: {kind: DeckHotcueJump, target: 0}
    slot: 1
  - midi: {kind: note, chan: 1, num: 40}
    mods: [ctrl, shift]
    action: {kind: DeckHotcueDelete, target: 0}
    slot: 1
`
	m, err := ParseMapping("order.yaml", []byte(body))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if _, err := m.Compile(); err == nil {
		t.Fatal("same modifier set in different order must be rejected as a duplicate")
	}
}
