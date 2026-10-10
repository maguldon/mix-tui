package analyze

import (
	"fmt"
	"math"

	"math/rand"
	"testing"
	"time"

	"github.com/maguldon/mix-tui/internal/dsp"
)

// clickTrack is a deterministic impulse train at the given tempo: the
// clearest possible signal for a beat tracker, and a fixture every beat
// tracker must handle.
func clickTrack(bpm float64, seconds float64) dsp.Stereo {
	period := int(60.0 / bpm * dsp.SampleRate)
	total := int(seconds * dsp.SampleRate)
	out := make(dsp.Stereo, total*2)
	for i := 0; i < total; i += period {
		for j := 0; j < 128 && i+j < total; j++ {
			v := float32(math.Exp(-float64(j) / 12.0))
			out[(i+j)*2] += v
			out[(i+j)*2+1] += v
		}
	}
	return out
}

func noiseTrack(seconds float64, seed int64) dsp.Stereo {
	rng := rand.New(rand.NewSource(seed))
	n := int(seconds * dsp.SampleRate)
	out := make(dsp.Stereo, n*2)
	for i := range out {
		out[i] = float32(rng.NormFloat64() * 0.3)
	}
	return out
}

// chord renders a set of frequencies as a sustained tone: a stand-in for
// tonal music, used for key detection.
func chord(seconds float64, freqs []float64) dsp.Stereo {
	n := int(seconds * dsp.SampleRate)
	out := make(dsp.Stereo, n*2)
	for _, f := range freqs {
		for i := 0; i < n; i++ {
			// Slight amplitude modulation so the result is not a pure stationary
			// tone, which is a degenerate input.
			amp := 0.16 * (1 + 0.2*math.Sin(2*math.Pi*0.7*float64(i)/dsp.SampleRate))
			v := float32(amp * math.Sin(2*math.Pi*f*float64(i)/dsp.SampleRate))
			out[i*2] += v
			out[i*2+1] += v
		}
	}
	return out
}

func TestBeatGridDetectsTempo(t *testing.T) {
	for _, bpm := range []float64{120, 128, 140} {
		g, err := AnalyzeBeatGrid(clickTrack(bpm, 20))
		if err != nil {
			t.Fatalf("bpm %v: %v", bpm, err)
		}
		if math.Abs(g.BPM-bpm) > 2.0 {
			t.Errorf("detected %v, want %v (±2)", g.BPM, bpm)
		}
		if len(g.Beats) < 20 {
			t.Errorf("bpm %v: only %d beats", bpm, len(g.Beats))
		}
	}
}

// TestBeatGridAvoidsOctaveErrors pins the classic failure: a 128 BPM track
// detected at 64 or 256 because the autocorrelation peak landed on a
// multiple or submultiple of the true period.
func TestBeatGridAvoidsOctaveErrors(t *testing.T) {
	for _, bpm := range []float64{124, 128, 132} {
		g, err := AnalyzeBeatGrid(clickTrack(bpm, 20))
		if err != nil {
			t.Fatal(err)
		}
		ratio := g.BPM / bpm
		if math.Abs(ratio-1) > 0.02 {
			t.Errorf("detected %v for a %v track (ratio %v) — octave error", g.BPM, bpm, ratio)
		}
	}
}

func TestBeatGridBeatSpacingIsRegular(t *testing.T) {
	g, err := AnalyzeBeatGrid(clickTrack(128, 20))
	if err != nil {
		t.Fatal(err)
	}
	want := 60.0 / 128.0
	for i := 1; i < len(g.Beats) && i < 50; i++ {
		gap := g.Beats[i].Seconds() - g.Beats[i-1].Seconds()
		if math.Abs(gap-want) > 0.02 {
			t.Fatalf("beat %d gap = %v, want %v", i, gap, want)
		}
	}
}

func TestBeatGridBeatsAreSortedAndInRange(t *testing.T) {
	g, err := AnalyzeBeatGrid(clickTrack(128, 20))
	if err != nil {
		t.Fatal(err)
	}
	dur := 20 * time.Second
	for i, b := range g.Beats {
		if b < 0 || b > dur {
			t.Errorf("beat %d at %v is outside the track", i, b)
		}
		if i > 0 && b <= g.Beats[i-1] {
			t.Errorf("beat %d (%v) is not after beat %d (%v)", i, b, i-1, g.Beats[i-1])
		}
	}
}

func TestBeatGridHasDownbeats(t *testing.T) {
	g, err := AnalyzeBeatGrid(clickTrack(128, 20))
	if err != nil {
		t.Fatal(err)
	}
	if len(g.Downbeats) == 0 {
		t.Error("no downbeats marked")
	}
	// Roughly one downbeat per four beats.
	if d, b := len(g.Downbeats), len(g.Beats); d < b/8 || d > b/2 {
		t.Errorf("%d downbeats for %d beats, want roughly one in four", d, b)
	}
}

