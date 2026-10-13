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
