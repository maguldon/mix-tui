//go:build linux

package pipewire

import (
	"testing"

	"github.com/maguldon/mix-tui/internal/audio"
)

// TestPumperTargetsAtLeastOneDeviceBuffer is the fix for a failure this package
// shipped for its whole life: a ring held at a fixed target can only serve a
// target-sized callback, so a device asking for more came up short on EVERY
// callback while the starve counter read zero.
func TestPumperTargetsAtLeastOneDeviceBuffer(t *testing.T) {
	mainStream := &Stream{}
	cueStream := &Stream{}
	p := &Pumper{cfg: PumpConfig{TargetLatencyFrames: 8820}}
	// No streams attached: the configured target stands alone.
	if got := p.target(); got != 8820 {
		t.Fatalf("with no streams, target = %d, want the configured 8820", got)
	}

	p = &Pumper{
		cfg:        PumpConfig{TargetLatencyFrames: 8820},
		mainStream: mainStream,
		cueStream:  cueStream,
	}
	// Still nothing observed, so still the configured target.
	if got := p.target(); got != 8820 {
		t.Fatalf("before any callback, target = %d, want the configured 8820", got)
	}

	// The device asks for more than the target. Measured on this host with a
	// driver batching to ~12,200-frame callbacks.
	mainStream.maxBuffer.Store(12200)
	got := p.target()
	if got < 12200 {
		t.Errorf("target = %d, want at least the 12200-frame buffer the device asked for; "+
			"a ring at %d can never cover it", got, got)
	}

	// The larger of the two streams wins, whichever it is.
	mainStream.maxBuffer.Store(8820)
	cueStream.maxBuffer.Store(20000)
	if got := p.target(); got < 20000 {
		t.Errorf("target = %d, want at least the CUE stream's 20000-frame buffer", got)
	}

	// A device that asks for LESS than the target must not pull the target
	// down: the configured latency is a floor chosen for glitch resistance, not
	// something to be traded away because one driver happens to be greedy in
	// the other direction.
	mainStream.maxBuffer.Store(1024)
	cueStream.maxBuffer.Store(1024)
	if got := p.target(); got != 8820 {
		t.Errorf("target = %d, want the configured 8820 — a small device buffer must not "+
			"shrink the buffer below the configured latency", got)
	}
}

// TestRingMustOutliveThePumpTarget is the invariant the defaults exist to hold.
// A ring sized equal to (or smaller than) the depth the pump fills it to cannot
// serve a callback of that depth plus anything the device adds on top.
func TestRingMustOutliveThePumpTarget(t *testing.T) {
	const rate = 44100

	// The REAL default, not a reconstruction of it. An earlier version of this
	// test recomputed the expected value from the same arithmetic the code
	// used, and therefore passed unchanged when the code's arithmetic was
	// reverted — a test that cannot fail against the bug it is written for.
	ring := defaultRingFrames(rate)
	// The pump's configured default target.
	target := rate / 5

	if ring <= target {
		t.Fatalf("default ring of %d frames does not exceed the default pump target of %d: "+
			"the ring would be full at the target, leaving no room to cover any buffer "+
			"larger than the target", ring, target)
	}

	// And it must be able to hold a deeply batched device buffer, or the floor
	// the pump raises to would be unreachable.
	batched := rate * 7 / 25 // ~280ms, the worst measured on this host
	if ring <= batched {
		t.Errorf("default ring of %d frames cannot hold a %d-frame batched buffer",
			ring, batched)
	}
}

// TestAMaxBufferRingReadStillDrainsCompletely guards the obvious way to "fix"
// the above by enlarging things: a bigger ring must not change what a full read
// yields.
func TestAMaxBufferRingReadStillDrainsCompletely(t *testing.T) {
	ring := audio.NewRing(1 << 16)
	ring.Write(make([]float32, 4096*2))
	if got := ring.Read(make([]float32, 4096*2)); got != 4096 {
		t.Errorf("read %d frames from a ring holding 4096", got)
	}
	if got := ring.Read(make([]float32, 4096*2)); got != 0 {
		t.Errorf("second read returned %d frames, want 0 — the first did not drain it", got)
	}
}
