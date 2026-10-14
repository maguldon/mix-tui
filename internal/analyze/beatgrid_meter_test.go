package analyze

import (
	"testing"
	"time"
)

// fakeSpec builds the minimum a meter test needs: an onset envelope indexed by
// frame, plus the spectrogram geometry that converts a beat time to a frame.
func fakeSpec(env []float64, sampleRate, hop int) *Spectrogram {
	return &Spectrogram{Frames: make([][]complex64, len(env)), WindowLen: 1024, HopSize: hop, SampleRate: sampleRate}
}

// TestDetectMeterPrefersTheAccentedCycle is the regression test for the
// hardcoded "every fourth beat is a bar start".
//
// markDownbeats assumed 4/4 unconditionally, so 3/4 and 7/8 material had its bar
// lines drawn on the wrong beat -- which matters in the UI because the downbeat
// markers and the loop grid are drawn from them.
func TestDetectMeterPrefersTheAccentedCycle(t *testing.T) {
	const (
		sr, hop, period = 44100, 512, 1024 // one beat every 1024 frames
		nBeats          = 28
	)
	// The envelope must cover every beat: onsetAt clamps, so a beat past the end
	// reads as silence rather than as "no accent" -- and a fixture that does
	// that is testing the clamp, not the detector.
	envLen := nBeats*period + 8
	beats := make([]time.Duration, 0, nBeats)
	var env []float64

	// A strong onset on the bar's first beat, a weak one everywhere else.
	// The contrast is the whole signal: if weak beats are the same strength as
	// accents there is nothing for a detector to find, and asking it to guess
	// would be asking it to invent.
	place := func(meter int) []time.Duration {
		bs := make([]time.Duration, 0, nBeats)
		for i := 0; i < nBeats; i++ {
			tb := time.Duration(i*period*sr/hop) * time.Second / time.Duration(sr)
			bs = append(bs, tb)
			f := int(float64(tb)/float64(time.Second)) * sr / hop
			if f >= len(env) {
				continue
			}
			if i%meter == 0 {
				env[f] = 1
			} else {
				env[f] = 0.05
			}
		}
		return bs
	}

	for _, tc := range []struct {
		meter int
		want  int
	}{{4, 4}, {3, 3}, {7, 7}, {6, 6}, {5, 5}} {
		env = make([]float64, envLen)
		beats = place(tc.meter)
		if got := detectMeter(beats, env, fakeSpec(env, sr, hop)); got != tc.want {
			t.Errorf("detectMeter on %d beats per bar = %d, want %d", tc.meter, got, tc.want)
		}
	}
}

// TestDetectMeterDefaultsToFourOnFlatMaterial keeps the prior honest: with no
// accent pattern at all, nothing should be claimed about the bar length. A meter
// invented from evenly strong beats would draw bar lines at random.
func TestDetectMeterDefaultsToFourOnFlatMaterial(t *testing.T) {
	const sr, hop, period, nBeats = 44100, 512, 1024, 28
	env := make([]float64, nBeats*period+8)
	var beats []time.Duration
	for i := 0; i < nBeats; i++ {
		bs := time.Duration(i*period*sr/hop) * time.Second / time.Duration(sr)
		beats = append(beats, bs)
		f := int(float64(bs)/float64(time.Second)) * sr / hop
		if f < len(env) {
			env[f] = 0.5 // every beat equally strong
		}
	}
	if got := detectMeter(beats, env, fakeSpec(env, sr, hop)); got != 4 {
		t.Errorf("evenly strong beats gave meter %d, want the 4/4 default", got)
	}
}

// TestMarkDownbeatsSpacesByTheDetectedMeter is the part that reaches the UI:
// downbeats are indices into Beats, and the waveform draws a marker at each.
func TestMarkDownbeatsSpacesByTheDetectedMeter(t *testing.T) {
	beats := make([]time.Duration, 12)
	for i := range beats {
		beats[i] = time.Duration(i) * time.Second
	}
	for _, tc := range []struct {
		meter int
		want  []int
	}{
		{4, []int{0, 4, 8}},
		{3, []int{0, 3, 6, 9}},
		{7, []int{0, 7}},
	} {
		got := markDownbeats(beats, tc.meter)
		if len(got) != len(tc.want) {
			t.Errorf("markDownbeats(meter=%d) = %v, want %v", tc.meter, got, tc.want)
			continue
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("markDownbeats(meter=%d) = %v, want %v", tc.meter, got, tc.want)
				break
			}
		}
	}
}

// TestShortTracksKeepFour keeps a two-beat fragment from claiming a meter it
// cannot possibly show.
func TestShortTracksKeepFour(t *testing.T) {
	beats := []time.Duration{0, time.Second}
	if got := detectMeter(beats, []float64{1, 1, 1}, fakeSpec([]float64{1, 1, 1}, 44100, 512)); got != 4 {
		t.Errorf("a two-beat fragment gave meter %d, want 4", got)
	}
}