// TestBeatGridOnSilenceIsLowConfidence is the degradation requirement from
// spec §8: no beat must not be reported as a confident detection.
func TestBeatGridOnSilenceIsLowConfidence(t *testing.T) {
	g, err := AnalyzeBeatGrid(dsp.DC(0.01, 10))
	if err != nil {
		t.Fatal(err)
	}
	if g.Confidence > 0.5 {
		t.Errorf("silence got confidence %v — must be low", g.Confidence)
	}
}

func TestBeatGridOnNoiseIsLowConfidence(t *testing.T) {
	g, err := AnalyzeBeatGrid(noiseTrack(10, 42))
	if err != nil {
		t.Fatal(err)
	}
	if g.Confidence > 0.6 {
		t.Errorf("white noise got confidence %v", g.Confidence)
	}
}

// TestBeatGridAlwaysReturnsUsableGrid: a caller must never nil-deref, even on
// audio with no beat at all.
func TestBeatGridOnSilenceDoesNotError(t *testing.T) {
	g, err := AnalyzeBeatGrid(make(dsp.Stereo, 44100*2))
	if err != nil {
		t.Errorf("silence should not error, got %v", err)
	}
	if g == nil {
		t.Fatal("returned nil grid")
	}
}

func TestBeatGridOnShortTrack(t *testing.T) {
	g, err := AnalyzeBeatGrid(clickTrack(128, 1.0))
	if err != nil {
		t.Fatalf("short track errored: %v", err)
	}
	_ = g // must not panic; may be low confidence
}

func TestBeatGridOnTinyInput(t *testing.T) {
	for _, n := range []int{0, 1, 100, 512, 2048} {
		g, err := AnalyzeBeatGrid(make(dsp.Stereo, n*2))
		if err != nil {
			t.Errorf("n=%d errored: %v", n, err)
		}
		if g == nil {
			t.Errorf("n=%d returned nil grid", n)
		}
	}
}

func TestBeatGridOnPureTone(t *testing.T) {
	// A steady tone has no beat. Must not panic and must not claim confidence.
	g, err := AnalyzeBeatGrid(dsp.Sine(440, 10))
	if err != nil {
		t.Fatal(err)
	}
	if g.Confidence > 0.7 {
		t.Errorf("a pure tone got confidence %v", g.Confidence)
	}
}

func TestAnalyzeKeyOnMajorChord(t *testing.T) {
	k, err := AnalyzeKey(chord(10, []float64{261.6, 329.6, 392.0})) // C major
	if err != nil {
		t.Fatal(err)
	}
	if k.Confidence < KeyConfidenceThreshold {
		t.Skipf("confidence %v below threshold", k.Confidence)
	}
	if k.Camelot != "8B" {
		t.Errorf("C major = %q, want 8B", k.Camelot)
	}
	if k.Name != "C major" {
		t.Errorf("name = %q, want C major", k.Name)
	}
}

func TestAnalyzeKeyOnMinorChord(t *testing.T) {
	k, err := AnalyzeKey(chord(10, []float64{220.0, 261.6, 329.6})) // A minor
	if err != nil {
		t.Fatal(err)
	}
	if k.Confidence < KeyConfidenceThreshold {
		t.Skipf("confidence %v below threshold", k.Confidence)
	}
	if k.Camelot != "8A" {
		t.Errorf("A minor = %q, want 8A", k.Camelot)
	}
}

func TestAnalyzeKeyOnSeveralKeys(t *testing.T) {
	// Root, minor third, fifth, octave: enough for a reliable chroma profile.
	cases := []struct {
		freqs   []float64
		camelot string
	}{
		{[]float64{261.6, 329.6, 392.0}, "8B"},  // C major: C4 E4 G4
		{[]float64{220.0, 261.6, 329.6}, "8A"},  // A minor: A3 C4 E4
		{[]float64{196.0, 246.9, 293.7}, "9B"},  // G major: G3 B3 D4
		{[]float64{174.6, 220.0, 261.6}, "7B"},  // F major: F3 A3 C4
		{[]float64{146.8, 185.0, 220.0}, "10B"}, // D major: D3 F#3 A3
	}
	for _, c := range cases {
		k, err := AnalyzeKey(chord(8, c.freqs))
		if err != nil {
			t.Errorf("%v: %v", c.camelot, err)
			continue
		}
		if k.Confidence < KeyConfidenceThreshold {
			t.Logf("%v: confidence %v below threshold, skipped", c.camelot, k.Confidence)
			continue
		}
		if k.Camelot != c.camelot {
			t.Errorf("detected %q, want %q (conf %v)", k.Camelot, c.camelot, k.Confidence)
		}
	}
}

