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
	// rate designs the biquad coefficients; see EQ.rate.
	rate int
}

// NewFilter returns a filter with its band wide open (cutoff at Nyquist, Q at
// minimum), so a fresh deck passes everything until the knob is turned.
func NewFilter(rate int) *Filter {
	if rate <= 0 {
		rate = SampleRate
	}
	f := &Filter{rate: rate, cutoff: maxCutoffHz, q: 0.707}
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
	nyq := float64(f.rate) / 2
	hi := math.Min(maxCutoffHz, nyq*0.99)
	f.cutoff = math.Max(minCutoffHz, math.Min(hi, cutoffHz))
	f.q = math.Max(minQ, math.Min(maxQ, resonance))
	f.update()
}

func (f *Filter) update() {
	if f.highpass {
		f.b.highpassRBJ(float64(f.rate), f.cutoff, f.q)
	} else {
		f.b.lowpassRBJ(float64(f.rate), f.cutoff, f.q)
	}
}

// Cutoff returns the clamped cutoff in Hz.
func (f *Filter) Cutoff() float64 { return f.cutoff }

// IsHighpass reports whether the filter is sweeping highs (true) or lows.
func (f *Filter) IsHighpass() bool { return f.highpass }

// Resonance returns the current Q.
func (f *Filter) Resonance() float64 { return f.q }

// SetResonance sets Q from a normalized 0..1 knob position, spanning the
// usable range from Butterworth to a resonant peak. Values outside 0..1 and
// NaN are clamped: a MIDI knob can send anything, and a self-oscillating
// filter on a PA is not a defensible failure.
func (f *Filter) SetResonance(normalized float64) {
	if math.IsNaN(normalized) {
		normalized = 0
	}
	if normalized < 0 {
		normalized = 0
	}
	if normalized > 1 {
		normalized = 1
	}
	f.SetParams(f.cutoff, minQ+(maxQ-minQ)*normalized)
}

// SetCutoffNormalized sets the cutoff from a normalized 0..1 knob position,
// sweeping exponentially from the minimum to the maximum. A linear sweep
// would crowd almost the entire usable range into the last few percent of
// knob travel, because pitch is logarithmic.
func (f *Filter) SetCutoffNormalized(normalized float64) {
	if math.IsNaN(normalized) {
		normalized = 1
	}
	if normalized < 0 {
		normalized = 0
	}
	if normalized > 1 {
		normalized = 1
	}
	f.SetParams(minCutoffHz*math.Pow(maxCutoffHz/minCutoffHz, normalized), f.q)
}

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

// Prime settles the filter's state from the samples immediately before a
// discontinuity, so resuming playback there is as smooth as uninterrupted
// playback. See biquad.prime.
func (f *Filter) Prime(history Stereo) { f.b.prime(history) }

// NormalizedCutoff converts a cutoff in Hz to the knob position that produces
// it, the inverse of the logarithmic sweep SetCutoffNormalized applies.
//
// Needed because the knob is logarithmic and SetFilter's public API is in Hz.
// Feeding Hz into the normalized setter sounds almost right and is not: 1000Hz
// arrives as knob position 0.5, which is a filter several hundred Hz away, and
// the error is largest in the middle of the range where the knob spends most of
// its travel.
func NormalizedCutoff(hz float64) float64 {
	if math.IsNaN(hz) || hz <= minCutoffHz {
		return 0
	}
	if hz >= maxCutoffHz {
		return 1
	}
	return math.Log(hz/minCutoffHz) / math.Log(maxCutoffHz/minCutoffHz)
}
