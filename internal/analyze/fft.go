// Package analyze derives beatgrids and musical keys from decoded audio.
//
// Both analyses share a single short-time Fourier transform (spec §4.1).
// Computing the spectrogram once is what makes key detection nearly free: it
// adds no second pass over the audio, only a different reading of the same
// bins. The window and hop are chosen to serve both — fine enough for chroma
// resolution, coarse enough for onset timing.
package analyze

import (
	"math"

	"github.com/maguldon/mix-tui/internal/dsp"
)

// STFT parameters. 2048 samples at 44.1kHz is 46ms per window with 21.5Hz bin
// spacing: enough to separate chroma pitch classes, short enough to time an
// onset within a few milliseconds.
const (
	windowLen = 2048
	hopSize   = 512
)

// Spectrogram is the shared STFT result. ONE computation feeds both beatgrid
// and key detection (spec §4.1).
type Spectrogram struct {
	// Frames[i] holds bins 0..windowLen/2 for frame i, in DC-to-Nyquist order.
	Frames     [][]complex64
	WindowLen  int
	HopSize    int
	SampleRate int
}

// binHz is the frequency of bin b, allowing a fractional bin index for
// sub-bin peak refinement.
func (s *Spectrogram) binHz(b float64) float64 {
	return b * float64(s.SampleRate) / float64(s.WindowLen)
}

// ComputeSpectrogram performs a Hann-windowed STFT over the mono sum of pcm.
//
// It is offline code, called once per track at load. It allocates freely; the
// realtime path never touches it (spec §4.3).
func ComputeSpectrogram(pcm dsp.Stereo, wlen, hop int) *Spectrogram {
	// Degenerate parameters must not divide by zero or index out of range.
	if wlen < 2 {
		wlen = 2
	}
	if hop < 1 {
		hop = 1
	}
	if wlen > 1<<20 {
		wlen = 1 << 20
	}

	mono := toMono(pcm)
	bins := wlen/2 + 1

	// A Hann window, precomputed once.
	win := make([]float64, wlen)
	for i := range win {
		win[i] = 0.5 * (1 - math.Cos(2*math.Pi*float64(i)/float64(wlen-1)))
	}

	n := len(mono)
	if n < wlen {
		// Not enough audio for a single window: return an empty result rather
		// than a panic. Callers treat that as "cannot analyse".
		return &Spectrogram{
			Frames: nil, WindowLen: wlen, HopSize: hop, SampleRate: dsp.SampleRate,
		}
	}

	frameCount := 1 + (n-wlen)/hop
	frames := make([][]complex64, frameCount)
	buf := make([]complex128, wlen)

	for f := 0; f < frameCount; f++ {
		off := f * hop
		for i := 0; i < wlen; i++ {
			buf[i] = complex(mono[off+i]*win[i], 0)
		}
		fft(buf)
		row := make([]complex64, bins)
		for b := 0; b < bins; b++ {
			row[b] = complex64(buf[b])
		}
		frames[f] = row
	}

	return &Spectrogram{
		Frames: frames, WindowLen: wlen, HopSize: hop, SampleRate: dsp.SampleRate,
	}
}

// toMono returns the mono sum of interleaved stereo.
func toMono(pcm dsp.Stereo) []float64 {
	n := len(pcm) / 2
	out := make([]float64, n)
	for i := 0; i < n; i++ {
		out[i] = (float64(pcm[i*2]) + float64(pcm[i*2+1])) / 2
	}
	return out
}

// fft computes the in-place radix-2 Cooley-Tukey transform.
//
// A hand-written radix-2 FFT is used rather than a dependency: this is the one
// numerical kernel the project would otherwise pull a large module for, it
// runs only on the offline analysis path, and thirty lines here are easier to
// verify than a transitive dependency tree.
func fft(a []complex128) {
	n := len(a)
	if n <= 1 {
		return
	}
	// Bit-reversal permutation.
	bits := 0
	for 1<<bits < n {
		bits++
	}
	for i := 0; i < n; i++ {
		j := reverseBits(i, bits)
		if j > i {
			a[i], a[j] = a[j], a[i]
		}
	}
	// Cooley-Tukey butterflies.
	for length := 2; length <= n; length <<= 1 {
		angle := -2 * math.Pi / float64(length)
		wLen := complex(math.Cos(angle), math.Sin(angle))
		for i := 0; i < n; i += length {
			w := complex(1, 0)
			half := length / 2
			for j := 0; j < half; j++ {
				u := a[i+j]
				v := a[i+j+half] * w
				a[i+j] = u + v
				a[i+j+half] = u - v
				w *= wLen
			}
		}
	}
}

func reverseBits(x, bits int) int {
	var r int
	for i := 0; i < bits; i++ {
		r = (r << 1) | ((x >> i) & 1)
	}
	return r
}

// magnitudeAt returns the magnitude spectrum of one frame, DC to Nyquist.
func (s *Spectrogram) magnitude(frame, bin int) float64 {
	if frame < 0 || frame >= len(s.Frames) {
		return 0
	}
	row := s.Frames[frame]
	if bin < 0 || bin >= len(row) {
		return 0
	}
	c := row[bin]
	return math.Hypot(float64(real(c)), float64(imag(c)))
}