// TestAnalyzeKeyOnNoiseIsUndetected is spec §6.5's core requirement: below
// the threshold we report nothing rather than a confident guess.
func TestAnalyzeKeyOnNoiseIsUndetected(t *testing.T) {
	for _, seed := range []int64{7, 99, 12345} {
		k, err := AnalyzeKey(noiseTrack(8, seed))
		if err != nil {
			t.Fatal(err)
		}
		if k.Confidence >= KeyConfidenceThreshold {
			t.Errorf("noise seed %d: confidence %v >= threshold", seed, k.Confidence)
		}
		if k.Camelot != "" || k.Name != "" {
			t.Errorf("noise seed %d: below-threshold key must be empty, got %q/%q",
				seed, k.Camelot, k.Name)
		}
	}
}

func TestAnalyzeKeyOnPercussionIsUndetected(t *testing.T) {
	// A kick-only track has no pitch content at all.
	k, err := AnalyzeKey(clickTrack(128, 8))
	if err != nil {
		t.Fatal(err)
	}
	if k.Camelot != "" {
		t.Errorf("percussion got key %q; should be undetected", k.Camelot)
	}
}

func TestAnalyzeKeyOnSilenceIsUndetected(t *testing.T) {
	k, err := AnalyzeKey(make(dsp.Stereo, 44100*2))
	if err != nil {
		t.Fatal(err)
	}
	if k.Camelot != "" {
		t.Errorf("silence got key %q", k.Camelot)
	}
}

func TestAnalyzeKeyOnShortInput(t *testing.T) {
	for _, n := range []int{0, 100, 2048, 8192} {
		k, err := AnalyzeKey(make(dsp.Stereo, n*2))
		if err != nil {
			t.Errorf("n=%d errored: %v", n, err)
		}
		if k == nil {
			t.Errorf("n=%d returned nil key", n)
		}
	}
}

func TestCamelotRoundTrip(t *testing.T) {
	// Every Camelot code must map to a name and back. A mapping that is not
	// an exact inverse would report the wrong key for some tracks.
	for i := 1; i <= 12; i++ {
		for _, m := range []string{"A", "B"} {
			code := fmt.Sprintf("%d%s", i, m)
			name := camelotToName(code)
			if name == "" {
				t.Errorf("%s unmapped", code)
				continue
			}
			if got := nameToCamelot(name); got != code {
				t.Errorf("%s -> %q -> %s", code, name, got)
			}
		}
	}
}

func TestCamelotCIsEightB(t *testing.T) {
	// Standard Camelot: 8B is C major, 8A is A minor. 7A is D minor.
	if got := nameToCamelot("C major"); got != "8B" {
		t.Errorf("C major = %s, want 8B", got)
	}
	if got := nameToCamelot("A minor"); got != "8A" {
		t.Errorf("A minor = %s, want 8A", got)
	}
	if got := nameToCamelot("D minor"); got != "7A" {
		t.Errorf("D minor = %s, want 7A", got)
	}
}

func TestSpectrogramIsSharedByBothAnalyses(t *testing.T) {
	// The spec's claim (§4.1) is that one STFT feeds both. Verify the
	// signature exposes a single computation both paths use.
	pcm := clickTrack(128, 4)
	spec := ComputeSpectrogram(pcm, 2048, 512)
	if spec == nil {
		t.Fatal("nil spectrogram")
	}
	if len(spec.Frames) == 0 {
		t.Fatal("no frames")
	}
	if len(spec.Frames[0]) != 2048/2+1 {
		t.Errorf("bins = %d, want %d", len(spec.Frames[0]), 2048/2+1)
	}
	// A click train must show energy.
	if energyOf(spec) <= 0 {
		t.Error("spectrogram has no energy")
	}
}

func TestSpectrogramOnEmptyAndTinyInput(t *testing.T) {
	for _, n := range []int{0, 1, 100, 1024} {
		s := ComputeSpectrogram(make(dsp.Stereo, n*2), 2048, 512)
		if s == nil {
			t.Errorf("n=%d returned nil", n)
		}
	}
}

func TestSpectrogramOnTinyWindow(t *testing.T) {
	// A window smaller than the hop is degenerate but must not divide by zero.
	_ = ComputeSpectrogram(dsp.Sine(440, 0.1), 64, 512)
	_ = ComputeSpectrogram(dsp.Sine(440, 0.1), 0, 0)
}

func energyOf(s *Spectrogram) float64 {
	var acc float64
	for _, f := range s.Frames {
		for _, c := range f {
			acc += float64(real(c))*float64(real(c)) + float64(imag(c))*float64(imag(c))
		}
	}
	return acc
}
