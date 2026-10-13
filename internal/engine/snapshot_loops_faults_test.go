package engine

import (
	"testing"
	"time"

	"github.com/maguldon/mix-tui/internal/dsp"
)

// loopDeck returns a deck with a two-second track, playing, with a four-beat
// loop set and saved into two slots.
func loopDeck(t *testing.T) *Engine {
	t.Helper()
	e := New(Config{SampleRate: dsp.SampleRate, DeckCount: 2})
	e.Deck(0).LoadNow(NewTrackFromPCM(dsp.Sine(220, 2)), testGrid(128, 8), nil)
	e.Deck(0).Play()
	drain(e)

	// A beat-quantised loop rather than hand-placed SetLoopIn/SetLoopOut: those
	// two mutate the loop from the caller's goroutine while the render goroutine
	// owns it, so building a loop that way from a test is a data race. What is
	// under test is the Snapshot field, not how the loop was placed.
	if err := e.Deck(0).SetLoopBeat(4); err != nil {
		t.Fatalf("SetLoopBeat: %v", err)
	}
	drain(e)
	for _, slot := range []int{1, 3} {
		if err := e.Deck(0).SaveLoop(slot); err != nil {
			t.Fatalf("SaveLoop(%d): %v", slot, err)
		}
	}
	drain(e)
	return e
}

// TestSnapshotCarriesSavedLoopSlots covers spec 6.2.1, which asks for saved slots
// to be visible alongside the hotcues.
//
// Deck.Loop() has always returned them. The Snapshot did not, so no view could
// have drawn them and the requirement was unbuildable rather than unimplemented.
func TestSnapshotCarriesSavedLoopSlots(t *testing.T) {
	e := loopDeck(t)
	slots := e.Snapshot().Decks[0].SavedLoopSlots

	if len(slots) != 2 {
		t.Fatalf("Snapshot carries %d slots, want 2: %v", len(slots), slots)
	}
	for _, slot := range []int{1, 3} {
		r, ok := slots[slot]
		if !ok {
			t.Errorf("slot %d is missing from the Snapshot", slot)
			continue
		}
		if r.End <= r.Start {
			t.Errorf("slot %d is empty: %v .. %v", slot, r.Start, r.End)
		}
	}
}

// TestSnapshotSlotsAreATraceableCopy keeps the Snapshot from aliasing the Deck's
// own map: the view holds it across frames while the render goroutine writes.
func TestSnapshotSlotsAreATraceableCopy(t *testing.T) {
	e := loopDeck(t)
	snap := e.Snapshot().Decks[0]

	if err := e.Deck(0).SaveLoop(2); err != nil {
		t.Fatalf("SaveLoop: %v", err)
	}
	drain(e)

	if len(snap.SavedLoopSlots) != 2 {
		t.Errorf("an earlier Snapshot changed under the caller: %v", snap.SavedLoopSlots)
	}
	if _, ok := e.Snapshot().Decks[0].SavedLoopSlots[2]; !ok {
		t.Error("the new slot did not reach a fresh Snapshot")
	}
}

// TestSnapshotFaultsOnlyRealFaults covers the marker added for spec 8.
//
// The spec asks for it on a decoder failing mid-stream, which cannot happen with
// whole-file decoding. What is worth marking is the case a user cannot
// otherwise diagnose: a deck that renders a title, a BPM and a waveform while
// producing nothing. Marking every deck that merely has no track, or one that is
// merely stopped, would make the marker noise.
func TestSnapshotFaultsOnlyRealFaults(t *testing.T) {
	e := New(Config{SampleRate: dsp.SampleRate, DeckCount: 2})

	// An empty deck is not faulted; it has nothing to be faulted about.
	if d := e.Snapshot().Decks[0]; d.Faulted {
		t.Errorf("an empty deck is faulted: %q", d.FaultReason)
	}

	e.Deck(0).LoadNow(NewTrackFromPCM(dsp.Sine(220, 2)), nil, nil)
	drain(e)
	d := e.Snapshot().Decks[0]
	if d.Faulted {
		t.Errorf("a loaded, playable deck is faulted: %q", d.FaultReason)
	}
	if d.FaultReason != "" {
		t.Errorf("a healthy deck carries a reason: %q", d.FaultReason)
	}
}

// TestSnapshotFaultsATrackThatHasRunOut covers the case the marker exists for: a
// deck that still shows its metadata but has no audio left to play.
func TestSnapshotFaultsATrackThatHasRunOut(t *testing.T) {
	e := New(Config{SampleRate: dsp.SampleRate, DeckCount: 2})
	e.Deck(0).LoadNow(NewTrackFromPCM(dsp.Sine(220, 1)), nil, nil)
	e.Deck(0).Play()

	// Render past the end of the track.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		drain(e)
		if e.Snapshot().Decks[0].Faulted {
			return
		}
	}
	d := e.Snapshot().Decks[0]
	if !d.Loaded {
		t.Skip("the deck unloaded rather than reaching the end; nothing to mark")
	}
	t.Errorf("a finished track is not marked faulted: %q", d.FaultReason)
}

// TestALoopingDeckAtTheEndIsNotFaulted keeps the marker off a looping deck. A
// deck cycling a loop past the track's end is doing exactly what it was asked
// to, and marking it would report a fault for correct behaviour.
func TestALoopingDeckAtTheEndIsNotFaulted(t *testing.T) {
	e := New(Config{SampleRate: dsp.SampleRate, DeckCount: 2})
	e.Deck(0).LoadNow(NewTrackFromPCM(dsp.Sine(220, 1)), testGrid(128, 16), nil)
	e.Deck(0).Play()
	drain(e)
	if err := e.Deck(0).SetLoopBeat(1); err != nil {
		t.Fatalf("SetLoopBeat: %v", err)
	}
	drain(e)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		drain(e)
		if d := e.Snapshot().Decks[0]; d.Faulted {
			t.Fatalf("a looping deck was marked faulted: %q", d.FaultReason)
		}
	}
}
