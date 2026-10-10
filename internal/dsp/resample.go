package dsp

import "math"

const (
	// resamplerTaps is the filter length. 32 with a Blackman window puts the
	// stopband well below audibility while keeping the convolution within the
	// realtime budget (spec §4.3).
	resamplerTaps = 32
	// resamplerHalf is the filter's reach in each direction.
	resamplerHalf = resamplerTaps / 2
	// resamplerPhases is the polyphase table resolution. 512 phases over one
	// input sample gives -96dB of interpolation error, far below audibility.
	resamplerPhases = 512
)

// Resampler converts between sample rates using a polyphase windowed-sinc
// filter, and exposes its filter state so a seek can prime it.
//
// The state exposure is the entire reason this type exists rather than a plain
// function. After a jump the filter carries history from samples that are no
// longer adjacent to the output; resuming on top of that produces an audible
// click. Prime rebuilds the state from the destination's own history, which is
// what lets hotcues, nudges, sync corrections, and loop wraps all be seamless
// through one primitive (spec §4.4.1).
type Resampler struct {
	ratio   float64 // output frames consumed per input frame
	inRate  int
	outRate int

	// buf holds interleaved stereo; valid data is buf[:len].
	buf []float32
	// len is the number of valid samples in buf.
	len int
	// rp is the fractional read position in frames from the start of buf.
	rp float64

	// table is the polyphase weight table, indexed [phase][tap]. Precomputing
	// it keeps math.Sin out of the inner loop, which is what makes the
	// realtime budget achievable without cgo.
	table [resamplerPhases][resamplerTaps]float32
}

// NewResampler returns a resampler converting between inRate and outRate.
func NewResampler(inRate, outRate int) *Resampler {
	if inRate <= 0 {
		inRate = SampleRate
	}
	if outRate <= 0 {
		outRate = SampleRate
	}
	r := &Resampler{inRate: inRate, outRate: outRate}
	r.Reset()
	r.SetRatio(float64(outRate) / float64(inRate))
	return r
}

// SetRatio sets output frames produced per input frame consumed. 1.0 plays at
// the source rate; above 1.0 consumes input faster, raising pitch and tempo
// together the way a turntable pitch fader does.
func (r *Resampler) SetRatio(ratio float64) {
	if ratio <= 0 || math.IsNaN(ratio) || math.IsInf(ratio, 0) {
		ratio = 1.0
	}
	r.ratio = ratio

	// Decimation must narrow the filter or high frequencies alias.
	fc := 0.5
	if ratio > 1 {
		fc = 0.5 / ratio
	}

	for p := 0; p < resamplerPhases; p++ {
		frac := float64(p) / float64(resamplerPhases)
		for k := 0; k < resamplerTaps; k++ {
			off := float64(k-resamplerHalf+1) - frac
			t := float64(k) / float64(resamplerTaps-1)
			win := 0.42 - 0.5*math.Cos(2*math.Pi*t) + 0.08*math.Cos(4*math.Pi*t)
			r.table[p][k] = float32(win * fc * sincScaled(off*fc))
		}
	}
}

// sincScaled evaluates sin(pi*x)/(pi*x), with the limit at zero.
func sincScaled(x float64) float64 {
	if x == 0 {
		return 1
	}
	px := math.Pi * x
	return math.Sin(px) / px
}

// Reset returns the resampler to a just-constructed state: no history, read
// position at zero.
func (r *Resampler) Reset() {
	if r.buf == nil {
		// Enough for the retained history plus several blocks, so Process does
		// not allocate once warm.
		r.buf = make([]float32, (resamplerHalf+8192)*2)
	}
	r.len = 0
	r.rp = 0
}

// HistorySamples is how many frames of context Prime must load for the filter
// to start correctly primed.
func (r *Resampler) HistorySamples() int { return resamplerHalf }

// Prime positions the resampler to read at pos in stream, with filter state
// loaded from the frames immediately before it, WITHOUT emitting output.
//
// After Prime, Process produces exactly the signal that uninterrupted playback
// from pos would have produced. Positions outside the stream clamp rather than
// reading out of bounds.
func (r *Resampler) Prime(stream Stereo, pos int, historySamples int) {
	if historySamples < 0 {
		historySamples = 0
	}
	total := stream.Frames()
	if pos < 0 {
		pos = 0
	}
	if pos > total {
		pos = total
	}
	start := pos - historySamples
	if start < 0 {
		start = 0
	}

	r.Reset()
	have := pos - start
	if have > 0 {
		copy(r.buf[:have*2], stream[start*2:pos*2])
	}
	// Any shortfall (a seek near the beginning of the track) stays zero, which
	// the convolution treats as a zero-padded history: correct and silent.
	r.len = have * 2
	r.rp = float64(have)
}

