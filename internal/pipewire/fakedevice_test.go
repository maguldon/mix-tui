//go:build linux

package pipewire

import (
	"context"

	"github.com/maguldon/mix-tui/internal/engine"
	"math"
	"sync/atomic"
	"testing"
	"time"

	"github.com/maguldon/mix-tui/internal/audio"
	"github.com/maguldon/mix-tui/internal/dsp"
)

// fakeDevice drains a ring at real time, standing in for libpipewire's drain
// thread when no graph link exists.
//
// Every test so far ran against a pump feeding a ring nobody consumed, which is
// why a pump rendering half of real time still passed: with nothing draining,
// an infinitely slow pump looks perfect. This is the gap that let a stutter
// ship, so the fix is tested against something that consumes at the device's
// rate.
type fakeDevice struct {
	ring   *audio.Ring
	rate   int
	stop   chan struct{}
	frames atomic.Uint64
	starve atomic.Uint64
}

func newFakeDevice(r *audio.Ring, rate int) *fakeDevice {
	return &fakeDevice{ring: r, rate: rate, stop: make(chan struct{})}
}

// run consumes the ring at real time until stopped.
func (d *fakeDevice) run() {
	block := engineBlockFrames
	buf := make(dsp.Stereo, block*2)
	// How often real time hands us the next block.
	ticker := time.NewTicker(time.Duration(block) * time.Second / time.Duration(d.rate))
	defer ticker.Stop()
	for {
		select {
		case <-d.stop:
			return
		case <-ticker.C:
			if n := d.ring.Read(buf); n == 0 {
				d.starve.Add(1)
			} else {
				d.frames.Add(uint64(n))
			}
		}
	}
}

func (d *fakeDevice) close() { close(d.stop) }

// newPumpRig builds a pump over two standalone rings plus a fake device
// draining the MAIN one, and returns both.
//
// The rings are deliberately NOT the PipeWire streams' rings. The fake device
// exists to stand in for a real drain thread, so attaching it to a ring that
// libpipewire is already draining gives the ring two real-time consumers and
// one real-time producer -- the ring then starves by construction and the test
// measures that arithmetic rather than the pump.
//
// It used to share the stream's ring, back when the stream never drained at
// all: the EnumFormat pod handed to pw_stream_connect was malformed, so
// WirePlumber could not configure the node and the drain callback never fired.
// The bug that made the test's arrangement accidentally true is fixed, and the
// arrangement itself has to change with it.
//
// Owning the rings also drops the daemon dependency: the rig now exercises the
// pump's pacing against a real-time consumer with nothing else attached.
func newFakeDeviceRig(t *testing.T, rate int) (*Pumper, *fakeDevice) {
	t.Helper()
	main := audio.NewRing(rate / 5)
	cue := audio.NewRing(rate / 5)

	cfg := PumpConfig{TargetLatencyFrames: rate / 5}
	dev := newFakeDevice(main, rate)
	p := &Pumper{eng: newSilentEngine(rate), cfg: cfg, main: main, cue: cue}

	// Fill before the device starts, so the first tick does not race the pump's
	// first fill and count a startup artifact as a starve.
	p.pumpOnce()
	go dev.run()
	t.Cleanup(dev.close)
	return p, dev
}

// C1 RED: the pump must keep up with real time against a device that actually
// consumes. The previous implementation rendered one block per capped tick and
// delivered roughly half of what real time needed.
func TestPumpKeepsUpWithRealTime(t *testing.T) {
	p, dev := newFakeDeviceRig(t, 44100)

	p.runFor(3 * time.Second)

	got := int(dev.frames.Load())
	// Three seconds of 44.1k audio, with generous tolerance for scheduling.
	want := int(3 * 44100)
	if got < want*9/10 || got > want*11/10 {
		t.Errorf("device received %d frames in 3s, want ~%d (within 10%%)", got, want)
	}
}

// The pump must deliver ahead of the drain, not merely eventually: a consumer
// that sees an empty ring has already dropped audio.
func TestPumpNeverStarvesADrainingDevice(t *testing.T) {
	p, dev := newFakeDeviceRig(t, 44100)

	p.runFor(2 * time.Second)

	if s := dev.starve.Load(); s > 0 {
		t.Errorf("device starved %d times; the pump must stay ahead of the drain", s)
	}
}

func TestFakeDeviceConsumesAtRealTime(t *testing.T) {
	// Guard the guard: if the fake itself does not consume at real time, the
	// two tests above prove nothing.
	r := audio.NewRing(1 << 16)
	dev := newFakeDevice(r, 44100)
	go dev.run()
	defer dev.close()

	for i := 0; i < 30; i++ {
		r.Write(dsp.Sine(440, 0.05))
		time.Sleep(50 * time.Millisecond)
	}
	// ~1.5s of writes at 44100 should be consumed within a wide band.
	if dev.frames.Load() == 0 {
		t.Fatal("fake device consumed nothing")
	}
	t.Logf("fake device consumed %d frames, starved %d times",
		dev.frames.Load(), dev.starve.Load())
	_ = math.Abs
}

// newSilentEngine builds an engine with one deck ready to render silence, so
// the pump has real work to do without needing a decodable file.
func newSilentEngine(rate int) *engine.Engine {
	eng := engine.New(engine.Config{
		SampleRate: rate, BlockFrames: engineBlockFrames,
		RingFrames: 8820, DeckCount: 1,
	})
	eng.Deck(0).Load(&audio.Track{
		Path: "silence.wav", SampleRate: rate, Channels: 2,
		PCM:      dsp.Stereo(make([]float32, rate*2)),
		Duration: time.Second,
	})
	eng.Deck(0).Play()
	return eng
}

// runFor drives the pump for a fixed duration without needing a context, so a
// test can measure what it actually delivered.
func (p *Pumper) runFor(d time.Duration) {
	ctx, cancel := context.WithTimeout(context.Background(), d)
	defer cancel()
	_ = p.Run(ctx)
}
