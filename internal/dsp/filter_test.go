package dsp

import (
	"math"
	"testing"
)

func TestFilterLowpassAttenuatesAboveCutoff(t *testing.T) {
	f := NewFilter()
	f.SetKind(false)      // lowpass
	f.SetParams(200, 1.0) // 200Hz, Q=1
	in := Sine(2000, 0.3)
	out := make(Stereo, len(in))
	f.Process(in, out)
	// A 2-pole lowpass one decade above cutoff is ~40dB down. The bound is
	// loose because Q=1 gives a gentler skirt than Butterworth.
	if out.RMS() > 0.05*in.RMS() {
		t.Errorf("lowpass 200Hz let 2kHz through: %v vs %v", out.RMS(), in.RMS())
	}
}

func TestFilterHighpassAttenuatesBelowCutoff(t *testing.T) {
	f := NewFilter()
	f.SetKind(true)
	f.SetParams(200, 1.0)
	in := Sine(100, 0.3)
	out := make(Stereo, len(in))
	f.Process(in, out)
	// 100Hz is half an octave below a 200Hz cutoff: about -11dB for a 2-pole
	// at Q=1. Anything above -9dB would mean the filter is not working.
	if out.RMS() > 0.35*in.RMS() {
		t.Errorf("highpass 200Hz barely attenuated 100Hz: %v vs %v", out.RMS(), in.RMS())
	}
}

// TestFilterIsButterworthAtCutoff pins the -3dB point, which is what makes the
// filter knob feel calibrated: a knob at a given position must attenuate by a
// predictable amount.
func TestFilterIsButterworthAtCutoff(t *testing.T) {
	for _, hp := range []bool{false, true} {
		f := NewFilter()
		f.SetKind(hp)
		f.SetParams(1000, 0.707)
		in := Sine(1000, 0.3)
		out := make(Stereo, len(in))
		f.Process(in, out)
		g := out.RMS() / in.RMS()
		if math.Abs(g-0.707) > 0.05 {
			t.Errorf("hp=%v: gain at cutoff = %v, want 0.707 (-3dB)", hp, g)
		}
	}
}

// TestFilterSlopeIsTwoPoles verifies the rolloff is what a 2-pole lowpass
// gives: -12dB/octave. Two octaves above a 200Hz cutoff is 800Hz, where the
// response should be about 1/16 (-24dB).
func TestFilterSlopeIsTwoPoles(t *testing.T) {
	f := NewFilter()
	f.SetKind(false)
	f.SetParams(200, 0.707)
	in := Sine(800, 0.3)
	out := make(Stereo, len(in))
	f.Process(in, out)
	g := out.RMS() / in.RMS()
	if g > 0.12 || g < 0.03 {
		t.Errorf("2-pole lowpass gain two octaves up = %v, want ~0.06 (-24dB)", g)
	}
}

func TestFilterLowpassPassesBelowCutoff(t *testing.T) {
	f := NewFilter()
	f.SetKind(false)
	f.SetParams(5000, 0.707)
	in := Sine(200, 0.3)
	out := make(Stereo, len(in))
	f.Process(in, out)
	if out.RMS() < 0.5*in.RMS() {
		t.Errorf("lowpass 5kHz blocked 200Hz: %v vs %v", out.RMS(), in.RMS())
	}
}

func TestFilterHighpassPassesAboveCutoff(t *testing.T) {
	f := NewFilter()
	f.SetKind(true)
	f.SetParams(200, 0.707)
	in := Sine(5000, 0.3)
	out := make(Stereo, len(in))
	f.Process(in, out)
	if out.RMS() < 0.5*in.RMS() {
		t.Errorf("highpass 200Hz blocked 5kHz: %v vs %v", out.RMS(), in.RMS())
	}
}

func TestFilterClampsAbsurdCutoff(t *testing.T) {
	f := NewFilter()
	f.SetParams(1e9, 0.01) // must clamp, must not panic or produce NaN
	in := Sine(1000, 0.1)
	out := make(Stereo, len(in))
	f.Process(in, out)
	if math.IsNaN(out.RMS()) || math.IsInf(out.RMS(), 0) {
		t.Fatalf("clamped filter produced %v", out.RMS())
	}
	if out.RMS() < 1e-6 {
		t.Error("clamped lowpass produced silence")
	}
}

func TestFilterClampsNegativeCutoff(t *testing.T) {
	f := NewFilter()
	f.SetParams(-500, 1.0)
	in := Sine(1000, 0.1)
	out := make(Stereo, len(in))
	f.Process(in, out)
	if math.IsNaN(out.RMS()) {
		t.Fatal("negative cutoff produced NaN")
	}
}

func TestFilterCentreIsNearUnity(t *testing.T) {
	// At the cutoff frequency a 2-pole filter is -3dB, so within 30% is a
	// generous but meaningful bound.
	f := NewFilter()
	f.SetKind(false)
	f.SetParams(1000, 0.707)
	in := Sine(1000, 0.3)
	out := make(Stereo, len(in))
	f.Process(in, out)
	if r := out.RMS() / in.RMS(); math.Abs(r-1.0) > 0.3 {
		t.Errorf("cutoff frequency attenuated by %v", r)
	}
}

func TestFilterAliasedInPlace(t *testing.T) {
	f := NewFilter()
	f.SetParams(1000, 1.0)
	s := Sine(4000, 0.1)
	f.Process(s, s) // must not corrupt
	if s.Peak() > 1.5 {
		t.Errorf("aliased filter produced peak %v", s.Peak())
	}
}

func TestFilterReset(t *testing.T) {
	f := NewFilter()
	f.SetParams(1000, 1.0)
	warm := Sine(4000, 0.05)
	buf := make(Stereo, len(warm))
	f.Process(warm, buf)
	f.Reset()
	in := Sine(4000, 0.2)
	out := make(Stereo, len(in))
	f.Process(in, out)
	if math.IsNaN(out.RMS()) {
		t.Fatal("after Reset, filter produced NaN")
	}
}

func TestFilterSwitchingKindIsSafe(t *testing.T) {
	f := NewFilter()
	f.SetParams(1000, 1.0)
	in := Sine(1000, 0.1)
	out := make(Stereo, len(in))
	f.Process(in, out)
	f.SetKind(true) // switch mid-stream must not panic or NaN
	f.Process(in, out)
	if math.IsNaN(out.RMS()) {
		t.Fatal("kind switch produced NaN")
	}
}

func TestFilterHandlesEmptyBuffers(t *testing.T) {
	f := NewFilter()
	f.Process(nil, nil)
	out := make(Stereo, 512)
	f.Process(nil, out)
	if out.Peak() != 0 {
		t.Errorf("filter produced %v from empty input", out.Peak())
	}
}

func BenchmarkFilterProcess(b *testing.B) {
	const block = 512
	in := Sine(1000, 2.0)
	out := make(Stereo, block*2)
	f := NewFilter()
	f.SetParams(2000, 1.0)
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		f.Process(in[:block*2], out)
	}
}
