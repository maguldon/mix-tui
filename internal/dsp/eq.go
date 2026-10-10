package dsp

import "math"

// EQ band indices.
const (
	EQLow = iota
	EQMid
	EQHigh
)

// Band centre frequencies. Low sits where a DJ's head EQ acts, high where the
// air EQ acts; the mid band is centred where vocal intelligibility lives.
const (
	eqLowHz  = 250.0
	eqMidHz  = 1000.0
	eqHighHz = 4000.0
	eqQ      = 1.0
)

// EQ is a three-band peaking EQ: low, mid, high, cascaded.
type EQ struct {
	bands [3]biquad
	gains [3]float64
}

// NewEQ returns a flat EQ.
func NewEQ() *EQ {
	e := &EQ{}
	for i := range e.gains {
		e.gains[i] = 1.0
	}
	e.update()
	return e
}

// SetBand sets one band's linear gain. 1.0 is unity; 4.0 is +12dB. Gains are
// clamped to a sane range and out-of-range bands are ignored rather than
// panicking, because a MIDI knob or a config file can send anything.
func (e *EQ) SetBand(band int, gain float64) {
	if band < 0 || band >= len(e.bands) {
		return
	}
	if math.IsNaN(gain) {
		gain = 1.0
	}
	gain = math.Max(0.01, math.Min(10.0, gain))
	e.gains[band] = gain
	e.update()
}

// Gain returns a band's current linear gain.
func (e *EQ) Gain(band int) float64 {
	if band < 0 || band >= len(e.gains) {
		return 1.0
	}
	return e.gains[band]
}

func (e *EQ) update() {
	e.bands[EQLow].peakingRBJ(SampleRate, eqLowHz, eqQ, e.gains[EQLow])
	e.bands[EQMid].peakingRBJ(SampleRate, eqMidHz, eqQ, e.gains[EQMid])
	e.bands[EQHigh].peakingRBJ(SampleRate, eqHighHz, eqQ, e.gains[EQHigh])
}

// Reset clears filter state, discarding the history of a previous position.
func (e *EQ) Reset() {
	for i := range e.bands {
		e.bands[i].reset()
	}
}

// Process runs the EQ over in, writing to out. in and out may alias.
func (e *EQ) Process(in, out Stereo) {
	n := in.Frames()
	if n > out.Frames() {
		n = out.Frames()
	}
	for i := 0; i < n; i++ {
		l, r := float64(in[i*2]), float64(in[i*2+1])
		for b := range e.bands {
			l, r = e.bands[b].process(l, r)
		}
		out[i*2] = float32(l)
		out[i*2+1] = float32(r)
	}
	for i := n * 2; i < len(out); i++ {
		out[i] = 0
	}
}

// Prime settles the EQ's state from the samples immediately before a
// discontinuity. See biquad.prime.
func (e *EQ) Prime(history Stereo) {
	for i := range e.bands {
		e.bands[i].prime(history)
	}
}
