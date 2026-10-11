package engine

import "sync"

// peaksWidth is the resolution cached peaks are computed at.
//
// One resolution covers every panel width the layouts use: the deck panels are
// 24-52 columns, so 64 covers them all with headroom. A view narrower than this
// samples the envelope down rather than rescanning the audio.
const peaksWidth = 64

// computePeaks reduces PCM to a min/max envelope of width*2 columns.
//
// This is the expensive part of drawing a waveform: a five-minute track takes
// roughly 33ms. Doing it per frame at 30fps would consume two CPU-seconds per
// wall-clock second, so it happens once when a track loads.
func computePeaks(pcm []float32, width int) Peaks {
	n := width * 2
	p := Peaks{Min: make([]float32, n), Max: make([]float32, n)}
	if width <= 0 || len(pcm) == 0 {
		return p
	}
	frames := len(pcm) / 2
	if frames == 0 {
		return p
	}
	for c := 0; c < n; c++ {
		start := c * frames / n
		end := (c + 1) * frames / n
		if end <= start {
			end = start + 1
		}
		if end > frames {
			end = frames
		}
		var mn, mx float32
		for f := start; f < end; f++ {
			l, r := pcm[f*2], pcm[f*2+1]
			if l < mn {
				mn = l
			}
			if l > mx {
				mx = l
			}
			if r < mn {
				mn = r
			}
			if r > mx {
				mx = r
			}
		}
		p.Min[c] = mn
		p.Max[c] = mx
	}
	return p
}

// peaksCache memoises the envelope per deck. It is guarded because a Deck is
// read from the render goroutine and from Snapshot, which holds the engine
// mutex but not the deck's own.
type peaksCache struct {
	mu   sync.Mutex
	p    Peaks
	ok   bool
	path string
}

// invalidate drops the cache; called when a new track is loaded.
func (c *peaksCache) invalidate() {
	c.mu.Lock()
	c.p, c.ok, c.path = Peaks{}, false, ""
	c.mu.Unlock()
}

// get returns the cached envelope, computing it if the track or resolution
// changed.
func (c *peaksCache) get(path string, pcm []float32, width int) Peaks {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.ok && c.path == path {
		return c.p
	}
	c.p = computePeaks(pcm, width)
	c.ok = true
	c.path = path
	return c.p
}
