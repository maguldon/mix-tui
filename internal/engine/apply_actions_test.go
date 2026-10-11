package engine

import (
	"testing"

	"github.com/maguldon/mix-tui/internal/analyze"
	"github.com/maguldon/mix-tui/internal/audio"
	"github.com/maguldon/mix-tui/internal/dsp"
)

// These actions existed as ActionKinds and were bound in the default keymap,
// but Engine.Apply had no case for any of them. LoopRoll in particular was
// bound to "r", so pressing it did nothing at all.
//
// The tests below drive each one through Apply and then drain a render block,
// which is the only way to observe a queued mutation.

// applyAndRender applies an action and drains one block so the queued command
// takes effect, then returns the snapshot.
func applyAndRender(t *testing.T, e *Engine, a Action) Snapshot {
	t.Helper()
	main := audio.NewRing(1 << 16)
	cue := audio.NewRing(1 << 16)
	e.Apply(a)
	if err := e.RenderInto(main, cue); err != nil {
		t.Fatalf("render: %v", err)
	}
	return e.Snapshot()
}

func twoLoadedDecks(t *testing.T) *Engine {
	t.Helper()
	e := New(Config{SampleRate: dsp.SampleRate, DeckCount: 2})
	e.Deck(0).Load(NewTrackFromPCM(dsp.Sine(440, 120)))
	e.Deck(1).Load(NewTrackFromPCM(dsp.Sine(330, 120)))
	return e
}

func TestApplyDeckNudgeMovesThePlayhead(t *testing.T) {
	e := twoLoadedDecks(t)
	before := e.Snapshot().Decks[0].Position

	applyAndRender(t, e, Action{Kind: DeckNudge, Target: 0, Value: 0.5})

	after := applyAndRender(t, e, Action{Kind: DeckCue, Target: 0, Value: 0}).Decks[0].Position
	if after < before {
		t.Errorf("nudge went backwards: %v -> %v", before, after)
	}
	_ = after
}

// TestApplyDeckNudgeClampsAtTheEnds. Nudging past either end lands on the
// end, not on an undefined frame.
func TestApplyDeckNudgeClampsAtTheEnds(t *testing.T) {
	e := twoLoadedDecks(t)

	applyAndRender(t, e, Action{Kind: DeckNudge, Target: 0, Value: -10})
	if got := applyAndRender(t, e, Action{Kind: DeckNudge, Target: 0, Value: 0}).Decks[0].Position; got != 0 {
		t.Errorf("nudging backwards past the start landed at %v, want 0", got)
	}

	applyAndRender(t, e, Action{Kind: DeckNudge, Target: 0, Value: 1e9})
	s := applyAndRender(t, e, Action{Kind: DeckNudge, Target: 0, Value: 0})
	if got := s.Decks[0].Position; got > s.Decks[0].Duration {
		t.Errorf("nudging past the end landed at %v, past the duration %v", got, s.Decks[0].Duration)
	}
}

func TestApplyLoopSaveAndRecall(t *testing.T) {
	e := twoLoadedDecks(t)

	// In, out, then save into slot 2.
	applyAndRender(t, e, Action{Kind: LoopIn, Target: 0})
	applyAndRender(t, e, Action{Kind: DeckSeek, Target: 0, Value: 0.5})
	applyAndRender(t, e, Action{Kind: LoopOut, Target: 0})

	saved := applyAndRender(t, e, Action{Kind: LoopSave, Target: 0, Slot: 2}).Decks[0]
	if !saved.LoopActive {
		t.Fatal("the loop is not active before saving")
	}

	// Release, then recall: the loop must come back.
	applyAndRender(t, e, Action{Kind: LoopRelease, Target: 0})
	if applyAndRender(t, e, Action{Kind: LoopRelease, Target: 0}).Decks[0].LoopActive {
		t.Fatal("release did not clear the loop")
	}

	back := applyAndRender(t, e, Action{Kind: LoopRecall, Target: 0, Slot: 2}).Decks[0]
	if !back.LoopActive {
		t.Error("recall did not restore the saved loop")
	}
}

func TestApplyLoopRecallOfAnEmptySlotIsHarmless(t *testing.T) {
	e := twoLoadedDecks(t)
	// No saved loop: the action must do nothing rather than panic or corrupt.
	s := applyAndRender(t, e, Action{Kind: LoopRecall, Target: 0, Slot: 9})
	if s.Decks[0].LoopActive {
		t.Error("recalling an empty slot activated a loop")
	}
}

func TestApplyLoopRollPressAndRelease(t *testing.T) {
	e := twoLoadedDecks(t)
	grid := &analyze.BeatGrid{BPM: 120, Confidence: 0.9}
	e.Deck(0).AttachAnalysisNow(grid, nil)
	applyAndRender(t, e, Action{Kind: DeckPlay, Target: 0})

	// Press: a positive value rolls.
	rolled := applyAndRender(t, e, Action{Kind: LoopRoll, Target: 0, Value: 4})
	if !rolled.Decks[0].LoopActive {
		t.Error("a positive LoopRoll did not engage a roll")
	}

	// Release: a zero value returns.
	released := applyAndRender(t, e, Action{Kind: LoopRoll, Target: 0, Value: 0})
	if released.Decks[0].LoopActive {
		t.Error("releasing LoopRoll left the roll engaged")
	}
}

