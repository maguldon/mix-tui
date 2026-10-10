package engine

import (
	"testing"
	"time"

	"github.com/maguldon/mix-tui/internal/audio"
	"github.com/maguldon/mix-tui/internal/dsp"
)

// Spec §4.2: "the sample rate is whatever the device runs at (typically 44100
// or 48000), negotiated once at startup ... the code is rate-agnostic."
//
// Config.SampleRate was a dead field: NewDeck hardcoded dsp.SampleRate for its
// resamplers and every duration/frame conversion. Setting it to 48000 changed
// nothing, so milestone 2's rate negotiation would have declared a 48k stream
// while the engine rendered 44.1k audio into it.

func TestEngineHonoursConfiguredSampleRate(t *testing.T) {
	const rate = 48000
	e := New(Config{SampleRate: rate, BlockFrames: 512, RingFrames: 4096, DeckCount: 1})
	if got := e.Config().SampleRate; got != rate {
		t.Fatalf("Config().SampleRate = %d, want %d", got, rate)
	}
	if got := e.Deck(0).SampleRate(); got != rate {
		t.Errorf("deck sample rate = %d, want %d", got, rate)
	}
}

// One second of a track is SampleRate frames, whatever the rate.
func TestDeckPositionsConvertAtConfiguredRate(t *testing.T) {
	for _, rate := range []int{44100, 48000} {
		d := NewDeckAtRate(0, rate)
		d.Load(&audio.Track{
			Path: "t", SampleRate: rate, Channels: 2,
			Duration: time.Second,
			PCM:      dsp.Stereo(make([]float32, rate*2)),
		})
		d.Seek(time.Second)
		if got := d.PosFrames(); got != float64(rate) {
			t.Errorf("rate %d: one second = %v frames, want %v", rate, got, rate)
		}
	}
}

// A deck at 48k must consume its source at 48k frames per second of playback.
func TestDeckRendersAtConfiguredRate(t *testing.T) {
	const rate = 48000
	d := NewDeckAtRate(0, rate)
	d.Load(&audio.Track{
		Path: "t", SampleRate: rate, Channels: 2,
		Duration: 10 * time.Second,
		PCM:      dsp.Stereo(make([]float32, rate*10*2)),
	})
	d.Play()
	tmp := make(dsp.Stereo, BlockFrames*2)
	total := 0
	for i := 0; i < 100; i++ {
		total += d.Render(tmp)
	}
	// 100 blocks at 512 frames is 51200 frames, which at 48k is 1.067s.
	want := 100.0 * BlockFrames
	if diff := d.PosFrames() - want; diff > 1 || diff < -1 {
		t.Errorf("pos after 100 blocks = %v, want ~%v", d.PosFrames(), want)
	}
	_ = total
}
