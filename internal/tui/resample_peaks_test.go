package tui

import (
	"math"
	"testing"

	"github.com/maguldon/mix-tui/internal/engine"
)

// TestResamplePeaksMatchesAReference is the regression test for the peak
// resampler.
//
// The original version computed its source window in cells but indexed it
// against the entry arrays (2 per cell), then clamped the upper bound to the
// cell count instead of the entry count. For any panel narrower than the 64
// cells the cache holds -- layouts A and D both clamp the deck column to
// 24-52 -- the right half of the envelope was sampled from the wrong offsets,
// so a transient on the right of the track vanished and the left and right
// halves of a braille cell disagreed.
//
// Checking against a brute-force reference rather than against the previous
// output is what makes this meaningful: a test that compares to "what the
// function did" passes just as happily on a broken implementation.
func TestResamplePeaksMatchesAReference(t *testing.T) {
	const n = 128 // the engine's cache: 64 cells x 2 entries
	p := engine.Peaks{Min: make([]float32, n), Max: make([]float32, n)}
	for i := range p.Max {
		// A ramp with a distinctive spike in each half, so a mis-offset read
		// lands on a different value rather than a similar one.
		v := float32(i)
		if i == 10 {
			v = 100
		}
		if i == 100 {
			v = -100
		}
		p.Min[i], p.Max[i] = -v, v
	}

	for _, w := range []int{1, 5, 10, 24, 32, 52, 63, 64, 80, 120} {
		out := resamplePeaks(p, w)

		if len(out.Max) != w*2 || len(out.Min) != w*2 {
			t.Errorf("w=%d: got %d entries, want %d", w, len(out.Max), w*2)
			continue
		}
		for c := 0; c < w*2; c++ {
			lo := c * n / (w * 2)
			hi := (c + 1) * n / (w * 2)
			if hi <= lo {
				hi = lo + 1
			}
			var mn, mx float32 = p.Min[lo], p.Max[lo]
			for i := lo + 1; i < hi; i++ {
				if p.Min[i] < mn {
					mn = p.Min[i]
				}
				if p.Max[i] > mx {
					mx = p.Max[i]
				}
			}
			if out.Min[c] != mn || out.Max[c] != mx {
				t.Errorf("w=%d entry %d: got (%v,%v), want (%v,%v)", w, c, out.Min[c], out.Max[c], mn, mx)
			}
		}
	}
}

// TestResamplePeaksPreservesTransients. Narrowing takes extremes, so a spike
// must survive even when it lands in a single collapsed entry. This is the
// property that makes a downscaled waveform still read as the track.
func TestResamplePeaksPreservesTransients(t *testing.T) {
	const n = 128
	p := engine.Peaks{Min: make([]float32, n), Max: make([]float32, n)}
	// A single huge transient at index 100 -- deep in the second half.
	p.Max[100] = 1
	p.Min[100] = -1

	for _, w := range []int{8, 24, 52, 64} {
		out := resamplePeaks(p, w)
		found := false
		for c := range out.Max {
			if math.Abs(float64(out.Max[c])) > 0.5 {
				found = true
			}
		}
		if !found {
			t.Errorf("w=%d: the transient at entry 100 vanished", w)
		}
	}
}

// TestResamplePeaksRoundTrips. Widening a cached envelope back to its native
// size must be the identity, so switching layouts does not quietly alter the
// drawn waveform.
func TestResamplePeaksRoundTrips(t *testing.T) {
	const n = 128
	p := engine.Peaks{Min: make([]float32, n), Max: make([]float32, n)}
	for i := range p.Max {
		p.Min[i], p.Max[i] = float32(i%17)*-0.5, float32(i%13)*0.5
	}

	out := resamplePeaks(p, n/2) // 64 cells
	for i := range p.Max {
		if out.Max[i] != p.Max[i] || out.Min[i] != p.Min[i] {
			t.Fatalf("identity resample changed entry %d: (%v,%v) -> (%v,%v)",
				i, p.Min[i], p.Max[i], out.Min[i], out.Max[i])
		}
	}
}

func TestResamplePeaksHandlesEmptyAndZeroWidth(t *testing.T) {
	if got := resamplePeaks(engine.Peaks{}, 10); len(got.Max) != 20 {
		t.Errorf("empty peaks: got %d entries, want 20", len(got.Max))
	}
	p := engine.Peaks{Min: make([]float32, 128), Max: make([]float32, 128)}
	if got := resamplePeaks(p, 0); len(got.Max) != 0 {
		t.Errorf("zero width: got %d entries, want 0", len(got.Max))
	}
}
