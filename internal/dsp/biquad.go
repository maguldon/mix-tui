package dsp

import "math"

// biquad is a second-order IIR section in transposed direct form II.
//
// Transposed DF-II is used rather than direct form I because its state is two
// values per channel regardless of order, which keeps the per-channel state
// small enough to stay in cache across a 512-frame block.
type biquad struct {
	b0, b1, b2 float64
	a1, a2     float64
	// Per-channel delay line: z1 and z2 for L then R.
	z1, z2 [2]float64
}

// reset clears filter state. Called after a seek: the history a filter holds
// describes samples that are no longer adjacent to the output, and resuming on
// top of it produces an audible click. Resetting is the cheap equivalent of
// priming a resampler — the transient is filtered away rather than clicked.
func (b *biquad) reset() {
	b.z1 = [2]float64{}
	b.z2 = [2]float64{}
}

// process runs one frame, writing the two channels.
//
// Transposed direct form II. Both feedback terms are required:
//
//	y  = b0*x + z1
//	z1 = b1*x + z2 - a1*y
//	z2 = b2*x      - a2*y
//
// Omitting the -a2*y term from z2 leaves the second pole unattenuated, and
// the state grows without bound — the impulse response diverges to +Inf
// within a second of audio, which is an unrecoverable scream on the master.
func (b *biquad) process(l, r float64) (float64, float64) {
	ol := b.b0*l + b.z1[0]
	b.z1[0] = b.b1*l + b.z2[0] - b.a1*ol
	b.z2[0] = b.b2*l - b.a2*ol

	or := b.b0*r + b.z1[1]
	b.z1[1] = b.b1*r + b.z2[1] - b.a1*or
	b.z2[1] = b.b2*r - b.a2*or

	return ol, or
}

// peakingRBJ sets coefficients for an RBJ peaking (bell) EQ section.
// amp is a linear gain multiplier: 1.0 leaves the band untouched.
func (b *biquad) peakingRBJ(fs, f0, q, gain float64) {
	if gain <= 1e-6 {
		gain = 1e-6
	}
	// gain is a linear multiplier (4.0 == +12dB); RBJ wants decibels.
	db := 20 * math.Log10(gain)
	A := math.Pow(10, db/40) // 10^(db/40) == gain^(1/4)
	w0 := 2 * math.Pi * f0 / fs
	alpha := math.Sin(w0) / (2 * q)
	cw := math.Cos(w0)

	b0 := 1 + alpha*A
	b1 := -2 * cw
	b2 := 1 - alpha*A
	a0 := 1 + alpha/A
	a1 := -2 * cw
	a2 := 1 - alpha/A

	b.b0 = b0 / a0
	b.b1 = b1 / a0
	b.b2 = b2 / a0
	b.a1 = a1 / a0
	b.a2 = a2 / a0
}

// stable reports whether the poles lie strictly inside the unit circle. Used by
// tests: an unstable filter does not merely sound wrong, it grows without
// bound and poisons everything downstream of it.
func (b *biquad) stable() bool {
	if math.IsNaN(b.a1) || math.IsNaN(b.a2) || math.IsInf(b.a1, 0) || math.IsInf(b.a2, 0) {
		return false
	}
	disc := b.a1*b.a1 - 4*b.a2
	if disc >= 0 {
		r1 := (-b.a1 + math.Sqrt(disc)) / 2
		r2 := (-b.a1 - math.Sqrt(disc)) / 2
		return math.Abs(r1) < 1 && math.Abs(r2) < 1
	}
	// Complex conjugate pair: radius is sqrt(a2).
	return math.Abs(b.a2) < 1
}

// lowpassRBJ sets coefficients for a 2-pole lowpass.
func (b *biquad) lowpassRBJ(fs, f0, q float64) {
	w0 := 2 * math.Pi * f0 / fs
	cw := math.Cos(w0)
	alpha := math.Sin(w0) / (2 * q)

	b0 := (1 - cw) / 2
	b1 := 1 - cw
	b2 := b0
	a0 := 1 + alpha
	a1 := -2 * cw
	a2 := 1 - alpha

	b.b0 = b0 / a0
	b.b1 = b1 / a0
	b.b2 = b2 / a0
	b.a1 = a1 / a0
	b.a2 = a2 / a0
}

// highpassRBJ sets coefficients for a 2-pole highpass.
func (b *biquad) highpassRBJ(fs, f0, q float64) {
	w0 := 2 * math.Pi * f0 / fs
	cw := math.Cos(w0)
	alpha := math.Sin(w0) / (2 * q)

	b0 := (1 + cw) / 2
	b1 := -(1 + cw)
	b2 := b0
	a0 := 1 + alpha
	a1 := -2 * cw
	a2 := 1 - alpha

	b.b0 = b0 / a0
	b.b1 = b1 / a0
	b.b2 = b2 / a0
	b.a1 = a1 / a0
	b.a2 = a2 / a0
}
