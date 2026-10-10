package dsp

import (
	"math"
	"testing"
)

func TestStereoFramesAndRMS(t *testing.T) {
	s := Stereo{1, -1, 1, -1} // 2 frames, full scale
	if got := s.Frames(); got != 2 {
		t.Errorf("Frames() = %d, want 2", got)
	}
	if got := s.RMS(); math.Abs(got-1.0) > 1e-6 {
		t.Errorf("RMS() = %v, want 1.0", got)
	}
}

func TestStereoPeakIsMaxAbsSample(t *testing.T) {
	s := Stereo{0.1, -0.9, 0.4, 0.2}
	if got := s.Peak(); math.Abs(got-0.9) > 1e-6 {
		t.Errorf("Peak() = %v, want 0.9", got)
	}
}

func TestStereoZeroCrossingRate(t *testing.T) {
	// A sine at f crosses zero 2f times per second. Measure against a
	// reference tone rather than an absolute frequency.
	ref := Sine(1000, 0.1)
	if got, want := ref.ZeroCrossingRate(), 2000.0; math.Abs(got-want) > 40 {
		t.Errorf("ZCR of a 1kHz sine = %v, want ~%v (2f convention)", got, want)
	}
}

func TestStereoZeroCrossingRateOfDCIsZero(t *testing.T) {
	if got := DC(0.5, 0.1).ZeroCrossingRate(); math.Abs(got) > 1e-9 {
		t.Errorf("DC ZCR = %v, want 0", got)
	}
}

func TestStereoDupIsIndependent(t *testing.T) {
	s := Sine(440, 0.01)
	c := s.Dup()
	c[0] = 99
	if s[0] == 99 {
		t.Error("Dup() shared backing memory with the original")
	}
}

func TestEnergyMatchesSumOfSquares(t *testing.T) {
	s := Stereo{1, 2, 3, 4}
	// Energy is over every sample in both channels, not the mono sum.
	want := 1.0 + 4.0 + 9.0 + 16.0
	if got := Energy(s); math.Abs(got-want) > 1e-6 {
		t.Errorf("Energy = %v, want %v", got, want)
	}
}

func TestEnergyDoesNotCancelOnAntiPhase(t *testing.T) {
	// A level measure reading zero on a hot signal would be useless for
	// verifying that resampling preserved gain.
	s := Stereo{1, -1, 1, -1}
	if Energy(s) < 3.9 {
		t.Errorf("anti-phase energy = %v, want ~4", Energy(s))
	}
}
func TestMaxSampleDeltaOnPureToneIsBoundedBySlope(t *testing.T) {
	s := Sine(440, 0.1)
	d := MaxSampleDelta(s)
	maxSlope := 2 * math.Pi * 440 / SampleRate
	if d > maxSlope*1.5 {
		t.Errorf("MaxSampleDelta = %v, want <= %v", d, maxSlope)
	}
}

func TestMaxSampleDeltaSpikesOnDiscontinuity(t *testing.T) {
	// the whole point of MaxSampleDelta: it must notice a seam
	s := Stereo{0.5, 0.5, 0.5, 0.5, -0.5, -0.5, -0.5, -0.5}
	if d := MaxSampleDelta(s); d < 0.9 {
		t.Errorf("MaxSampleDelta = %v, want ~1.0 across the step", d)
	}
}

func TestClampNormalized(t *testing.T) {
	cases := []struct{ in, want float64 }{
		{0, 0}, {1, 1}, {-1, -1}, {1.5, 1}, {-2.3, -1}, {0.4, 0.4},
	}
	for _, c := range cases {
		if got := ClampNormalized(c.in); math.Abs(got-c.want) > 1e-9 {
			t.Errorf("ClampNormalized(%v) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestEmptyStereoIsSafe(t *testing.T) {
	s := Stereo{}
	if s.Frames() != 0 {
		t.Errorf("Frames() = %d", s.Frames())
	}
	if s.RMS() != 0 || s.Peak() != 0 || Energy(s) != 0 {
		t.Error("empty buffer reported non-zero measurements")
	}
	if s.ZeroCrossingRate() != 0 {
		t.Error("empty buffer reported a crossing rate")
	}
	if MaxSampleDelta(s) != 0 {
		t.Error("empty buffer reported a sample delta")
	}
}
