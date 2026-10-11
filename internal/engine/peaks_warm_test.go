package engine

import (
	"testing"
	"time"

	"github.com/maguldon/mix-tui/internal/dsp"
)

// TestSnapshotDoesNotStallOnFirstRead is the regression test for the eager
// peaks cache.
//
// The envelope scan costs ~33ms for a five-minute track. It used to run lazily
// on first read, which meant it ran inside Snapshot -- on the TUI goroutine,
// holding the engine mutex -- so a track load stalled the render goroutine for
// the full scan. It now runs on the render goroutine at load time, while the
// deck is not yet playing.
//
// The property under test is "Snapshot does no work proportional to track
// length", not a specific number. Scanning half an hour of audio costs tens of
// milliseconds; not scanning costs well under a microsecond, so 5ms sits far
// from both ends.
func TestSnapshotDoesNotStallOnFirstRead(t *testing.T) {
	// Half an hour per deck. The gap between scanning and not scanning grows
	// with track length while the warm cost does not, so a long track makes
	// this a robust measurement rather than a coin flip near the threshold.
	e := New(Config{SampleRate: dsp.SampleRate, DeckCount: 2})
	e.Deck(0).Load(NewTrackFromPCM(dsp.Sine(440, 1800)))
	e.Deck(1).Load(NewTrackFromPCM(dsp.Sine(330, 1800)))

	start := time.Now()
	for i := 0; i < 10; i++ {
		_ = e.Snapshot()
	}
	per := time.Since(start) / 10

	if per > 5*time.Millisecond {
		t.Errorf("Snapshot takes %v per call; it must not scan the track", per)
	}
	t.Logf("Snapshot: %v per call", per)
}

// TestLoadWarmsThePeaksCache checks the other half: the envelope must be
// available immediately after a load, without waiting for a render, so a view
// drawn in the same tick as the load shows a waveform and not a flat line.
func TestLoadWarmsThePeaksCache(t *testing.T) {
	e := New(Config{SampleRate: dsp.SampleRate, DeckCount: 1})
	e.Deck(0).Load(NewTrackFromPCM(dsp.Sine(440, 300)))

	st := e.Snapshot().Decks[0]
	if !st.Loaded {
		t.Fatal("deck should be loaded")
	}

	var nonZero int
	for _, v := range st.Peaks.Max {
		if v > 0 {
			nonZero++
		}
	}
	if nonZero == 0 {
		t.Error("the envelope was not warmed at load time; the waveform would render flat")
	}
}

// TestPeaksInvalidateOnReload guards against a stale envelope: loading a second
// track must not leave the first track's waveform on screen.
func TestPeaksInvalidateOnReload(t *testing.T) {
	e := New(Config{SampleRate: dsp.SampleRate, DeckCount: 1})
	e.Deck(0).Load(NewTrackFromPCM(dsp.Sine(440, 60)))
	first := e.Snapshot().Decks[0].Peaks.Max[10]

	e.Deck(0).Load(NewTrackFromPCM(dsp.DC(0, 60)))

	second := e.Snapshot().Decks[0].Peaks.Max[10]
	if first == second {
		t.Error("the envelope was not recomputed for the new track")
	}
	if second != 0 {
		t.Errorf("a silent track should have a flat envelope, got %v", second)
	}
}