// Process converts src into dst, advancing filter state, and returns the
// number of stereo frames it actually produced.
//
// It may return fewer than dst holds. Process stops at the last frame it has
// full filter lookahead for, because emitting past that point would bake in a
// guess it cannot revise once more input arrives. Callers must use the return
// value rather than assuming dst is full: treating the unwritten remainder as
// audio inserts silence at the stall, and treating it as valid output produces
// a discontinuity indistinguishable from a real seam.
//
// It never allocates once warm and never blocks.
func (r *Resampler) Process(src, dst Stereo) int {
	if len(dst) == 0 {
		return 0
	}
	outFrames := dst.Frames()

	// Unity is an exact passthrough: faster, and perfectly transparent, which
	// is what makes the unity-rate test exact rather than approximate.
	if r.ratio == 1.0 {
		n := outFrames * 2
		if n > len(src) {
			n = len(src)
		}
		copy(dst, src[:n])
		for i := n; i < len(dst); i++ {
			dst[i] = 0
		}
		r.push(src)
		r.rp += float64(outFrames)
		r.compact()
		return n / 2
	}

	r.push(src)

	// step is input frames consumed per output frame produced. Above 1.0 the
	// source plays faster: pitch and tempo rise together, exactly as a
	// turntable pitch fader does (spec §2.1).
	step := r.ratio
	written := 0
	frames := r.len / 2

	for written < outFrames {
		base := int(r.rp)
		if base+resamplerHalf >= frames {
			break // not enough lookahead buffered
		}
		frac := r.rp - float64(base)
		p := int(frac * resamplerPhases)
		if p >= resamplerPhases {
			p = resamplerPhases - 1
		}
		w := &r.table[p]

		var l, rr float64
		first := base - resamplerHalf + 1
		for k := 0; k < resamplerTaps; k++ {
			idx := (first + k) * 2
			if idx < 0 {
				continue // history before the buffer is zero
			}
			if idx+1 >= r.len {
				break
			}
			l += float64(w[k]) * float64(r.buf[idx])
			rr += float64(w[k]) * float64(r.buf[idx+1])
		}
		dst[written*2] = float32(l)
		dst[written*2+1] = float32(rr)

		written++
		r.rp += step
	}

	for i := written * 2; i < len(dst); i++ {
		dst[i] = 0
	}
	r.compact()
	return written
}

// push appends src to the buffered input, growing only if the caller hands us
// more than the warmed buffer can hold.
func (r *Resampler) push(src Stereo) {
	n := len(src)
	if n == 0 {
		return
	}
	if r.len+n > len(r.buf) {
		r.grow(n)
	}
	copy(r.buf[r.len:r.len+n], src)
	r.len += n
}

// compact drops input that has fallen out of the filter's history window,
// sliding the buffer down so memory stays bounded without reallocating.
//
// Both ends are trimmed. Trimming only the front leaves everything the caller
// pushed past the read position, which in a streaming pattern accumulates
// without bound and forces the buffer to keep doubling.
func (r *Resampler) compact() {
	// The read position needs resamplerHalf frames of history before it and
	// resamplerHalf of lookahead after it.
	lo := (int(r.rp) - resamplerHalf + 1) * 2
	if lo < 0 {
		lo = 0
	}
	hi := (int(r.rp) + resamplerHalf + 1) * 2
	if hi > r.len {
		hi = r.len
	}
	if hi <= lo {
		return
	}
	// Slide the retained window to the front.
	copy(r.buf[:hi-lo], r.buf[lo:hi])
	r.rp -= float64(lo / 2)
	r.len = hi - lo
}

func (r *Resampler) grow(extra int) {
	need := r.len + extra
	if need <= len(r.buf) {
		return
	}
	size := len(r.buf)
	if size == 0 {
		size = (resamplerHalf + 8192) * 2
	}
	for size < need {
		size *= 2
	}
	nb := make([]float32, size)
	copy(nb, r.buf[:r.len])
	r.buf = nb
}
