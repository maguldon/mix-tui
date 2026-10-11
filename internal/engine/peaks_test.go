package engine

import (
	"testing"

	"github.com/maguldon/mix-tui/internal/dsp"
)

// TestPeaksAreComputedOnce pins the fix for the most expensive bug found in
// review: the view used to derive the envelope from PCM on every rendered
// frame, which costs ~33ms for a five-minute track. At 30fps with two decks
// that is two CPU-seconds per wall-clock second, and it thrashes the cache the
// audio thread depends on.
func TestPeaksAreComputedOnce(t *testing.T) {
	e := New(Config{SampleRate: dsp.SampleRate, DeckCount: 2})
	e.Deck(0).Load(NewTrackFromPCM(dsp.Sine(440, 60)))

	first, width := e.Deck(0).Peaks()
	if width <= 0 || len(first.Max) == 0 {
		t.Fatal("no peaks computed")
	}
	// Repeated reads must be the same slice, not a recomputation. Comparing the
	// backing arrays by pointer is what distinguishes a cache from a recompute.
	second, _ := e.Deck(0).Peaks()
	if &first.Max[0] != &second.Max[0] {
		t.Error("peaks were recomputed rather than served from the cache")
	}
}

// TestPeaksInvalidateOnLoad stops a stale envelope being drawn over new audio,
// which is what a cache without invalidation would do.
func TestPeaksInvalidateOnLoad(t *testing.T) {
	d := NewDeckAtRate(0, dsp.SampleRate)
	d.Load(NewTrackFromPCM(dsp.Sine(440, 1)))
	quiet, _ := d.Peaks()

	d.Load(NewTrackFromPCM(dsp.Sine(880, 1)))
	loud, _ := d.Peaks()

	if &quiet.Max[0] == &loud.Max[0] {
		t.Error("loading a new track did not invalidate the cached peaks")
	}
}

// TestPeaksEmptyDeckIsSafe covers an unloaded deck, which the view draws before
// anything is loaded.
func TestPeaksEmptyDeckIsSafe(t *testing.T) {
	d := NewDeckAtRate(0, dsp.SampleRate)
	p, w := d.Peaks()
	if w <= 0 {
		t.Error("width must still be reported for an empty deck")
	}
	if len(p.Max) != w*2 {
		t.Errorf("empty deck gave %d entries, want %d", len(p.Max), w*2)
	}
}
