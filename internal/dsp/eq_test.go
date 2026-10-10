package dsp

import (
	"math"
	"testing"
)

func TestEQUnityIsTransparent(t *testing.T) {
	eq := NewEQ()
	in := Sine(1000, 0.1)
	out := make(Stereo, len(in))
	eq.Process(in, out)
	if math.Abs(out.RMS()-in.RMS()) > 0.01 {
		t.Errorf("unity EQ changed RMS: %v -> %v", in.RMS(), out.RMS())
	}
}

func TestEQHighBoostRaisesHighFrequency(t *testing.T) {
	low := Sine(200, 0.2)
	high := Sine(5000, 0.2)
	eq := NewEQ()
	eq.SetBand(2, 4.0) // high band +12dB
	lo := make(Stereo, len(low))
	eq.Process(low, lo)
	hi := make(Stereo, len(high))
	eq.Process(high, hi)
	hiGain := hi.RMS() / low.RMS()
	loGain := lo.RMS() / low.RMS()
	if hiGain <= loGain {
		t.Errorf("high boost did not favour 5k over 200Hz: %v vs %v", hiGain, loGain)
	}
}

func TestEQHighBoostIsExactAtBandCentre(t *testing.T) {
	// At its centre frequency a peaking EQ delivers exactly the requested
	// gain. This is what makes the knob usable: +12dB means +12dB.
	eq := NewEQ()
	eq.SetBand(2, 4.0) // +12dB on the 4kHz band
	in := Sine(4000, 0.2)
	out := make(Stereo, len(in))
	eq.Process(in, out)
	g := out.RMS() / in.RMS()
	if math.Abs(g-4.0) > 0.1 {
		t.Errorf("gain at 4kHz centre = %v, want 4.0 (+12dB)", g)
	}
}

func TestEQOffCentreBoostIsTapered(t *testing.T) {
	// Away from the centre the boost tapers, so neighbouring bands are not
	// lifted as much as the band itself.
	eq := NewEQ()
	eq.SetBand(2, 4.0)
	centre := Sine(4000, 0.2)
	off := Sine(1000, 0.2)
	c := make(Stereo, len(centre))
	eq.Process(centre, c)
	o := make(Stereo, len(off))
	eq.Process(off, o)
	centreGain := c.RMS() / centre.RMS()
	offGain := o.RMS() / off.RMS()
	if offGain >= centreGain {
		t.Errorf("off-centre gain %v should be below centre gain %v", offGain, centreGain)
	}
}

func TestEQLowCutAttenuatesLowFrequency(t *testing.T) {
	low := Sine(100, 0.2)
	eq := NewEQ()
	eq.SetBand(0, 0.01) // low band near-silent (-40dB at 250Hz)
	out := make(Stereo, len(low))
	eq.Process(low, out)
	// 100Hz is well below the 250Hz centre, so it gets close to the full
	// -40dB. The bound allows for the Q=1 bell's skirt.
	if out.RMS() > 0.25*low.RMS() {
		t.Errorf("low cut insufficient: %v vs %v", out.RMS(), low.RMS())
	}
}

func TestEQMidBoostTargetsMid(t *testing.T) {
	mid := Sine(1000, 0.2)
	side := Sine(5000, 0.2)
	eq := NewEQ()
	eq.SetBand(1, 4.0)
	m := make(Stereo, len(mid))
	eq.Process(mid, m)
	s := make(Stereo, len(side))
	eq.Process(side, s)
	if m.RMS()/mid.RMS() <= s.RMS()/side.RMS() {
		t.Error("mid boost did not favour 1k over 5k")
	}
}

func TestEQClampsAbsurdGain(t *testing.T) {
	eq := NewEQ()
	eq.SetBand(0, 1e6)
	eq.SetBand(1, -1e6)
	eq.SetBand(2, 0)
	in := Sine(1000, 0.1)
	out := make(Stereo, len(in))
	eq.Process(in, out)
	if math.IsNaN(out.RMS()) || math.IsInf(out.RMS(), 0) {
		t.Errorf("clamped EQ produced %v", out.RMS())
	}
}

func TestEQRejectsOutOfRangeBand(t *testing.T) {
	eq := NewEQ()
	eq.SetBand(7, 2.0) // must not panic
	eq.SetBand(-1, 2.0)
}

func TestEQAliasedInPlace(t *testing.T) {
	eq := NewEQ()
	eq.SetBand(1, 2.0)
	s := Sine(1000, 0.1)
	eq.Process(s, s) // must not corrupt
	if s.Peak() > 1.5 {
		t.Errorf("aliased EQ produced out-of-range peak %v", s.Peak())
	}
}

func TestEQResetRestoresFlat(t *testing.T) {
	eq := NewEQ()
	eq.SetBand(2, 4.0)
	warm := Sine(5000, 0.05)
	buf := make(Stereo, len(warm))
	eq.Process(warm, buf)
	eq.Reset()
	in := Sine(1000, 0.1)
	out := make(Stereo, len(in))
	eq.Process(in, out)
	if math.Abs(out.RMS()-in.RMS()) > 0.05 {
		t.Errorf("after Reset, EQ is not flat: %v vs %v", out.RMS(), in.RMS())
	}
}

func BenchmarkEQProcess(b *testing.B) {
	const block = 512
	in := Sine(1000, 2.0)
	out := make(Stereo, block*2)
	eq := NewEQ()
	eq.SetBand(EQLow, 0.5)
	eq.SetBand(EQMid, 1.5)
	eq.SetBand(EQHigh, 3.0)
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		eq.Process(in[:block*2], out)
	}
}

func TestEQHandlesEmptyBuffers(t *testing.T) {
	eq := NewEQ()
	eq.Process(nil, nil) // must not panic
	out := make(Stereo, 512)
	eq.Process(nil, out) // no input, output should be silent
	if out.Peak() != 0 {
		t.Errorf("EQ produced %v from empty input", out.Peak())
	}
}
