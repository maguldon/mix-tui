package engine

import (
	"testing"

	"github.com/maguldon/mix-tui/internal/dsp"
)

// Loading a track must not leave the previous track's positions behind.
//
// Nothing positional was reset, so loading a new track into a deck that was
// looping the previous one left the loop armed and ACTIVE: the deck jumped
// straight back into a loop over audio the user had just replaced, at a position
// measured in the old track's frames. Hotcues and saved loop slots survived the
// same way, so a hotcue set on track A jumped to a meaningless offset in track
// B.
func TestALoadClearsTheLoopThePreviousTrackHad(t *testing.T) {
	e := New(Config{SampleRate: dsp.SampleRate, DeckCount: 2})
	e.Deck(0).LoadNow(NewTrackFromPCM(dsp.Sine(220, 4)), testGrid(128, 64), nil)
	drain(e)
	e.Deck(0).Play()
	drain(e)

	e.Apply(Action{Kind: LoopBeat, Value: 4})
	drain(e)
	for i := 0; i < 200; i++ {
		drain(e)
	}
	e.Apply(Action{Kind: LoopIn})
	drain(e)
	for i := 0; i < 40; i++ {
		drain(e)
	}
	e.Apply(Action{Kind: LoopOut})
	drain(e)
	e.Apply(Action{Kind: DeckHotcueSet, Slot: 0})
	drain(e)

	if before := e.Snapshot().Decks[0]; !before.LoopActive {
		t.Fatalf("setup failed: the loop is not active, so this test proves nothing")
	}

	e.Deck(0).LoadNow(NewTrackFromPCM(dsp.Sine(330, 4)), testGrid(140, 64), nil)
	drain(e)

	after := e.Snapshot().Decks[0]
	if after.LoopActive {
		t.Error("the previous track's loop is still armed after loading another track")
	}
	if after.LoopStart != 0 || after.LoopEnd != 0 {
		t.Errorf("the previous track's loop bounds survived: %v..%v", after.LoopStart, after.LoopEnd)
	}
	if len(after.SavedLoopSlots) != 0 {
		t.Errorf("saved loop slots survived the load: %v", after.SavedLoopSlots)
	}
}

// The armed loop LENGTH is a different thing from the loop's bounds and must
// survive a load: spec 6.2.2 requires the encoder's position to stay visible, and
// the readout is the armed length. Clearing it would blank the encoder every
// time a track changed.
func TestALoadKeepsTheArmedLoopLength(t *testing.T) {
	e := New(Config{SampleRate: dsp.SampleRate, DeckCount: 2})
	e.Deck(0).LoadNow(NewTrackFromPCM(dsp.Sine(220, 4)), testGrid(128, 64), nil)
	drain(e)
	e.Apply(Action{Kind: LoopBeat, Value: 4})
	drain(e)

	e.Deck(0).LoadNow(NewTrackFromPCM(dsp.Sine(330, 4)), testGrid(140, 64), nil)
	drain(e)

	if got := e.Snapshot().Decks[0].LoopBeatCount; got != 4 {
		t.Errorf("a load cleared the armed loop length: %g beats, want 4", got)
	}
}
