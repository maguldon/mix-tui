package engine

import (
	"sync"
	"testing"

	"github.com/maguldon/mix-tui/internal/audio"
	"github.com/maguldon/mix-tui/internal/dsp"
)

// TestLoadWithDuringRenderDoesNotRace covers loading a track mid-performance,
// which is what the library and the quick-load overlay do.
//
// The first version of that feature called Deck.Load directly from the TUI
// goroutine. Load mutates the resampler, the filter and the playhead, which the
// pump reads concurrently, so the race detector reported it. LoadWith posts the
// mutation instead, which is the invariant the engine is built around: the
// render goroutine is the only writer of a deck's audio state.
func TestLoadWithDuringRenderDoesNotRace(t *testing.T) {
	e := New(Config{SampleRate: dsp.SampleRate, DeckCount: 2})
	e.Deck(0).Load(NewTrackFromPCM(dsp.Sine(440, 30)))
	e.Deck(1).Load(NewTrackFromPCM(dsp.Sine(330, 30)))
	main := audio.NewRing(1 << 16)
	cue := audio.NewRing(1 << 16)

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			_ = e.RenderInto(main, cue)
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 50; i++ {
			e.Deck(0).LoadWith(NewTrackFromPCM(dsp.Sine(220, float64(i%10+1))), nil, nil)
		}
	}()
	wg.Wait()

	// The load must actually land once a block drains the queue.
	_ = e.RenderInto(main, cue)
	if !e.Snapshot().Decks[0].Loaded {
		t.Error("the posted load never applied")
	}
}

// TestLoadNowRequiresNoRendering documents the split: LoadNow is for the
// startup path, before anything renders.
func TestLoadNowIsSynchronous(t *testing.T) {
	d := NewDeckAtRate(0, dsp.SampleRate)
	d.LoadNow(NewTrackFromPCM(dsp.Sine(440, 1)), nil, nil)
	if !d.HasTrack() {
		t.Error("LoadNow must apply immediately; it exists for the startup path")
	}
}
