// Package dsp provides the digital signal processing primitives for mix-tui:
// sample buffers, variable-rate resampling, EQ, filtering, and dynamics.
//
// Everything here is pure Go with no cgo and no device dependency, so it is
// fully testable without a sound card or a terminal.
package dsp

import "math"

// SampleRate is the sample rate the engine is configured for. It is not fixed
// at construction: milestone 2 negotiates it from the audio device and passes
// it through config. It exists here as the default and as the rate signal
// generators in tests run at.
const SampleRate = 44100

// Stereo is interleaved stereo audio: [L0 R0 L1 R1 ...]. Interleaved rather
// than planar because that is what both the resampler input and the audio
// device want, and a copy between layouts in the realtime path is not free.
type Stereo []float32

// Frames returns the number of stereo sample frames, i.e. len(s)/2.
func (s Stereo) Frames() int { return len(s) / 2 }

// Dup returns an independent copy. Tests use it to compare a processed buffer
// against its input without aliasing surprises.
func (s Stereo) Dup() Stereo {
	out := make(Stereo, len(s))
	copy(out, s)
	return out
}

// mono returns the mono sum (L+R)/2 of frame i, which is the signal the
// measurement helpers below operate on. Interleaved stereo measures better on
// its sum than on either channel: level detectors and crossing counts should
// not care about pan.
func (s Stereo) mono(i int) float64 { return float64(s[i*2]+s[i*2+1]) / 2 }

// RMS returns the root-mean-square across every sample in both channels.
//
// Level measures deliberately do NOT use the mono sum. Anti-phase material
// (L = -R) cancels to zero in a mono sum, and a level meter that reads zero on
// a hot signal is useless for verifying that resampling preserved gain.
func (s Stereo) RMS() float64 {
	if len(s) == 0 {
		return 0
	}
	var acc float64
	for _, v := range s {
		acc += float64(v) * float64(v)
	}
	return math.Sqrt(acc / float64(len(s)))
}

// Peak returns the largest absolute sample value in either channel.
func (s Stereo) Peak() float64 {
	var p float32
	for _, v := range s {
		a := v
		if a < 0 {
			a = -a
		}
		if a > p {
			p = a
		}
	}
	return float64(p)
}

// ZeroCrossingRate returns sign changes per second in the mono signal.
//
// Note the standard convention: a pure sine at frequency f produces 2f
// crossings per second, not f. Measure a ratio against a reference tone rather
// than against an absolute frequency.
func (s Stereo) ZeroCrossingRate() float64 {
	n := s.Frames()
	if n < 2 {
		return 0
	}
	cross := 0
	prev := s.mono(0)
	for i := 1; i < n; i++ {
		v := s.mono(i)
		if (v >= 0 && prev < 0) || (v < 0 && prev >= 0) {
			cross++
		}
		prev = v
	}
	seconds := float64(n) / float64(SampleRate)
	if seconds <= 0 {
		return 0
	}
	return float64(cross) / seconds
}

// Energy returns the sum of squares across every sample in both channels.
func Energy(s Stereo) float64 {
	var acc float64
	for _, v := range s {
		acc += float64(v) * float64(v)
	}
	return acc
}

// MaxSampleDelta returns the largest frame-to-frame change in the mono signal.
//
// This is the seam detector. A continuous signal at frequency f cannot change
// by more than roughly 2*pi*f/SampleRate per frame, so a jump larger than that
// is a discontinuity — which is exactly what an unprimed seek produces and
// what Prime exists to remove.
func MaxSampleDelta(s Stereo) float64 {
	n := s.Frames()
	if n < 2 {
		return 0
	}
	var m float64
	prev := s.mono(0)
	for i := 1; i < n; i++ {
		v := s.mono(i)
		if d := math.Abs(v - prev); d > m {
			m = d
		}
		prev = v
	}
	return m
}

// ClampNormalized constrains a normalized control value to [-1, 1]. Every
// Action carrying a normalized Value clamps through this, so no control can
// drive a deck outside its designed range regardless of what a MIDI knob or a
// config file sends.
func ClampNormalized(v float64) float64 {
	if math.IsNaN(v) {
		return 0
	}
	return math.Max(-1, math.Min(1, v))
}
