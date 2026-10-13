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

// drumPatternFor returns a buffer of `seconds` length holding drumPattern's
// first `active` seconds and silence after that.
//
// The silence has to be part of the signal, not a truncation of it: cutting the
// audio short would also cut the analysed span, so the detector would never see
// that the track goes quiet and coverage would still read as full.
func drumPatternFor(bpm, seconds, active float64) dsp.Stereo {
	out := make(dsp.Stereo, int(seconds*dsp.SampleRate)*2)
	copy(out, drumPattern(bpm, active))
	return out
}

// Confidence must account for how much of the track the grid actually covers.
//
// Regularity alone cannot see this: a short chain can be perfectly consistent,
// which is how a 6-beat grid over a 431-second track reported 0.60 confidence
// and left beat features switched on.
func TestBeatGridConfidenceFallsWithCoverage(t *testing.T) {
	const bpm = 128.0
	whole := drumPattern(bpm, 60)
	part := drumPatternFor(bpm, 60, 4) // 4s of beats, then 56s of silence

	full, err := AnalyzeBeatGrid(whole)
	if err != nil {
		t.Fatal(err)
	}
	sparse, err := AnalyzeBeatGrid(part)
	if err != nil {
		t.Fatal(err)
	}

	if sparse.Confidence >= full.Confidence {
		t.Errorf("a grid covering 4s of 60s scored %v, not below the full track's %v",
			sparse.Confidence, full.Confidence)
	}
	// 4/60 of the beats is ~7% coverage, well under the half-track threshold,
	// so confidence must collapse rather than merely dip.
	if sparse.Confidence > 0.3 {
		t.Errorf("7%% coverage got confidence %v, want <= 0.3", sparse.Confidence)
	}
	if full.Confidence < 0.5 {
		t.Errorf("full coverage got confidence %v, want >= 0.5", full.Confidence)
	}
}

// A real track with a breakdown should NOT be punished: coverage is full credit
// at half the track, because music legitimately goes quiet.
func TestBeatGridConfidenceToleratesGaps(t *testing.T) {
	pcm := drumPatternFor(128, 60, 40) // 40s of beats then 20s of silence
	g, err := AnalyzeBeatGrid(pcm)
	if err != nil {
		t.Fatal(err)
	}
	if g.Confidence < 0.5 {
		t.Errorf("two thirds coverage got confidence %v, want >= 0.5", g.Confidence)
	}
}

// Octave resolution must hold across the whole search range, including the
// tempi where the half-time peak genuinely outscores the true one.
//
// On synthetic kick-plus-hat patterns the half-time autocorrelation peak
// exceeds the true peak for some tempi -- 1.09x at 128 BPM, 1.23x at 150 BPM --
// because that is what a backbeat looks like. The old rules doubled the lag
// whenever the other peak was within 15%, which reported 150 BPM as 74.962.
// The existing octave test only covered 124/128/132 on a bare impulse train,
// where the peak is unambiguous and the rule never bit.
func TestBeatGridAvoidsOctaveErrorsAcrossRange(t *testing.T) {
	for _, bpm := range []float64{70, 85, 100, 108, 120, 128, 135, 140, 150, 160, 174, 190} {
		g, err := AnalyzeBeatGrid(drumPattern(bpm, 40))
		if err != nil {
			t.Fatalf("bpm %v: %v", bpm, err)
		}
		ratio := g.BPM / bpm
		if math.Abs(ratio-1) > 0.03 {
			t.Errorf("detected %v for a %v track (ratio %v) — octave error", g.BPM, bpm, ratio)
		}
	}
}

// preferredTempo must be symmetric in octaves: one octave below the centre is
// exactly as plausible as one above. Asymmetry is what makes a prior drag a
// tempo toward the middle instead of only breaking near-ties.
func TestPreferredTempoIsSymmetricInOctaves(t *testing.T) {
	half := preferredTempo(preferredBPM / 2)
	double := preferredTempo(preferredBPM * 2)
	if rel := math.Abs(half-double) / half; rel > 1e-9 {
		t.Errorf("an octave down is %v but an octave up is %v; want equal", half, double)
	}
	if got := preferredTempo(preferredBPM); math.Abs(got-1) > 1e-9 {
		t.Errorf("preferredTempo(%v) = %v, want 1", preferredBPM, got)
	}
	if got := preferredTempo(0); got != 0 {
		t.Errorf("preferredTempo(0) = %v, want 0", got)
	}
	// It must fall off, not merely be symmetric.
	if preferredTempo(preferredBPM*8) >= preferredTempo(preferredBPM) {
		t.Error("eight octaves up is no less plausible than the centre")
	}
}
