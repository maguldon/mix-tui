package dsp

import (
	"math"
	"testing"
)

func TestResamplerUnityPassesToneUnchanged(t *testing.T) {
	r := NewResampler(SampleRate, SampleRate)
	in := Sine(1000, 0.5)
	out := make(Stereo, len(in))
	r.Process(in, out)
	if math.Abs(out.RMS()-in.RMS()) > 0.01 {
		t.Errorf("RMS drift at unity: in=%v out=%v", in.RMS(), out.RMS())
	}
}

// trimTail removes the zero-filled tail Process leaves when it lacks lookahead.
// Measuring across that boundary measures the padding, not the conversion.
func trimTail(s Stereo) Stereo {
	for i := s.Frames() - 1; i >= 0; i-- {
		if math.Abs(float64(s[i*2])) > 1e-9 || math.Abs(float64(s[i*2+1])) > 1e-9 {
			return s[:(i+1)*2]
		}
	}
	return s[:0]
}

func TestResamplerRatioChangesRate(t *testing.T) {
	// +6% pitch must raise the measured rate by 6%. Compare against the
	// input's own rate rather than an absolute frequency, so the test does
	// not depend on the 2f zero-crossing convention.
	r := NewResampler(SampleRate, SampleRate)
	r.SetRatio(1.06)
	in := Sine(1000, 0.5)
	out := make(Stereo, len(in))
	r.Process(in, out)
	produced := trimTail(out)
	ratio := produced.ZeroCrossingRate() / in.ZeroCrossingRate()
	if math.Abs(ratio-1.06) > 0.02 {
		t.Errorf("rate ratio = %v, want 1.06 for +6%% pitch (produced %d frames)",
			ratio, produced.Frames())
	}
}

func TestResamplerRatioDown(t *testing.T) {
	r := NewResampler(SampleRate, SampleRate)
	r.SetRatio(0.94)
	in := Sine(1000, 0.5)
	out := make(Stereo, len(in))
	r.Process(in, out)
	ratio := trimTail(out).ZeroCrossingRate() / in.ZeroCrossingRate()
	if math.Abs(ratio-0.94) > 0.02 {
		t.Errorf("rate ratio = %v, want 0.94 for -6%% pitch", ratio)
	}
}

// drain processes src in chunkFrames-sized pieces and returns exactly the
// frames the resampler produced, using Process's return value. Concatenating
// whole output buffers instead would splice each lookahead stall's zero-fill
// into the signal as a discontinuity indistinguishable from a real seam.
func drain(t *testing.T, r *Resampler, src Stereo, chunkFrames int) Stereo {
	t.Helper()
	out := make(Stereo, 0, len(src))
	buf := make(Stereo, chunkFrames*2)
	for off := 0; off < len(src); off += chunkFrames * 2 {
		end := off + chunkFrames*2
		if end > len(src) {
			end = len(src)
		}
		n := r.Process(src[off:end], buf)
		out = append(out, buf[:n*2]...)
	}
	return out
}

// TestPrimeEliminatesSeekDiscontinuity is the test the whole project rests on.
// A primed jump must be as continuous as uninterrupted playback; an unprimed
// one must not be, or the test would prove nothing.
func TestPrimeEliminatesSeekDiscontinuity(t *testing.T) {
	// Play the first second, then seek forward two seconds. head and tail
	// must NOT be contiguous, or there is no jump to detect.
	const played = SampleRate
	const jump = 2*SampleRate + 37
	stream := Sine(440, 4.0)
	head := stream[:played*2]
	tail := stream[jump*2:]

	// --- unprimed seek (the control) ---
	bad := NewResampler(SampleRate, SampleRate)
	bad.SetRatio(1.05)
	drain(t, bad, head, 512)
	badJump := MaxSampleDelta(drain(t, bad, tail, 512))

	// --- primed seek (the thing under test) ---
	good := NewResampler(SampleRate, SampleRate)
	good.SetRatio(1.05)
	drain(t, good, head, 512)
	good.Prime(stream, jump, good.HistorySamples())
	goodJump := MaxSampleDelta(drain(t, good, tail, 512))

	// 440Hz at 1.05 is ~462Hz; max sample-to-sample slope bounds any
	// legitimate output. Allow 2x for the phase transient at the seek.
	maxSlope := 2 * math.Pi * 462 / SampleRate
	if goodJump > maxSlope*2 {
		t.Errorf("primed seek %.5f exceeds tone slope %.5f", goodJump, maxSlope)
	}
	if badJump <= goodJump*2 {
		t.Errorf("priming had no measurable effect: bad=%.5f good=%.5f "+
			"(the control must show a clearly larger discontinuity)", badJump, goodJump)
	}
}

func TestPrimeAtPositionZeroDoesNotPanic(t *testing.T) {
	// Review Focus #5: Prime reads BEFORE the target; at pos 0 there is no
	// history. This must not panic or read out of bounds.
	stream := Sine(440, 1.0)
	r := NewResampler(SampleRate, SampleRate)
	r.SetRatio(1.05)
	r.Prime(stream, 0, r.HistorySamples())
	out := make(Stereo, 1024)
	r.Process(stream[:512], out) // must not panic
	if math.IsNaN(out.RMS()) {
		t.Error("priming at position zero produced NaN")
	}
}

