package engine

import (
	"math"
	"testing"
	"time"

	"github.com/maguldon/mix-tui/internal/audio"
	"github.com/maguldon/mix-tui/internal/dsp"
)

// measuredFreq estimates a 44.1k rendered tone by counting zero
// crossings. It measures what the listener would actually hear, rather than
// trusting a configuration value.
func measuredFreqAt(s dsp.Stereo, renderRate int) float64 {
	// Count RISING zero crossings only. Counting both directions double-counts
	// a sine (each period has one up-crossing and one down-crossing), which is
	// how an earlier version of this helper reported 880Hz for a 440Hz tone.
	crossings := 0
	first, last := -1, -1
	var prev float32
	for i := 0; i+3 < len(s); i += 2 {
		v := s[i]
		if i > 0 && prev < 0 && v >= 0 {
			if first < 0 {
				first = i / 2
			}
			last = i / 2
			crossings++
		}
		prev = v
	}
	if crossings < 2 || last <= first {
		return 0
	}
	// Crossings per second of the RENDER rate, not the package constant: at
	// 48k a 440Hz tone has proportionally fewer crossings per frame.
	return float64(crossings-1) * float64(renderRate) / float64(last-first)
}

// measuredFreq measures at the package default rate.
func measuredFreq(s dsp.Stereo) float64 { return measuredFreqAt(s, dsp.SampleRate) }

// C3 RED: a deck rendering at a rate other than its source's must resample, or
// every track plays at the wrong speed and pitch.
//
// -rate was wired end to end but nothing converted decoded PCM to that rate:
// -rate 48000 played a 440Hz source at 480Hz, 9% fast and 1.6 semitones sharp.
func TestDeckResamplesSourceToItsOwnRate(t *testing.T) {
	const sourceFreq = 440.0
	// A source at 44100, rendered by a deck at 48000.
	const srcFrames = 44100 * 4
	src := dsp.Stereo(make([]float32, srcFrames*2))
	for i := 0; i < srcFrames; i++ {
		v := float32(0.5 * math.Sin(2*math.Pi*sourceFreq*float64(i)/44100.0))
		src[i*2], src[i*2+1] = v, v
	}

	d := NewDeckAtRate(0, 48000)
	d.Load(&audio.Track{
		Path: "src", SampleRate: 44100, Channels: 2,
		Duration: 4 * time.Second, PCM: src,
	})
	d.Play()

	out := make(dsp.Stereo, BlockFrames*2)
	acc := make(dsp.Stereo, 0, 2000*BlockFrames*2)
	for i := 0; i < 2000; i++ {
		n := d.Render(out)
		acc = append(acc, out[:n*2]...)
	}

	got := measuredFreqAt(acc, 48000)
	if math.Abs(got-sourceFreq) > sourceFreq*0.01 {
		t.Errorf("44100Hz source rendered at 48000 produced %.1f Hz, want ~%.0f Hz",
			got, sourceFreq)
	}
}

// At the source's own rate the output must be transparent.
func TestDeckAtSourceRateIsTransparent(t *testing.T) {
	src := dsp.Sine(440, 8.0)
	d := NewDeckAtRate(0, dsp.SampleRate)
	d.Load(&audio.Track{
		Path: "src", SampleRate: dsp.SampleRate, Channels: 2,
		Duration: 8 * time.Second, PCM: src,
	})
	d.Play()

	out := make(dsp.Stereo, BlockFrames*2)
	acc := make(dsp.Stereo, 0, 2000*BlockFrames*2)
	for i := 0; i < 2000; i++ {
		n := d.Render(out)
		acc = append(acc, out[:n*2]...)
	}
	got := measuredFreq(acc)
	if math.Abs(got-440) > 4.4 {
		t.Errorf("unity-rate render measured %.1f Hz, want ~440", got)
	}
}

// The engine's advertised rate must be the rate it actually renders at, or the
// stream declaration and the audio disagree.
func TestEngineRateMatchesWhatItRenders(t *testing.T) {
	const rate = 48000
	e := New(Config{SampleRate: rate, BlockFrames: BlockFrames,
		RingFrames: 44100, DeckCount: 1})
	if got := e.Deck(0).SampleRate(); got != rate {
		t.Fatalf("deck rate = %d, want %d", got, rate)
	}
	e.Deck(0).Load(&audio.Track{
		Path: "src", SampleRate: 44100, Channels: 2,
		Duration: 8 * time.Second, PCM: dsp.Sine(440, 8.0),
	})
	e.Deck(0).Play()

	main := ringFor(e)
	cue := ringFor(e)
	tmp := make(dsp.Stereo, 2*BlockFrames)
	acc := make(dsp.Stereo, 0, 2000*BlockFrames*2)
	for i := 0; i < 1500; i++ {
		if err := e.RenderInto(main, cue); err != nil {
			t.Fatal(err)
		}
		for n := main.Read(tmp); n > 0; n = main.Read(tmp) {
			acc = append(acc, tmp[:n*2]...)
		}
	}
	got := measuredFreqAt(acc, rate)
	if math.Abs(got-440) > 4.4 {
		t.Errorf("48000Hz engine rendered a 44100Hz source at %.1f Hz, want ~440", got)
	}
}

// ringFor makes a ring sized for the engine under test.
func ringFor(e *Engine) *audio.Ring {
	return audio.NewRing(e.Config().RingFrames)
}
