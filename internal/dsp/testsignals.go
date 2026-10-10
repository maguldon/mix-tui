package dsp

import "math"

// Sine returns a stereo sine tone at freq Hz lasting seconds, at half scale.
//
// Exported because test signal generation belongs to the package that owns the
// signal format: engine, analyze, and the TUI all need reference tones, and
// duplicating the generator in each test package guarantees they drift.
func Sine(freq, seconds float64) Stereo {
	n := int(seconds * SampleRate)
	out := make(Stereo, n*2)
	for i := 0; i < n; i++ {
		v := float32(0.5 * math.Sin(2*math.Pi*freq*float64(i)/SampleRate))
		out[i*2], out[i*2+1] = v, v
	}
	return out
}

// DC returns a stereo constant signal lasting seconds.
func DC(amplitude, seconds float64) Stereo {
	n := int(seconds * SampleRate)
	out := make(Stereo, n*2)
	for i := range out {
		out[i] = float32(amplitude)
	}
	return out
}