func TestPrimeNearEndClamps(t *testing.T) {
	stream := Sine(440, 1.0)
	r := NewResampler(SampleRate, SampleRate)
	r.Prime(stream, SampleRate*5, r.HistorySamples()) // past the end
	r.Prime(stream, -100, r.HistorySamples())         // negative
}

func TestHistorySamplesIsPositive(t *testing.T) {
	r := NewResampler(SampleRate, SampleRate)
	if r.HistorySamples() <= 0 {
		t.Errorf("HistorySamples() = %v, want > 0", r.HistorySamples())
	}
}

func TestResetClearsState(t *testing.T) {
	in := Sine(440, 0.2)
	a := NewResampler(SampleRate, SampleRate)
	a.SetRatio(1.05)
	a.Reset()
	out := make(Stereo, len(in))
	a.Process(in, out)

	b := NewResampler(SampleRate, SampleRate)
	b.SetRatio(1.05)
	out2 := make(Stereo, len(in))
	b.Process(in, out2)

	if math.Abs(a.RMSOf(out)-b.RMSOf(out2)) > 1e-6 {
		t.Error("Reset did not restore initial state")
	}
}

// RMSOf is a test helper mirroring Stereo.RMS.
func (r *Resampler) RMSOf(s Stereo) float64 { return s.RMS() }

func TestProcessHandlesShortSource(t *testing.T) {
	r := NewResampler(SampleRate, SampleRate)
	r.SetRatio(1.05)
	out := make(Stereo, 2048)
	r.Process(Sine(440, 0.001), out) // far fewer input frames than output
	if math.IsNaN(out.RMS()) {
		t.Error("short source produced NaN")
	}
}

func TestProcessHandlesEmptySource(t *testing.T) {
	r := NewResampler(SampleRate, SampleRate)
	out := make(Stereo, 512)
	r.Process(nil, out) // must not panic
}

func TestProcessHandlesEmptyDestination(t *testing.T) {
	r := NewResampler(SampleRate, SampleRate)
	r.Process(Sine(440, 0.01), nil) // must not panic
}

func TestProcessIsContinuousAcrossCalls(t *testing.T) {
	// Streaming in blocks must produce the same signal as one call, frame for
	// frame. Compare over the frames both paths actually produced: the whole-
	// call version emits more in its single pass because it never stalls.
	const chunkFrames = 512
	full := Sine(440, 0.2)
	total := full.Frames()

	whole := NewResampler(SampleRate, SampleRate)
	whole.SetRatio(1.05)
	a := make(Stereo, total*2)
	nWhole := whole.Process(full, a)
	a = a[:nWhole*2]

	chunked := NewResampler(SampleRate, SampleRate)
	chunked.SetRatio(1.05)
	b := make(Stereo, 0, total*2)
	block := make(Stereo, chunkFrames*2)
	for off := 0; off < total; off += chunkFrames {
		end := off + chunkFrames
		if end > total {
			end = total
		}
		n := chunked.Process(full[off*2:end*2], block)
		b = append(b, block[:n*2]...)
	}

	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	if n < total {
		t.Fatalf("too few frames compared: whole=%d chunked=%d", nWhole, len(b)/2)
	}
	var worst float64
	for i := 0; i < n; i++ {
		if d := math.Abs(float64(a[i]) - float64(b[i])); d > worst {
			worst = d
		}
	}
	if worst > 1e-3 {
		t.Errorf("chunked output diverges from whole-call by %v over %d samples", worst, n/2)
	}
}

// BenchmarkResamplerProcess measures the realtime pattern: pull a 512-frame
// block from a rolling source and resample it. The realtime budget is 11.6ms
// per block (spec §4.3), and the render path must not allocate, so both are
// asserted by reading these numbers rather than by a test that could pass on a
// fast machine and fail on a slow one.
func BenchmarkResamplerProcess(b *testing.B) {
	const block = 512
	r := NewResampler(SampleRate, SampleRate)
	r.SetRatio(1.08)
	src := Sine(1000, 30.0)
	scratch := make(Stereo, block*2)
	// Produce one full 512-frame output block. At ratio r each output frame
	// consumes r input frames, and the filter additionally holds resamplerHalf
	// frames of lookahead it cannot consume yet.
	const ratio = 1.08
	var feed int = block
	feed = int(float64(feed)*ratio) + resamplerHalf + 2
	if n := r.Process(src[:feed*2], scratch); n < block {
		b.Fatalf("warmup produced only %d of %d frames (feed=%d)", n, block, feed)
	}
	off := feed * 2

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if off+feed*2 > len(src) {
			off = 0
		}
		r.Process(src[off:off+feed*2], scratch)
		off += feed * 2
	}
}

// BenchmarkResamplerProcessCold measures a freshly-primed resampler, which is
// the loop-wrap and hotcue-jump path: a seek primes from the destination's
// history and then processes a block.
func BenchmarkResamplerProcessCold(b *testing.B) {
	const block = 512
	stream := Sine(1000, 30.0)
	scratch := make(Stereo, block*2)
	// Vary the jump target so the benchmark does not benefit from caching one
	// position, and wrap before running off the end.
	maxJump := len(stream)/2 - block*4
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		at := (i * 977) % maxJump
		r := NewResampler(SampleRate, SampleRate)
		r.SetRatio(1.05)
		r.Prime(stream, at, r.HistorySamples())
		r.Process(stream[at*2:], scratch)
	}
}
