package dsp

import (
	"math"
	"testing"
)

func TestGainScalesInPlace(t *testing.T) {
	s := DC(1.0, 0.1)
	Gain(s, 0.5)
	if math.Abs(s.Peak()-0.5) > 1e-6 {
		t.Errorf("Gain gave %v, want 0.5", s.Peak())
	}
}

func TestGainUnityIsTransparent(t *testing.T) {
	s := Sine(440, 0.1)
	in := s.RMS()
	Gain(s, 1.0)
	if math.Abs(s.RMS()-in) > 1e-6 {
		t.Errorf("unity Gain changed RMS: %v -> %v", in, s.RMS())
	}
}

func TestGainZeroSilences(t *testing.T) {
	s := Sine(440, 0.1)
	Gain(s, 0)
	if s.Peak() != 0 {
		t.Errorf("Gain(0) left peak %v", s.Peak())
	}
}

func TestGainOnEmptyIsSafe(t *testing.T) {
	Gain(nil, 0.5)
}

func TestCrossfadeHardLeftIsSourceA(t *testing.T) {
	a := Sine(440, 0.1)
	b := Sine(880, 0.1)
	out := make(Stereo, len(a))
	Crossfade(out, a, b, -1.0)
	// At hard left the crossfader is all A and none B, so A's content is
	// preserved exactly.
	want := a.RMS()
	if math.Abs(out.RMS()-want) > 0.05 {
		t.Errorf("hard left RMS = %v, want A's %v", out.RMS(), want)
	}
}

func TestCrossfadeHardRightIsSourceB(t *testing.T) {
	a := Sine(440, 0.1)
	b := Sine(880, 0.1)
	out := make(Stereo, len(a))
	Crossfade(out, a, b, 1.0)
	if math.Abs(out.RMS()-b.RMS()) > 0.05 {
		t.Errorf("hard right RMS = %v, want B's %v", out.RMS(), b.RMS())
	}
}

func TestCrossfadeCentreIsConstantPower(t *testing.T) {
	// Equal-power crossfade: at centre each side is cos(45 deg), so two
	// uncorrelated sources sum to unity power rather than dipping.
	a := Sine(440, 0.2)
	b := Sine(997, 0.2) // different freq = uncorrelated enough
	out := make(Stereo, len(a))
	Crossfade(out, a, b, 0.0)
	in := a.RMS()
	got := out.RMS()
	// cos(pi/4)^2 + sin(pi/4)^2 == 1
	if math.Abs(got/in-1.0) > 0.15 {
		t.Errorf("centre RMS = %v, want ~%v (constant power)", got, in)
	}
}

func TestCrossfadeHoldsPowerForUncorrelatedSources(t *testing.T) {
	// The reason for the cos/sin curve: two uncorrelated sources at the
	// centre must not dip. Different frequencies stand in for independence.
	a := Sine(440, 0.2)
	b := Sine(997, 0.2)
	out := make(Stereo, len(a))
	var worst float64
	prev := 0.0
	for i := 0; i <= 20; i++ {
		x := -1.0 + 0.1*float64(i)
		Crossfade(out, a, b, x)
		got := out.RMS() / a.RMS()
		if d := math.Abs(got - 1.0); d > worst {
			worst = d
		}
		if got < prev-1e-4 {
			t.Fatalf("level dipped at x=%v: %v after %v", x, got, prev)
		}
		prev = got
	}
	if worst > 0.15 {
		t.Errorf("constant-power crossfade varied by %v across the throw", worst)
	}
}

func TestCrossfadePeaksMidThrowOnCorrelatedSources(t *testing.T) {
	// Documented consequence of constant-power: two IN-PHASE sources add
	// coherently and peak at the centre. Real decks play different tracks, so
	// this is the wrong model for a mix -- but the behaviour is pinned so a
	// future change is deliberate rather than accidental.
	a := DC(1.0, 0.05)
	b := DC(1.0, 0.05)
	out := make(Stereo, len(a))
	Crossfade(out, a, b, -1.0)
	left := out.Peak()
	Crossfade(out, a, b, 0.0)
	mid := out.Peak()
	Crossfade(out, a, b, 1.0)
	right := out.Peak()

	if math.Abs(left-1.0) > 1e-3 || math.Abs(right-1.0) > 1e-3 {
		t.Errorf("hard positions should be unity: left=%v right=%v", left, right)
	}
	if mid <= left || mid <= right {
		t.Errorf("in-phase sources should peak mid-throw, got mid=%v", mid)
	}
}

