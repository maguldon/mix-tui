package engine

import (
	"testing"

	"github.com/maguldon/mix-tui/internal/audio"
)

// drain renders enough for posted deck commands to land in the snapshot.
func drain(e *Engine) {
	for i := 0; i < 4; i++ {
		_ = e.RenderInto(audio.NewRing(1<<16), audio.NewRing(1<<16))
	}
}

// An inc/dec key must move a control by one step, not slam it to an extreme.
//
// Resolve sends +1/-1 to mean a direction. Treating that as an absolute
// position meant one press of "-" took master gain from unity to zero and "="
// took it to full. A knob sending an absolute value still has to mean
// "set it here", so the two cases must stay distinguishable.
func TestRelativeActionsStepAndAbsoluteActionsSet(t *testing.T) {
	e := New(Config{SampleRate: 44100, BlockFrames: BlockFrames, DeckCount: 2})
	e.Apply(Action{Kind: MixerMasterGain, Value: 1})

	for i := 0; i < 3; i++ {
		e.Apply(Action{Kind: MixerMasterGain, Value: -1, Relative: true})
	}
	got := e.Snapshot().MasterGain
	want := 1 - 3*StepSize
	if diff := got - want; diff > 1e-9 || diff < -1e-9 {
		t.Errorf("after three decrements master gain = %v, want %v (one step each)", got, want)
	}

	// Absolute, as a knob or a config literal sends.
	e.Apply(Action{Kind: MixerMasterGain, Value: 0.25})
	if got := e.Snapshot().MasterGain; got != 0.25 {
		t.Errorf("absolute master gain = %v, want 0.25", got)
	}
}

// The same rule applies to the pitch fader.
func TestPitchStepsRelativeAndSetsAbsolute(t *testing.T) {
	e := New(Config{SampleRate: 44100, BlockFrames: BlockFrames, DeckCount: 2})

	e.Apply(Action{Kind: DeckPitch, Value: 0})
	drain(e)
	for i := 0; i < 2; i++ {
		e.Apply(Action{Kind: DeckPitch, Value: -1, Relative: true})
		drain(e)
	}
	got := e.Snapshot().Decks[0].Pitch
	want := -2 * StepSize
	if diff := got - want; diff > 1e-9 || diff < -1e-9 {
		t.Errorf("after two decrements pitch = %v, want %v", got, want)
	}

	e.Apply(Action{Kind: DeckPitch, Value: 0.5})
	drain(e)
	if got := e.Snapshot().Decks[0].Pitch; got != 0.5 {
		t.Errorf("absolute pitch = %v, want 0.5", got)
	}
}
