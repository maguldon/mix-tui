package dsp

import "math"

// Gain scales s in place by a linear multiplier.
func Gain(s Stereo, g float64) {
	if math.IsNaN(g) {
		return
	}
	for i := range s {
		s[i] *= float32(g)
	}
}

// Crossfade mixes two sources into out with a constant-power (equal-gain)
// curve.
//
// x runs -1 (hard left, all A) through 0 (centre) to +1 (hard right, all B).
// The curve is cos/sin rather than linear because a linear crossfade dips 3dB
// at the centre for uncorrelated material, which is audible as a hole in the
// mix. Constant power holds the level flat across the throw.
func Crossfade(out Stereo, a, b Stereo, x float64) {
	if math.IsNaN(x) {
		x = 0
	}
	x = math.Max(-1, math.Min(1, x))
	t := (x + 1) / 2
	ga := math.Cos(t * math.Pi / 2)
	gb := math.Sin(t * math.Pi / 2)

	n := out.Frames()
	if a.Frames() < n {
		n = a.Frames()
	}
	if b.Frames() < n {
		n = b.Frames()
	}
	for i := 0; i < n; i++ {
		l := float64(a[i*2])*ga + float64(b[i*2])*gb
		r := float64(a[i*2+1])*ga + float64(b[i*2+1])*gb
		out[i*2] = float32(l)
		out[i*2+1] = float32(r)
	}
	for i := n * 2; i < len(out); i++ {
		out[i] = 0
	}
}