func TestCrossfadeClampsOutOfRange(t *testing.T) {
	a := DC(1.0, 0.02)
	b := DC(1.0, 0.02)
	out := make(Stereo, len(a))
	Crossfade(out, a, b, -99.0)
	if math.Abs(out.Peak()-a.Peak()) > 1e-6 {
		t.Errorf("x=-99 did not clamp to hard left: %v", out.Peak())
	}
	Crossfade(out, a, b, 99.0)
	if math.Abs(out.Peak()-b.Peak()) > 1e-6 {
		t.Errorf("x=99 did not clamp to hard right: %v", out.Peak())
	}
}

func TestCrossfadeHandlesEmpty(t *testing.T) {
	out := make(Stereo, 8)
	Crossfade(out, nil, nil, 0) // must not panic
	if out.Peak() != 0 {
		t.Errorf("empty crossfade produced %v", out.Peak())
	}
}

func TestLimiterPreventsClipping(t *testing.T) {
	l := NewLimiter(0.8, 0.95)
	hot := make(Stereo, 4410*2)
	for i := range hot {
		hot[i] = 0.99 // constant near-full-scale
	}
	l.Process(hot)
	if hot.Peak() > 0.96 {
		t.Errorf("limiter let peak through: %v", hot.Peak())
	}
}

func TestLimiterLeavesQuietSignalAlone(t *testing.T) {
	l := NewLimiter(0.8, 0.95)
	quiet := Sine(440, 0.1)
	in := quiet.RMS()
	l.Process(quiet)
	if math.Abs(quiet.RMS()-in) > 0.01 {
		t.Errorf("limiter altered quiet signal: %v -> %v", in, quiet.RMS())
	}
}

func TestLimiterCeilingIsNeverExceeded(t *testing.T) {
	l := NewLimiter(0.9, 0.5)
	// a signal far above the ceiling must be brought under it
	hot := Sine(440, 0.3)
	for i := range hot {
		hot[i] *= 3
	}
	l.Process(hot)
	if hot.Peak() > 0.51 {
		t.Errorf("ceiling 0.5 exceeded: %v", hot.Peak())
	}
}

func TestLimiterNoNaNOnSilence(t *testing.T) {
	l := NewLimiter(0.8, 0.95)
	s := make(Stereo, 4410)
	l.Process(s)
	if s.Peak() != 0 {
		t.Errorf("limiter produced %v from silence", s.Peak())
	}
	if math.IsNaN(float64(s[0])) {
		t.Error("limiter produced NaN on silence")
	}
}

func TestLimiterRecoversAfterTransient(t *testing.T) {
	// After a loud burst, the limiter must release so quiet material is not
	// left attenuated.
	l := NewLimiter(0.8, 0.95)
	burst := make(Stereo, 4410)
	for i := range burst {
		burst[i] = 0.99
	}
	l.Process(burst)
	quiet := Sine(440, 0.2)
	before := quiet.RMS()
	l.Process(quiet)
	if quiet.RMS() < before*0.9 {
		t.Errorf("limiter did not release: %v -> %v", before, quiet.RMS())
	}
}

func TestLimiterReset(t *testing.T) {
	l := NewLimiter(0.8, 0.95)
	hot := make(Stereo, 4410)
	for i := range hot {
		hot[i] = 0.99
	}
	l.Process(hot)
	l.Reset()
	s := Sine(440, 0.05)
	before := s.RMS()
	l.Process(s)
	if math.Abs(s.RMS()-before) > 0.02 {
		t.Errorf("after Reset limiter changed a quiet signal: %v -> %v", before, s.RMS())
	}
}

func TestLimiterHandlesEmpty(t *testing.T) {
	l := NewLimiter(0.8, 0.95)
	l.Process(nil) // must not panic
}

func BenchmarkLimiterProcess(b *testing.B) {
	const block = 512
	in := Sine(1000, 2.0)
	l := NewLimiter(0.8, 0.95)
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		l.Process(in[:block*2])
	}
}
