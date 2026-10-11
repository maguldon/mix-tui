package config

import (
	"testing"

	"github.com/maguldon/mix-tui/internal/engine"
)

// TestKeyBindingsCarrySlots. compiled.slot was declared and never assigned, so
// no keybinding could name a hotcue or loop slot. Every slot-taking action was
// MIDI-only, which made the README's claim that every action is reachable from
// the keyboard false for hotcues, loop save and loop recall.
func TestKeyBindingsCarrySlots(t *testing.T) {
	r, err := NewKeyResolver(Defaults())
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		key  string
		kind engine.ActionKind
		slot int
	}{
		{"1", engine.DeckHotcueSet, 0},
		{"3", engine.DeckHotcueSet, 1},
		{"5", engine.DeckHotcueSet, 2},
		{"7", engine.DeckHotcueSet, 3},
		{"2", engine.DeckHotcueJump, 0},
		{"i", engine.LoopSave, 0},
		{"o", engine.LoopRecall, 0},
	} {
		a, ok := r.Resolve(tc.key)
		if !ok {
			t.Errorf("key %q has no binding", tc.key)
			continue
		}
		if a.Kind != tc.kind {
			t.Errorf("key %q resolves to %v, want %v", tc.key, a.Kind, tc.kind)
		}
		if a.Slot != tc.slot {
			t.Errorf("key %q resolves to slot %d, want %d", tc.key, a.Slot, tc.slot)
		}
	}
}

// TestEveryShippedBindingResolves catches a keymap that names an action the
// resolver does not know. Such a binding fails at startup rather than at the
// moment the user presses the key.
func TestEveryShippedBindingResolves(t *testing.T) {
	r, err := NewKeyResolver(Defaults())
	if err != nil {
		t.Fatal(err)
	}
	for key := range DefaultKeys() {
		if _, ok := r.Resolve(key); !ok {
			t.Errorf("shipped key %q does not resolve", key)
		}
	}
}