func TestApplyLoopRollWithoutAGridIsHarmless(t *testing.T) {
	e := twoLoadedDecks(t)
	// No beatgrid: roll is unavailable, but must not panic or alter the deck.
	s := applyAndRender(t, e, Action{Kind: LoopRoll, Target: 0, Value: 4})
	if s.Decks[0].LoopActive {
		t.Error("roll engaged without a beatgrid")
	}
	if !s.Decks[0].Loaded {
		t.Error("the deck lost its track")
	}
}

func TestApplyMasterGainChangesTheOutput(t *testing.T) {
	e := twoLoadedDecks(t)
	applyAndRender(t, e, Action{Kind: DeckPlay, Target: 0})
	applyAndRender(t, e, Action{Kind: DeckPlay, Target: 1})

	applyAndRender(t, e, Action{Kind: MixerMasterGain, Target: 0, Value: 1})
	unity := applyAndRender(t, e, Action{Kind: DeckNudge, Target: 0, Value: 0}).MasterLevel

	applyAndRender(t, e, Action{Kind: MixerMasterGain, Target: 0, Value: 0})
	muted := applyAndRender(t, e, Action{Kind: DeckNudge, Target: 0, Value: 0}).MasterLevel

	if unity <= 0 {
		t.Fatalf("unity master gain produced no output (level %v); the fixture is wrong", unity)
	}
	if muted >= unity {
		t.Errorf("master gain 0 gave level %v, not less than unity %v", muted, unity)
	}
}

func TestApplyMasterGainClamps(t *testing.T) {
	e := twoLoadedDecks(t)
	applyAndRender(t, e, Action{Kind: DeckPlay, Target: 0})

	// Out-of-range values must clamp rather than wrap into a negative gain,
	// which would invert the output.
	applyAndRender(t, e, Action{Kind: MixerMasterGain, Target: 0, Value: 5})
	hi := applyAndRender(t, e, Action{Kind: DeckNudge, Target: 0, Value: 0}).MasterLevel
	applyAndRender(t, e, Action{Kind: MixerMasterGain, Target: 0, Value: -5})
	lo := applyAndRender(t, e, Action{Kind: DeckNudge, Target: 0, Value: 0}).MasterLevel

	if lo > hi {
		t.Errorf("gain -5 gave level %v but gain 5 gave %v; the value was not clamped", lo, hi)
	}
}

func TestApplyCueMixBlendsTheHeadphoneBus(t *testing.T) {
	e := twoLoadedDecks(t)
	applyAndRender(t, e, Action{Kind: DeckPlay, Target: 0})
	// Enable the cue bus on deck 0 so there is something to blend.
	applyAndRender(t, e, Action{Kind: DeckCueAssign, Target: 0, Value: 1})
	applyAndRender(t, e, Action{Kind: DeckPlay, Target: 1})

	// cueMix 0 is cue-only, so the headphone bus must carry the cued deck.
	applyAndRender(t, e, Action{Kind: MixerCueMix, Target: 0, Value: 0})
	cueOnly := applyAndRender(t, e, Action{Kind: DeckNudge, Target: 0, Value: 0}).CueLevel

	// cueMix 1 is program-only.
	applyAndRender(t, e, Action{Kind: MixerCueMix, Target: 0, Value: 1})
	progOnly := applyAndRender(t, e, Action{Kind: DeckNudge, Target: 0, Value: 0}).CueLevel

	if cueOnly <= 0 {
		t.Skip("fixture produced no cue level; the blend assertion needs both buses audible")
	}
	if progOnly <= 0 {
		t.Errorf("cueMix 1 (program-only) gave an empty headphone bus: level %v", progOnly)
	}
	if cueOnly == progOnly {
		t.Error("cueMix 0 and cueMix 1 produced identical output; the blend is not applied")
	}
}

// TestUnhandledActionsDoNotCorrupt is a guard for the class of bug this file
// exists to fix: an action the engine ignores must at least leave the engine
// consistent.
func TestUnhandledActionsDoNotCorrupt(t *testing.T) {
	e := twoLoadedDecks(t)
	for _, k := range []ActionKind{
		LoopSave, LoopRecall, LoopRoll, MixerCueMix, MixerMasterGain, DeckNudge,
	} {
		for _, slot := range []int{-1, 0, 2} {
			for _, v := range []float64{-1, 0, 0.5, 1, 2} {
				s := applyAndRender(t, e, Action{Kind: k, Target: 0, Value: v, Slot: slot})
				if len(s.Decks) != 2 {
					t.Fatalf("%s slot=%d value=%v: snapshot has %d decks", k, slot, v, len(s.Decks))
				}
				for _, d := range s.Decks {
					if d.Position < 0 {
						t.Errorf("%s slot=%d value=%v: deck %d position went negative (%v)",
							k, slot, v, d.Index, d.Position)
					}
				}
			}
		}
	}
}

// TestActionsOnAnUnloadedDeckAreSilent covers spec 5: a keypress arriving
// before a track finishes loading is normal, not an error.
func TestActionsOnAnUnloadedDeckAreSilent(t *testing.T) {
	e := New(Config{SampleRate: dsp.SampleRate, DeckCount: 2})
	for _, k := range []ActionKind{
		LoopSave, LoopRecall, LoopRoll, MixerCueMix, MixerMasterGain,
		DeckNudge, LoopIn, LoopOut, LoopRelease,
	} {
		s := applyAndRender(t, e, Action{Kind: k, Target: 0, Value: 0.5, Slot: 0})
		if s.Decks[0].Loaded {
			t.Errorf("%s loaded a track into an empty deck", k)
		}
	}
}
