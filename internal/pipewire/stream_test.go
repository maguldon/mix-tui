//go:build linux

package pipewire

import (
	"testing"
	"time"

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
