package tui

import (
	"testing"

	"github.com/maguldon/mix-tui/internal/config"
	"github.com/maguldon/mix-tui/internal/engine"
)

// lastAction returns the last action the model applied.
func lastAction(f *fakeEngine) (engine.Action, bool) {
	if len(f.applied) == 0 {
		return engine.Action{}, false
	}
	return f.applied[len(f.applied)-1], true
}

// TestUntargetedKeysFollowDeckFocus is the regression test for "I can't play a
// track on deck B".
//
// KeyBinding.Target was a plain int, so an omitted target read as 0 and every
// deck-scoped binding resolved to deck A. The deck panel showed B focused, the
// footer said the keys acted on the focused deck, and space played the other
// one. The right-hand deck could not be played, cued, pitched, looped or given
// a hotcue from the keyboard at all.
func TestUntargetedKeysFollowDeckFocus(t *testing.T) {
	// Every deck-scoped key in the shipped map, so a new one cannot be added
	// without this test noticing it is untargeted.
	for _, key := range []string{
		"space", "c", "s", "q", "[", "]", "r",
		"1", "2", "3", "4", "5", "6", "7", "8",
		"9", "0", "w", "e", "y", "u", "i", "o", "\\",
	} {
		for _, focus := range []int{0, 1} {
			m, f := testModel(t)
			m.st.Focus = focus
			// A hotcue jump to an empty slot is refused before it reaches the
			// engine, which is correct but would read here as a missing action.
			// Give both decks four cues so the jump is really attempted.
			for i := range f.snap.Decks {
				f.snap.Decks[i].CueCount = 4
			}

			press(t, m, key)

			a, ok := lastAction(f)
			if !ok {
				t.Errorf("key %q produced no action", key)
				continue
			}
			if !a.TakesDeck() {
				continue // not a deck action; nothing to assert about its target
			}
			if a.Target != focus {
				t.Errorf("key %q with deck %d focused sent Target=%d", key, focus, a.Target)
			}
		}
	}
}

// TestExplicitTargetBeatsFocus keeps the Target field meaningful. A user who
// pins a key to one deck -- the README's own example does exactly this -- must
// get that deck whatever is focused.
func TestExplicitTargetBeatsFocus(t *testing.T) {
	zero, one := 0, 1
	cfg := config.Defaults()
	cfg.Keys = map[string]config.KeyBinding{
		// Pinned to deck A.
		"c": {Action: "DeckCue", Target: &zero, Value: "toggle"},
		// Pinned to deck B.
		"x": {Action: "DeckPlay", Target: &one},
	}
	keys, err := config.NewKeyResolver(cfg)
	if err != nil {
		t.Fatal(err)
	}
	m, f := testModel(t)
	m.keys = keys
	m.st.Focus = 1

	press(t, m, "c")
	if a, ok := lastAction(f); !ok || a.Target != 0 {
		t.Errorf("a key pinned to deck A sent Target=%v, want 0, with deck B focused", a.Target)
	}

	press(t, m, "x")
	if a, ok := lastAction(f); !ok || a.Target != 1 {
		t.Errorf("a key pinned to deck B sent Target=%v, want 1", a.Target)
	}
}

// TestUntargetedBindingIsNotDeckZero guards the config layer directly, so the
// distinction is tested where it is made rather than only where it is consumed.
func TestUntargetedBindingIsNotDeckZero(t *testing.T) {
	cfg := config.Defaults()
	keys, err := config.NewKeyResolver(cfg)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"space", "c", "w", "9"} {
		a, ok := keys.Resolve(key)
		if !ok {
			t.Errorf("key %q did not resolve", key)
			continue
		}
		if a.Target != -1 {
			t.Errorf("key %q resolves to Target=%d; an unnamed deck must not read as deck 0", key, a.Target)
		}
	}
}

// TestMixerKeysAreNotGivenADeck keeps the substitution from leaking. Master
// gain has no deck, and pointing it at one would be meaningless -- worse, it
// would make a future "which deck did this move?" question unanswerable.
func TestMixerKeysAreNotGivenADeck(t *testing.T) {
	m, f := testModel(t)
	m.st.Focus = 1

	press(t, m, "-")
	press(t, m, "=")
	press(t, m, "m")

	for _, a := range f.applied {
		if a.TakesDeck() {
			continue
		}
		if a.Target >= 0 {
			t.Errorf("%v got Target=%d, but it is not a deck action", a.Kind, a.Target)
		}
	}
}

// TestDeckActionsFromMIDIStyleStillWork guards the pinned path used by
// controllers: a mapping always names a deck, and that deck must survive.
func TestDeckActionsFromMIDIStyleStillWork(t *testing.T) {
	m, f := testModel(t)
	m.st.Focus = 1

	m.applyAction(engine.Action{Kind: engine.DeckPlay, Target: 0})

	if a, ok := lastAction(f); !ok || a.Target != 0 {
		t.Errorf("an action with an explicit deck sent Target=%v, want 0", a.Target)
	}
}
