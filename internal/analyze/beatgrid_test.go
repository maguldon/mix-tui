package analyze

import (
	"math"
	"testing"
	"time"

	"github.com/maguldon/mix-tui/internal/dsp"
)

// drumPattern is a click track with hats on the off-beats: strong onsets on
// every beat, weaker ones halfway between.
//
// This is the shape of real music, and it is what exposes the beat-placement
// bug in trackBeats. The pure impulse train used by clickTrack has exactly one
// onset per beat, so the DP's inner search window is never exercised: the
// first candidate walked back to already sits one period away. Add off-beat
// onsets and the first predecessor becomes too close, which is where the
// inverted break/continue guards used to collapse the whole chain.
//
// It opens with lead-in silence. A hit at sample 0 sits wholly inside the first
// STFT frame, where there is no earlier frame to measure a flux rise against, so
// it is undetectable by construction; real tracks have lead-in, and without it
// the fixture's first beat can never be placed correctly.
func drumPattern(bpm, seconds float64) dsp.Stereo {
	const sr = dsp.SampleRate
	const leadIn = 0.5
	total := int(seconds * sr)
	out := make(dsp.Stereo, total*2)

	beat := 60.0 / bpm * sr
	// addHit drops a decaying burst at sample offset `at`.
	addHit := func(at int, amp float64, decay float64) {
		const hitLen = 256
		for j := 0; j < hitLen && at+j < total; j++ {
			v := float32(amp * math.Exp(-float64(j)/decay))
			out[(at+j)*2] += v
			out[(at+j)*2+1] += v
		}
	}
	for at := leadIn * sr; at < float64(total); at += beat {
		i := int(at)
		addHit(i, 1.0, 14)             // kick on the beat
		addHit(i+int(beat/2), 0.35, 6) // hat on the off-beat
	}
	return out
}

// A track with off-beat onsets must still get one beat per beat.
//
// This is the regression test for #3, where a 431-second track came back with
// 6 beats because the DP broke out of its search on the first candidate.
func TestBeatGridPlacesEveryBeatWithOffBeatOnsets(t *testing.T) {
	const seconds = 60.0
	for _, bpm := range []float64{100, 128, 140} {
		pcm := drumPattern(bpm, seconds)
		g, err := AnalyzeBeatGrid(pcm)
		if err != nil {
			t.Fatalf("bpm %v: %v", bpm, err)
		}
		want := int(seconds * bpm / 60)
		// The last beat may fall at or past the end of the window, so the
		// detected count can be one either side of want.
		lo, hi := want-2, want+1
		if len(g.Beats) < lo || len(g.Beats) > hi {
			t.Errorf("bpm %v: got %d beats, want %d..%d (lost %d)",
				bpm, len(g.Beats), lo, hi, want-len(g.Beats))
		}
	}
}

// Beats must be spaced at the beat period, not scattered. With the DP intact
// the gaps cluster; with it collapsed they vary by hundreds of percent.
func TestBeatGridGapsAreRegularWithOffBeatOnsets(t *testing.T) {
	pcm := drumPattern(128, 60)
	g, err := AnalyzeBeatGrid(pcm)
	if err != nil {
		t.Fatal(err)
	}
	if len(g.Beats) < 100 {
		t.Fatalf("only %d beats placed", len(g.Beats))
	}
	period := g.BeatPeriod().Seconds()
	for i := 1; i < len(g.Beats); i++ {
		gap := (g.Beats[i] - g.Beats[i-1]).Seconds()
		if rel := math.Abs(gap-period) / period; rel > 0.25 {
			t.Fatalf("gap %d = %.4fs, want %.4fs (±25%%)", i, gap, period)
		}
	}
}

// Beats must land on the kicks, not the hats: a hat-synchronous grid is off by
// half a beat, which is exactly the error a user feels as quantize drift.
func TestBeatGridAlignsToStrongOnsets(t *testing.T) {
	const bpm = 128.0
	const leadIn = 0.5
	pcm := drumPattern(bpm, 30)
	g, err := AnalyzeBeatGrid(pcm)
	if err != nil {
		t.Fatal(err)
	}
	beat := 60.0 / bpm
	if len(g.Beats) < 50 {
		t.Fatalf("only %d beats placed", len(g.Beats))
	}
	for i, b := range g.Beats {
		// Distance to the nearest kick, allowing for the lead-in.
		off := b.Seconds() - leadIn
		phase := math.Mod(math.Mod(off, beat)+beat, beat)
		if half := math.Min(phase, beat-phase); half > beat*0.1 {
			t.Errorf("beat %d at %.4fs is %.1f%% off the kick, want within 10%%",
				i, b.Seconds(), half/beat*100)
		}
	}
}

// BeatPeriod is the accessor a caller reaches for to size a loop. It was
// returning 1/BPM seconds instead of 60/BPM, i.e. 7.8ms at 128 BPM instead of
// 468.75ms. Nothing called it while it was wrong, so nothing caught it.
func TestBeatPeriodIsSixtySecondsOverBPM(t *testing.T) {
	cases := []struct {
		bpm  float64
		want time.Duration
	}{
		{128, 468750 * time.Microsecond},
		{120, 500 * time.Millisecond},
		{60, time.Second},
		{174, 344827 * time.Microsecond},
	}
	for _, c := range cases {
		got := (&BeatGrid{BPM: c.bpm}).BeatPeriod()
		if rel := math.Abs(float64(got-c.want)) / float64(c.want); rel > 0.001 {
			t.Errorf("BPM %v: BeatPeriod() = %v, want %v", c.bpm, got, c.want)
		}
	}
	if got := (&BeatGrid{BPM: 0}).BeatPeriod(); got != 0 {
		t.Errorf("BPM 0: BeatPeriod() = %v, want 0", got)
	}
	var nilGrid *BeatGrid
	if got := nilGrid.BeatPeriod(); got != 0 {
		t.Errorf("nil grid: BeatPeriod() = %v, want 0", got)
	}
}
