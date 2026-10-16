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

// The EnumFormat pod must survive a round trip through spa's parser.
//
// This is the test that would have caught the silent-audio bug. The pod used
// to be produced by calling spa_pod_builder_pop a second time on an
// uninitialised frame, which zeroed the pod header. pw_stream_connect
// accepted that pod and returned success; the daemon created the node; and
// WirePlumber then logged "no usable format found for node N", never
// configured or linked the node, and left the stream in PAUSED forever with
// the process callback never firing.
//
// Nothing in the application reported an error, so the only observable symptom
// was silence. Asserting the pod parses closes that gap without needing a
// daemon, and it fails the instant the builder sequence changes.
func TestConnectParamsPodIsWellFormed(t *testing.T) {
	for _, tc := range []struct{ rate, channels int }{
		{44100, 2},
		{48000, 2},
	} {
		if got := connectParamsSelfcheck(tc.rate, tc.channels); got != tc.rate {
			t.Errorf("connect params for %dHz/%dch do not parse back correctly: got %d, want %d",
				tc.rate, tc.channels, got, tc.rate)
		}
	}
}

// The stream must not report a rate the device has never confirmed.
//
// NewStream used to seed `negotiated` with cfg.SampleRate, and NewStream has
// already coerced a non-positive rate to 44100 by that point — so the store
// could only ever write a positive number and the documented "0 if it has not
// said" was unreachable. Nothing else ever writes 0, so the field claimed a
// grant for as long as the stream lived: the debug overlay rendered "sample
// rate 44100 (device 44100)" with both halves the same stored integer, and
// mix-tui's startup warning compared that number against the declared rate,
// which is the same number by construction and could not fire.
//
// Read through the real API immediately after construction. No round trip is
// possible yet — Start is what calls pw_stream_connect — so this is a statement
// about the constructor alone and does not depend on the daemon's timing.
func TestStreamDoesNotClaimADeviceRateItHasNotBeenTold(t *testing.T) {
	ring := audio.NewRing(44100)
	s := newTestStream(t, ring)
	defer s.Destroy()

	if s.cfg.SampleRate <= 0 {
		t.Fatal("the rig declares no rate, so it cannot tell the two apart")
	}
	if got := s.NegotiatedRate(); got != 0 {
		t.Errorf("NegotiatedRate() = %d immediately after NewStream, want 0 (declared %d)",
			got, s.cfg.SampleRate)
	}
	if got := s.Stats().NegotiatedRate; got != 0 {
		t.Errorf("Stats().NegotiatedRate = %d immediately after NewStream, want 0 (declared %d)",
			got, s.cfg.SampleRate)
	}
	if s.Stats().RateMismatch {
		t.Error("RateMismatch is set before the daemon has granted anything")
	}
}

// The other half, and the reason dropping the seed is safe: once the daemon
// grants a format, the rate IT granted has to show up. A field that is merely
// stuck at zero would pass the test above while being useless — this is the only
// pair of numbers that can say whether a track is playing at the right speed.
func TestStreamReportsTheRateTheDaemonGrants(t *testing.T) {
	ring := audio.NewRing(44100)
	s := newTestStream(t, ring)
	if err := s.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer s.Stop()
	defer s.Destroy()

	// Polled rather than slept: the grant is asynchronous, and how long it takes
	// is the daemon's business. Only "it eventually arrives" is a property the
	// code controls.
	start := time.Now()
	deadline := start.Add(10 * time.Second)
	for time.Now().Before(deadline) && s.NegotiatedRate() == 0 {
		time.Sleep(10 * time.Millisecond)
	}

	got := s.NegotiatedRate()
	if got == 0 {
		t.Fatalf("the stream started and drained but no rate was granted within %s",
			time.Since(start).Round(time.Millisecond))
	}
	if got < 8000 || got > 192000 {
		t.Errorf("granted rate %d is not a plausible sample rate", got)
	}
	if stats := s.Stats(); stats.NegotiatedRate != got {
		t.Errorf("Stats().NegotiatedRate = %d, NegotiatedRate() = %d", stats.NegotiatedRate, got)
	}
	// The two disagreeing is the only case that sets this, so a granted rate
	// equal to the declared one must not raise it.
	if got != s.cfg.SampleRate && !s.Stats().RateMismatch {
		t.Errorf("granted %d but declared %d and RateMismatch is false", got, s.cfg.SampleRate)
	}
}
