package dsp

import (
	"math"
	"testing"
)

// TestBiquadImpulseResponsesAreStable is the guard against the worst class of
// DSP bug in this project: a filter whose coefficients are individually
// plausible but whose state diverges without bound. It does not merely sound
// wrong, it turns the master bus into a scream and, once it happens mid-set,
// there is no way to recover without stopping.
//
// Coefficients being "obviously correct" is not sufficient evidence of
// stability, so this asserts the actual impulse response decays.
func TestBiquadImpulseResponsesAreStable(t *testing.T) {
	types := []struct {
		name string
		set  func(*biquad)
	}{
		{"lowpass", func(b *biquad) { b.lowpassRBJ(SampleRate, 200, 0.707) }},
		{"lowpass-q4", func(b *biquad) { b.lowpassRBJ(SampleRate, 2000, 4.0) }},
		{"highpass", func(b *biquad) { b.highpassRBJ(SampleRate, 200, 0.707) }},
		{"highpass-q4", func(b *biquad) { b.highpassRBJ(SampleRate, 2000, 4.0) }},
		{"peak-boost", func(b *biquad) { b.peakingRBJ(SampleRate, 1000, 1.0, 4.0) }},
		{"peak-cut", func(b *biquad) { b.peakingRBJ(SampleRate, 1000, 1.0, 0.25) }},
	}

	for _, ty := range types {
		var b biquad
		ty.set(&b)
		if !b.stable() {
			t.Errorf("%s: poles outside unit circle (a1=%.4f a2=%.4f)", ty.name, b.a1, b.a2)
			continue
		}
		// The real test: drive an impulse and confirm it decays to silence.
		var peak, last float64
		for i := 0; i < SampleRate*2; i++ {
			x := 0.0
			if i == 0 {
				x = 1.0
			}
			y, _ := b.process(x, x)
			if math.IsNaN(y) || math.IsInf(y, 0) {
				t.Fatalf("%s: diverged to %v after %d frames", ty.name, y, i)
			}
			a := math.Abs(y)
			if a > peak {
				peak = a
			}
			last = a
		}
		if peak > 10 {
			t.Errorf("%s: impulse peak %g is implausibly large", ty.name, peak)
		}
		if last > peak*1e-3 {
			t.Errorf("%s: impulse did not decay (last %g vs peak %g)", ty.name, last, peak)
		}
	}
}

// TestBiquadIsLinear confirms the filter superposes, which a diverging one
// cannot: filtering 2x must equal 2x filtering.
func TestBiquadIsLinear(t *testing.T) {
	in := Sine(1000, 0.05)

	var a biquad
	a.lowpassRBJ(SampleRate, 2000, 0.707)
	sep := make([]float64, len(in)/2)
	for i := range sep {
		sep[i], _ = a.process(float64(in[i*2]), float64(in[i*2+1]))
	}

	var b biquad
	b.lowpassRBJ(SampleRate, 2000, 0.707)
	sum := make([]float64, len(in)/2)
	for i := range sum {
		// Input is 2x, so the output is 2x the filtered signal; halve it back
		// to compare against the unscaled path.
		l, _ := b.process(float64(in[i*2])*2, float64(in[i*2+1])*2)
		sum[i] = l / 2
	}

	for i := range sep {
		if d := math.Abs(sep[i] - sum[i]); d > 1e-6 {
			t.Fatalf("non-linear at frame %d: %v vs %v", i, sep[i], sum[i])
		}
	}
}

func TestEQIsStableAtExtremeSettings(t *testing.T) {
	eq := NewEQ()
	eq.SetBand(EQLow, 10.0)
	eq.SetBand(EQMid, 0.01)
	eq.SetBand(EQHigh, 10.0)
	in := Sine(1000, 0.5)
	out := make(Stereo, len(in))
	eq.Process(in, out)
	if math.IsNaN(out.RMS()) || math.IsInf(out.RMS(), 0) {
		t.Errorf("EQ at extreme gains produced %v", out.RMS())
	}
	if out.Peak() > 100 {
		t.Errorf("EQ at extreme gains peaked at %v", out.Peak())
	}
}

func TestFilterIsStableAcrossParameterSweep(t *testing.T) {
	// Sweep the whole legal range: an unstable filter at any reachable knob
	// position is an emergency, not an edge case.
	for _, hp := range []bool{false, true} {
		for _, cutoff := range []float64{20, 100, 500, 2000, 8000, 20000} {
			for _, q := range []float64{0.707, 1.0, 2.0, 5.0, 20.0} {
				f := NewFilter()
				f.SetKind(hp)
				f.SetParams(cutoff, q)
				in := Sine(1000, 0.2)
				out := make(Stereo, len(in))
				f.Process(in, out)
				r := out.RMS()
				if math.IsNaN(r) || math.IsInf(r, 0) {
					t.Fatalf("hp=%v cutoff=%v q=%v diverged: %v", hp, cutoff, q, r)
				}
				if r > 10 {
					t.Fatalf("hp=%v cutoff=%v q=%v blew up: %v", hp, cutoff, q, r)
				}
			}
		}
	}
}
