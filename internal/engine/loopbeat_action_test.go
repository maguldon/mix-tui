package engine

import (
	"testing"

	"github.com/maguldon/mix-tui/internal/dsp"
)

// TestLoopBeatKeysProduceTheirOwnLengths is the regression test for issue #10.
//
// w/e/y/u bind Value "2"/"4"/"8"/"16". The engine ran every LoopBeat through
// the MIDI knob mapping, where v >= 1 returns the largest size -- so all four
// beat-loop keys armed a 32-beat loop and the transport row read LOOP 32 for
// each of them.
func TestLoopBeatKeysProduceTheirOwnLengths(t *testing.T) {
	cases := map[string]float64{"w": 2, "e": 4, "y": 8, "u": 16}
	for key, want := range cases {
		if got := loopBeatsForAction(want); got != want {
			t.Errorf("key %q (value %v) produced a %g-beat loop, want %g", key, want, got, want)
		}
	}
}

// TestLoopBeatKnobPositionsStillWork keeps the MIDI path intact. An encoder
// sends 0..1 and must still sweep the whole size range, including full travel
// reaching the largest size.
func TestLoopBeatKnobPositionsStillWork(t *testing.T) {
	cases := []struct {
		in, want float64
	}{
		{0.0, 0.25},
		{0.5, 4},
		{1.0, 32},
	}
	for _, tc := range cases {
		if got := loopBeatsForAction(tc.in); got != tc.want {
			t.Errorf("knob at %v produced %g beats, want %g", tc.in, got, tc.want)
		}
	}
}

// TestLoopBeatSnapsAnOffSizeBeatCount keeps a user who writes 3 or 20 from
// getting silence. Snapping is what SetLoopBeat would apply anyway.
func TestLoopBeatSnapsAnOffSizeBeatCount(t *testing.T) {
	cases := map[float64]float64{3: 4, 5: 4, 6: 8, 20: 16, 100: 32}
	for in, want := range cases {
		if got := loopBeatsForAction(in); got != want {
			t.Errorf("beat count %v snapped to %g, want %g", in, got, want)
		}
	}
}

// TestLoopBeatKeysDriveTheRealEngine walks the whole path: the shipped keymap's
// literal value, through Apply, to the deck's armed loop length.
func TestLoopBeatKeysDriveTheRealEngine(t *testing.T) {
	for _, want := range []float64{2, 4, 8, 16} {
		e := New(Config{SampleRate: dsp.SampleRate, DeckCount: 2})
		e.Deck(0).LoadNow(NewTrackFromPCM(dsp.Sine(220, 4)), testGrid(128, 64), nil)

		e.Apply(Action{Kind: LoopBeat, Target: 0, Value: want})
		drain(e)

		d := e.Snapshot().Decks[0]
		if !d.LoopActive {
			t.Fatalf("value %v armed no loop", want)
		}
		if d.LoopBeatCount != want {
			t.Errorf("value %v armed a %g-beat loop, want %g", want, d.LoopBeatCount, want)
		}
	}
}

// TestReleaseLoopKeepsTheArmedLength is the regression test for spec 6.2.2.
//
// ReleaseLoop zeroed the beat count, so after releasing a loop the deck had no
// length at all: the panel showed nothing, and turning the encoder changed
// nothing on screen until it happened to arm a loop the user was already using.
// That is the "encoder with no readout feels broken" failure the section
// forbids, and the README documents w/e/y/u as 2/4/8/16-beat loop keys, so
// "nothing visible" meant "I cannot tell which key I pressed".
func TestReleaseLoopKeepsTheArmedLength(t *testing.T) {
	e := New(Config{SampleRate: dsp.SampleRate, DeckCount: 2})
	e.Deck(0).LoadNow(NewTrackFromPCM(dsp.Sine(220, 4)), testGrid(128, 64), nil)
	e.Deck(0).Play()
	drain(e)

	if err := e.Deck(0).SetLoopBeat(4); err != nil {
		t.Fatalf("SetLoopBeat: %v", err)
	}
	drain(e)
	if got := e.Snapshot().Decks[0].LoopBeatCount; got != 4 {
		t.Fatalf("armed %g beats, want 4", got)
	}

	e.Deck(0).ReleaseLoop()
	drain(e)

	d := e.Snapshot().Decks[0]
	if d.LoopActive {
		t.Error("the loop is still active after a release")
	}
	if d.LoopBeatCount != 4 {
		t.Errorf("releasing discarded the armed length: %g", d.LoopBeatCount)
	}
}

// TestAFreshDeckHasNoArmedLength keeps the readout from claiming a length the
// user never chose. Zero means "nothing selected", which is also what a free
// loop set by hand is.
func TestAFreshDeckHasNoArmedLength(t *testing.T) {
	e := New(Config{SampleRate: dsp.SampleRate, DeckCount: 2})
	e.Deck(0).LoadNow(NewTrackFromPCM(dsp.Sine(220, 4)), testGrid(128, 64), nil)
	drain(e)
	if got := e.Snapshot().Decks[0].LoopBeatCount; got != 0 {
		t.Errorf("a freshly loaded deck reports %g beats", got)
	}
}

// TestTheEncoderIsVisibleBetweenLoops covers the whole point: turning it must
// change something on screen whether or not a loop is engaged.
func TestTheEncoderIsVisibleBetweenLoops(t *testing.T) {
	e := New(Config{SampleRate: dsp.SampleRate, DeckCount: 2})
	e.Deck(0).LoadNow(NewTrackFromPCM(dsp.Sine(220, 4)), testGrid(128, 64), nil)
	e.Deck(0).Play()
	drain(e)
	e.Deck(0).ReleaseLoop()
	drain(e)

	seen := map[float64]bool{}
	for _, want := range []float64{2, 4, 8, 16} {
		// SetLoopBeat arms a loop, so release between turns: this is the
		// between-loops state a DJ is actually in most of the time.
		e.Deck(0).SetLoopBeat(want)
		drain(e)
		e.Deck(0).ReleaseLoop()
		drain(e)
		seen[e.Snapshot().Decks[0].LoopBeatCount] = true
	}
	if len(seen) != 4 {
		t.Errorf("the armed length took %d distinct values across w/e/y/u, want 4", len(seen))
	}
}
