package dsp

import "math"

// Limiter dynamics. Gain reduction is instant; only the release is smoothed,
// so the release time is the only knob here (spec §4.2).
const limiterReleaseMs = 50.0

// Limiter is a peak limiter for the master bus. It exists so that a hot mix
// is gently restrained rather than hard-clipped: clipping a master destroys
// the transients that make a mix sound loud.
type Limiter struct {
	threshold float64
	ceiling   float64
	// env is the current gain multiplier, in (0, 1].
	env float64
	// release is the one-pole coefficient for recovering toward unity.
	release float64
}

// NewLimiter returns a limiter with the given threshold and ceiling. Both are
// linear peak values; ceiling must be below 1.0 to be meaningful.
func NewLimiter(threshold, ceiling float64) *Limiter {
	if math.IsNaN(threshold) || threshold <= 0 {
		threshold = 1.0
	}
	if math.IsNaN(ceiling) || ceiling <= 0 {
		ceiling = 1.0
	}
	// A ceiling above the threshold would make the limiter a no-op on peaks
	// below it; clamp so the two are consistent.
	if ceiling > threshold {
		ceiling = threshold
	}
	releaseFrames := float64(SampleRate) * limiterReleaseMs / 1000
	return &Limiter{
		threshold: threshold,
		ceiling:   ceiling,
		env:       1.0,
		release:   onePole(releaseFrames),
	}
}

// onePole converts a time constant in frames to a smoothing coefficient.
// A zero or negative constant yields 1.0, meaning "track instantly".
func onePole(frames float64) float64 {
	if frames <= 1 {
		return 1
	}
	return 1 - math.Exp(-1/frames)
}

// Reset returns the gain reduction to unity.
func (l *Limiter) Reset() { l.env = 1.0 }

// Process applies limiting to s in place.
func (l *Limiter) Process(s Stereo) {
	n := s.Frames()
	for i := 0; i < n; i++ {
		lv := float64(s[i*2])
		rv := float64(s[i*2+1])

		// Target gain: the most restrictive of the threshold and the ceiling.
		peak := math.Max(math.Abs(lv), math.Abs(rv))
		target := 1.0
		if peak > 1e-9 {
			if t := l.threshold / peak; t < target {
				target = t
			}
			if c := l.ceiling / peak; c < target {
				target = c
			}
		}

		// Gain reduction applies immediately on the way down and is smoothed
		// only on the way up.
		//
		// This asymmetry is what makes it a limiter rather than a compressor.
		// Smoothing the attack would let a sustained over-threshold signal
		// overshoot the ceiling for tens of milliseconds — which on the
		// master bus is exactly the clipping the limiter exists to prevent.
		// The release is smoothed so that gain reduction does not pump
		// audibly between beats.
		if target < l.env {
			l.env = target
		} else {
			l.env += (target - l.env) * l.release
		}

		gl, gr := lv*l.env, rv*l.env
		// A NaN here would poison the bus for the rest of the session.
		if math.IsNaN(gl) {
			gl = 0
		}
		if math.IsNaN(gr) {
			gr = 0
		}
		s[i*2] = float32(gl)
		s[i*2+1] = float32(gr)
	}
}
