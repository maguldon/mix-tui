package engine

import (
	"math"
	"testing"

	"github.com/maguldon/mix-tui/internal/dsp"
)

// SetFilter posts the filter kind but then called SetParams directly on the
// render-owned filter from the caller's goroutine -- the exact race
// SetFilterCutoff and SetFilterResonance avoid by posting. Nothing in production
// called it, so it had never been caught.
func TestSetFilterIsRaceFree(t *testing.T) {
	e := New(Config{SampleRate: dsp.SampleRate, DeckCount: 2})
	e.Deck(0).LoadNow(NewTrackFromPCM(dsp.Sine(220, 4)), testGrid(128, 64), nil)

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 200; i++ {
			e.Deck(0).SetFilter(i%2 == 0, 400+float64(i)*10, 0.8)
		}
	}()
	for i := 0; i < 200; i++ {
		drain(e)
	}
	<-done
	drain(e)

	d := e.Snapshot().Decks[0]
	if d.FilterCutoffNorm < 0 || d.FilterCutoffNorm > 1 {
		t.Errorf("cutoff knob = %v, outside 0..1", d.FilterCutoffNorm)
	}
}

// SetFilter's API is in Hz; the knob is logarithmic. Feeding Hz into the
// normalized setter retunes every filter by a curve.
func TestSetFilterHzMatchesTheKnob(t *testing.T) {
	e := New(Config{SampleRate: dsp.SampleRate, DeckCount: 2})
	e.Deck(0).LoadNow(NewTrackFromPCM(dsp.Sine(220, 4)), testGrid(128, 64), nil)
	drain(e)

	e.Deck(0).SetFilter(true, 1000, 0.7)
	drain(e)

	got := e.Snapshot().Decks[0].FilterCutoffNorm
	want := dsp.NormalizedCutoff(1000)
	if math.Abs(got-want) > 1e-9 {
		t.Errorf("1000Hz gave knob %v, want %v", got, want)
	}
}

// NormalizedCutoff is the inverse of the knob sweep, so the round trip returns
// what went in.
func TestNormalizedCutoffRoundTrips(t *testing.T) {
	for _, hz := range []float64{20, 100, 1000, 5000, 20000} {
		n := dsp.NormalizedCutoff(hz)
		back := 20 * math.Pow(20000.0/20.0, n)
		if rel := math.Abs(back-hz) / hz; rel > 1e-9 {
			t.Errorf("round trip of %vHz gave %v", hz, back)
		}
	}
}
