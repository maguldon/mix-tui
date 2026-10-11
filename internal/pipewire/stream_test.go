//go:build linux

package pipewire

import (
	"sync"
	"testing"
	"time"
	"unsafe"

	"github.com/maguldon/mix-tui/internal/audio"
	"github.com/maguldon/mix-tui/internal/dsp"
	"github.com/maguldon/mix-tui/internal/engine"
)

func newTestStream(t *testing.T, ring *audio.Ring) *Stream {
	t.Helper()
	if !Available() {
		t.Skip("no pipewire daemon")
	}
	ctx, err := NewContext("mix-tui-stream-test")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ctx.Close() })
	s, err := NewStream(ctx, StreamConfig{
		NodeName: "mix-tui: test", SampleRate: 44100, Channels: 2,
	}, ring)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestStreamConnectsAndStarts(t *testing.T) {
	ring := audio.NewRing(44100)
	s := newTestStream(t, ring)
	if err := s.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer s.Stop()
	defer s.Destroy()
	time.Sleep(200 * time.Millisecond)
	if s.Stats().Callbacks == 0 {
		t.Error("no drain callbacks fired — stream is not processing")
	}
}

func TestStreamDrainsWhatIsWritten(t *testing.T) {
	ring := audio.NewRing(1 << 20)
	s := newTestStream(t, ring)
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	defer s.Stop()
	defer s.Destroy()

	tone := dsp.Sine(440, 2.0)
	for off := 0; off < len(tone); off += 1024 {
		end := off + 1024
		if end > len(tone) {
			end = len(tone)
		}
		ring.Write(tone[off:end])
	}
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		if s.Stats().FramesDrained > 44100 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if s.Stats().FramesDrained == 0 {
		t.Fatal("nothing was delivered to the device")
	}
}

func TestStreamStopIsIdempotent(t *testing.T) {
	ring := audio.NewRing(44100)
	s := newTestStream(t, ring)
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	if err := s.Stop(); err != nil {
		t.Errorf("first Stop: %v", err)
	}
	if err := s.Stop(); err != nil {
		t.Errorf("second Stop must be a no-op, got %v", err)
	}
	s.Destroy()
}

func TestStreamDestroyAfterStopDoesNotLeak(t *testing.T) {
	if !Available() {
		t.Skip("no daemon")
	}
	for i := 0; i < 5; i++ {
		ctx, err := NewContext("mix-tui-leak")
		if err != nil {
			t.Fatal(err)
		}
		ring := audio.NewRing(44100)
		s, err := NewStream(ctx, StreamConfig{NodeName: "mix-tui: leak",
			SampleRate: 44100, Channels: 2}, ring)
		if err != nil {
			t.Fatal(err)
		}
		s.Start()
		time.Sleep(50 * time.Millisecond)
		s.Stop()
		s.Destroy()
		ctx.Close()
	}
}

// Review Focus #2: the drain path must not block on Go scheduling. The
// PipeWire data loop is real-time prioritised, so a Consume that waited would
// stall the thread.
func TestRingTargetNeverBlocksWhenEmpty(t *testing.T) {
	ring := audio.NewRing(1024) // deliberately never written
	rt := engine.RingTarget(ring)
	out := make(dsp.Stereo, 512*2)
	done := make(chan bool, 1)
	go func() { done <- rt.Consume(out) }()
	select {
	case ok := <-done:
		if ok {
			t.Error("Consume reported success on an empty ring")
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Consume blocked on an empty ring — this stalls the audio thread")
	}
}

// Review Focus #2, measured: an allocating drain callback is a dropout
// generator that only shows up under load. This is the single most important
// property of the audio path and it is verifiable without a daemon.
func TestDrainPathDoesNotAllocate(t *testing.T) {
	ring := audio.NewRing(1 << 20)
	rt := engine.RingTarget(ring)
	ring.Write(dsp.Sine(440, 1.0))
	out := make(dsp.Stereo, 512*2)

	// Warm the ring so the measured calls actually find audio rather than
	// short-circuiting on the empty path.
	rt.Consume(out)

	allocs := testing.AllocsPerRun(200, func() { rt.Consume(out) })
	if allocs > 0 {
		t.Errorf("Consume allocated %v times per call — the audio thread must not", allocs)
	}
}

// I1: Destroy must not free the stream while a drain is in flight. The drain
// runs on the RT thread and calls pw_stream_dequeue_buffer(s.stream); if
// Destroy nils or frees s.stream first, that is a nil deref inside libpipewire
// on the audio thread. This drives Destroy concurrently with drain calls; under
// -race it must be clean, and no nil deref may escape.
func TestDestroyRacesDrainSafely(t *testing.T) {
	ring := audio.NewRing(1 << 16)
	s := newTestStream(t, ring)

	var wg sync.WaitGroup
	// Drive drains concurrently with teardown.
	//
	// Deliberately NOT calling Start(): an activated stream is driven by the
	// real PipeWire loop thread, and this test would then have two goroutines
	// inside pw_stream_dequeue_buffer on one stream. That corrupts libpipewire
	// and segfaults for reasons unrelated to the gate under test. Leaving the
	// stream inactive means the only drainer is this goroutine, which is
	// exactly the race the barrier has to survive.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			s.drain()
		}
	}()
	// Tear down underneath it.
	_ = s.Stop()
	_ = s.Destroy()
	wg.Wait()
}

