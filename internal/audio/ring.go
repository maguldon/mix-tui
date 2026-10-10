package audio

import (
	"sync/atomic"

	"github.com/maguldon/mix-tui/internal/dsp"
)

// Ring is a lock-free single-producer single-consumer ring buffer of stereo
// frames.
//
// It is the boundary between the render goroutine, which produces audio ahead
// of real time, and the audio callback, which consumes it on the device's
// clock. The two never share a lock: the producer owns the write index and the
// consumer owns the read index, each of which is only ever written by one
// goroutine.
//
// The contract that matters (spec §4.3):
//
//   - Read NEVER blocks. If the ring is empty it returns 0 immediately and
//     counts an underrun. Blocking the audio callback to wait for audio is
//     unrecoverable; a brief dropout is not.
//   - Read and Write NEVER allocate once the ring is constructed.
type Ring struct {
	// buf holds interleaved stereo. Capacity is a power of two so index
	// wrapping is a mask rather than a modulo.
	buf  []float32
	mask uint64

	// writeAt and readAt are monotonically increasing frame counters, not
	// wrapped indices. That removes the "full" versus "empty" ambiguity a
	// wrapped-index ring has to resolve with a spare slot.
	writeAt atomic.Uint64
	readAt  atomic.Uint64

	underruns atomic.Uint64
}

// NewRing returns a ring holding at least capacityFrames stereo frames.
// Capacity is rounded up to a power of two, with a floor: a ring too small to
// hold a block plus lookahead would starve the producer for no benefit.
func NewRing(capacityFrames int) *Ring {
	const minFrames = 4096
	if capacityFrames < minFrames {
		capacityFrames = minFrames
	}
	cap := nextPow2(capacityFrames)
	return &Ring{
		buf:  make([]float32, cap*2),
		mask: uint64(cap - 1),
	}
}

func nextPow2(n int) int {
	p := 1
	for p < n {
		p <<= 1
	}
	return p
}

// Capacity returns the ring's frame capacity.
func (r *Ring) Capacity() int { return int(r.mask) + 1 }

// Write copies as much of src as fits and returns the frames written. Writing
// to a full ring is not an error: the producer simply waits for the consumer,
// and returning a short count lets it try again after the next render.
func (r *Ring) Write(src dsp.Stereo) int {
	n := len(src) / 2
	if n == 0 {
		return 0
	}
	w := r.writeAt.Load()
	rd := r.readAt.Load()
	used := w - rd
	free := r.Capacity() - int(used)
	if free <= 0 {
		return 0
	}
	if n > free {
		n = free
	}

	start := int(w&r.mask) * 2
	chunk := n * 2
	if start+chunk <= len(r.buf) {
		copy(r.buf[start:start+chunk], src[:chunk])
	} else {
		// Wrap: copy the tail, then the head.
		first := len(r.buf) - start
		copy(r.buf[start:], src[:first])
		copy(r.buf[:chunk-first], src[first:chunk])
	}
	r.writeAt.Store(w + uint64(n))
	return n
}

// Read fills as much of dst as is available and returns the frames read.
//
// It returns 0 immediately when the ring is empty, counting an underrun. It
// must never block: it is called from the audio callback on the device's
// realtime thread (spec §4.3).
func (r *Ring) Read(dst dsp.Stereo) int {
	want := len(dst) / 2
	if want == 0 {
		return 0
	}
	rd := r.readAt.Load()
	w := r.writeAt.Load()
	avail := int(w - rd)
	if avail <= 0 {
		r.underruns.Add(1)
		return 0
	}
	if avail < want {
		want = avail
	}

	start := int(rd&r.mask) * 2
	chunk := want * 2
	if start+chunk <= len(r.buf) {
		copy(dst[:chunk], r.buf[start:start+chunk])
	} else {
		first := len(r.buf) - start
		copy(dst[:first], r.buf[start:])
		copy(dst[first:chunk], r.buf[:chunk-first])
	}
	r.readAt.Store(rd + uint64(want))
	return want
}

// AvailableRead returns the frames currently readable.
func (r *Ring) AvailableRead() int {
	return int(r.writeAt.Load() - r.readAt.Load())
}

// AvailableWrite returns the frames that can be written without blocking.
func (r *Ring) AvailableWrite() int {
	used := int(r.writeAt.Load() - r.readAt.Load())
	free := r.Capacity() - used
	if free < 0 {
		return 0
	}
	return free
}

// Underruns counts reads that found the ring empty. It surfaces in the
// Snapshot and the debug overlay: a glitch always has exactly one observable
// cause, and this is it (spec §4.3).
func (r *Ring) Underruns() uint64 { return r.underruns.Load() }

// LatencyFrames returns how far ahead of the consumer the producer is.
func (r *Ring) LatencyFrames() int { return r.AvailableRead() }
