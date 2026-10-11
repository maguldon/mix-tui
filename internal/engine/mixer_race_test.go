package engine

import (
	"sync"
	"testing"

	"github.com/maguldon/mix-tui/internal/audio"
	"github.com/maguldon/mix-tui/internal/dsp"
)

// TestApplyDuringMixDoesNotRace covers the unsynchronised mixer fields:
// Apply writes cueEnabled, trim and xfader under the engine mutex, while mix()
// -- on the audio goroutine, after RenderInto has released that mutex -- reads
// all three with no lock. Two goroutines touching the same float64 is a data
// race, not merely stale data: the race detector reports it and the value read
// is undefined.
//
// This test is meaningful under -race and simply exercises the path without it.
func TestApplyDuringMixDoesNotRace(t *testing.T) {
	e := New(Config{SampleRate: dsp.SampleRate, DeckCount: 2})
	main := audio.NewRing(1 << 16)
	cue := audio.NewRing(1 << 16)

	var wg sync.WaitGroup
	wg.Add(2)
	// The audio goroutine: render continuously.
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			_ = e.RenderInto(main, cue)
		}
	}()
	// The control goroutine: move the controls a user would move.
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			v := float64(i%100)/50 - 1
			e.Apply(Action{Kind: MixerCrossfader, Value: v})
			e.Apply(Action{Kind: MixerChannelGain, Target: 0, Value: v})
			e.Apply(Action{Kind: DeckCueAssign, Target: 1, Value: v})
		}
	}()
	wg.Wait()
}