// The teardown barrier itself, testable with no daemon and no live stream: the
// gate must stop a drain that starts after teardown begins, and Destroy must
// not return until an in-flight drain has finished.
func TestDrainGateStopsWorkAfterTeardown(t *testing.T) {
	s := &Stream{}
	// With no drainer running, entering must be refused once the gate is set.
	s.teardown.closeAndWait()
	s.drain() // must return immediately without touching a nil stream
	// And again with a zero stream it must not panic either way.
	s.drain()
}

// TestStreamReconnectsAfterStop covers the stop-then-play cycle. A
// disconnected pw_stream can be reconnected, so Stop must clear the started
// flag; if it did not, a later Start would silently return nil and the stream
// would stay dead with nothing to indicate why.
func TestStreamReconnectsAfterStop(t *testing.T) {
	ring := audio.NewRing(1 << 16)
	s := newTestStream(t, ring)

	if err := s.Start(); err != nil {
		t.Skipf("cannot connect in this environment: %v", err)
	}
	if err := s.Stop(); err != nil {
		t.Fatalf("stop: %v", err)
	}
	// The started flag must be clear, or the next Start is a no-op.
	if s.started.Load() {
		t.Fatal("Stop left started set; Start would silently no-op")
	}
	// A second Start must actually attempt to reconnect rather than return
	// early on the idempotency check.
	if err := s.Start(); err != nil {
		t.Skipf("cannot reconnect in this environment: %v", err)
	}
	if !s.started.Load() {
		t.Fatal("Start after Stop did not mark the stream started")
	}
	_ = s.Stop()
	_ = s.Destroy()
}

// TestDrainGateIsLockFree pins the realtime-safety property of the teardown
// gate. Spec §4.3 forbids locks in the PipeWire process callback. This does not
// try to prove the absence of a mutex (hard to assert directly); it pins the
// behaviour that makes the property matter — the gate admits and releases
// drains without any coordination primitive that could block the audio thread,
// and it correctly refuses new drains once closed.
func TestDrainGateIsLockFree(t *testing.T) {
	var someInt int
	var g drainGate

	// Open: enters are admitted. The gate only carries the pointer through, so
	// any non-nil value is fine.
	nonNil := unsafe.Pointer(&someInt)
	if _, ok := g.enter(func() unsafe.Pointer { return nonNil }); !ok {
		t.Fatal("enter refused on an open gate")
	}
	// A second concurrent drain is admitted too (count 2).
	if _, ok := g.enter(func() unsafe.Pointer { return nonNil }); !ok {
		t.Fatal("enter refused a concurrent drain")
	}

	g.leave()
	g.leave()

	// Closing must wait for nothing (count is 0) and must then refuse enters.
	g.closeAndWait()
	if _, ok := g.enter(func() unsafe.Pointer { return nil }); ok {
		t.Fatal("enter admitted after closeAndWait")
	}
	// closeAndWait is idempotent.
	g.closeAndWait()
}

// TestDrainGateCloseWaitsForActiveDrain checks closeAndWait actually waits
// rather than returning immediately while a drain is still inside the callback.
// If it returned early, Destroy would free the stream underneath an in-flight
// drain — the use-after-free this gate exists to prevent.
func TestDrainGateCloseWaitsForActiveDrain(t *testing.T) {
	var someInt int
	var g drainGate
	if _, ok := g.enter(func() unsafe.Pointer { return unsafe.Pointer(&someInt) }); !ok {
		t.Fatal("enter refused")
	}

	released := make(chan struct{})
	waited := make(chan struct{})
	go func() {
		g.closeAndWait() // must block: one drain is still active
		close(waited)
	}()
	// Give closeAndWait a chance to (incorrectly) return.
	time.Sleep(20 * time.Millisecond)
	select {
	case <-waited:
		t.Fatal("closeAndWait returned while a drain was still active")
	default:
	}
	// Now let the drain leave; closeAndWait must then finish.
	go func() {
		g.leave()
		close(released)
	}()
	<-released
	select {
	case <-waited:
	case <-time.After(2 * time.Second):
		t.Fatal("closeAndWait did not return after the last drain left")
	}
}
