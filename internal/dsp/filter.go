package dsp

import "math"

// Filter parameter bounds. Clamping rather than rejecting keeps a wild MIDI
// knob from producing an unstable filter, which would be both loud and
// indefensible (spec §8).
const (
	minCutoffHz = 20.0
	maxCutoffHz = 20000.0
	// minQ is 1/sqrt(2): below that a lowpass or highpass biquad becomes
	// unstable and the state diverges to Inf within a few thousand samples.
	// That is not a theoretical concern — it is audible as a scream, and it
	// poisons the master bus for the rest of the session.
	minQ = 0.7071067811865476
	maxQ = 20.0
)

// Filter is the DJ filter knob: a 2-pole lowpass or highpass with resonance.
type Filter struct {
	b        biquad
	highpass bool
	cutoff   float64
	q        float64
}

// NewFilter returns a filter with its band wide open (cutoff at Nyquist, Q at
// minimum), so a fresh deck passes everything until the knob is turned.
func NewFilter() *Filter {
	f := &Filter{cutoff: maxCutoffHz, q: 0.707}
	f.update()
	return f
}

// SetKind selects lowpass (false) or highpass (true).
func (f *Filter) SetKind(highpass bool) {
	if f.highpass == highpass {
		return
	}
	f.highpass = highpass
	f.update()
}

// SetParams sets cutoff in Hz and resonance (Q). Both are clamped.
func (f *Filter) SetParams(cutoffHz, resonance float64) {
	if math.IsNaN(cutoffHz) {
		cutoffHz = maxCutoffHz
	}
	if math.IsNaN(resonance) {
		resonance = 0.707
	}
	// Keep the cutoff below Nyquist with headroom; a filter tuned at or above
	// Nyquist is degenerate.
	nyq := float64(SampleRate) / 2
	hi := math.Min(maxCutoffHz, nyq*0.99)
	f.cutoff = math.Max(minCutoffHz, math.Min(hi, cutoffHz))
	f.q = math.Max(minQ, math.Min(maxQ, resonance))
	f.update()
}

func (f *Filter) update() {
	if f.highpass {
		f.b.highpassRBJ(SampleRate, f.cutoff, f.q)
	} else {
		f.b.lowpassRBJ(SampleRate, f.cutoff, f.q)
	}
}

// Cutoff returns the clamped cutoff in Hz.
func (f *Filter) Cutoff() float64 { return f.cutoff }

// Reset clears filter state.
func (f *Filter) Reset() { f.b.reset() }

// Process runs the filter over in, writing to out. in and out may alias.
func (f *Filter) Process(in, out Stereo) {
	n := in.Frames()
	if n > out.Frames() {
		n = out.Frames()
	}
	for i := 0; i < n; i++ {
		l, r := f.b.process(float64(in[i*2]), float64(in[i*2+1]))
		out[i*2] = float32(l)
		out[i*2+1] = float32(r)
	}
	for i := n * 2; i < len(out); i++ {
		out[i] = 0
	}
}
