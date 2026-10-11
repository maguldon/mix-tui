//go:build linux

package pipewire

import (
	"testing"
	"time"

	"github.com/maguldon/mix-tui/internal/dsp"
)

func TestOutputStartsBothStreams(t *testing.T) {
	if !Available() {
		t.Skip("no daemon")
	}
	o, err := NewOutput(OutputConfig{SampleRate: 44100, RingFrames: 8820})
	if err != nil {
		t.Fatal(err)
	}
	if err := o.Start(); err != nil {
		t.Fatal(err)
	}
	defer o.Close()
	time.Sleep(300 * time.Millisecond)
	m, c := o.Stats()
	if m.Callbacks == 0 {
		t.Error("MAIN never drained")
	}
	if c.Callbacks == 0 {
		t.Error("CUE never drained")
	}
}

// Review Focus #4: cue content must never appear in MAIN. Assert it
// STRUCTURALLY, because that is the guarantee — MAIN and CUE wrap different
// rings and different graph nodes. Frame counts alone would be idle-clock
// driven and prove nothing.
func TestCUEIsNotWrittenToMain(t *testing.T) {
	if !Available() {
		t.Skip("no daemon")
	}
	o, err := NewOutput(OutputConfig{SampleRate: 44100, RingFrames: 8820})
	if err != nil {
		t.Fatal(err)
	}
	if err := o.Start(); err != nil {
		t.Fatal(err)
	}
	defer o.Close()
	time.Sleep(200 * time.Millisecond)

	if o.MainTarget() == o.CueTarget() {
		t.Error("MAIN and CUE share a RenderTarget — cue would reach MAIN")
	}
}

func TestOutputNegotiatesSampleRate(t *testing.T) {
	if !Available() {
		t.Skip("no daemon")
	}
	o, err := NewOutput(OutputConfig{RingFrames: 8820}) // rate 0 = negotiate
	if err != nil {
		t.Fatal(err)
	}
	defer o.Close()
	r := o.SampleRate()
	if r < 8000 || r > 192000 {
		t.Errorf("negotiated rate %d is implausible", r)
	}
}

func TestOutputCloseIsIdempotentAndOrdered(t *testing.T) {
	if !Available() {
		t.Skip("no daemon")
	}
	o, err := NewOutput(OutputConfig{SampleRate: 44100, RingFrames: 8820})
	if err != nil {
		t.Fatal(err)
	}
	_ = o.Start()
	if err := o.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
	// Closing twice must be safe: teardown runs from signal handlers and
	// deferred paths that can both fire.
	if err := o.Close(); err != nil {
		t.Errorf("second Close: %v", err)
	}
}

// The structural guarantee above, verified WITHOUT a working sink, so it is
// meaningful in an environment where no graph link can form. MAIN and CUE must
// wrap different rings and different nodes; if either is shared, cue audio
// reaches the main output — a routing bug that otherwise only shows up
// mid-performance.
func TestMainAndCueAreStructurallySeparate(t *testing.T) {
	if !Available() {
		t.Skip("no daemon")
	}
	o, err := NewOutput(OutputConfig{SampleRate: 44100, RingFrames: 8820})
	if err != nil {
		t.Fatal(err)
	}
	defer o.Close()

	main, cue := o.MainTarget(), o.CueTarget()
	if main == nil || cue == nil {
		t.Fatal("MAIN or CUE target is nil")
	}
	if main == cue {
		t.Fatal("MAIN and CUE share a RenderTarget")
	}
	if o.mainStream == o.cueStream {
		t.Fatal("MAIN and CUE share a Stream")
	}
	if o.mainStream.ring == o.cueStream.ring {
		t.Fatal("MAIN and CUE share a ring buffer")
	}
	// Writing to the cue ring must leave the main ring untouched.
	o.cueStream.ring.Write(dsp.Sine(440, 0.1))
	if o.mainStream.ring.AvailableRead() != 0 {
		t.Errorf("cue write leaked %d frames into MAIN's ring",
			o.mainStream.ring.AvailableRead())
	}
}

// Default ring depth is ~200ms at the negotiated rate; too shallow and the
// render goroutine starves between wakeups.
func TestOutputDefaultsRingFrames(t *testing.T) {
	if !Available() {
		t.Skip("no daemon")
	}
	o, err := NewOutput(OutputConfig{SampleRate: 48000})
	if err != nil {
		t.Fatal(err)
	}
	defer o.Close()
	if got := o.mainStream.ring.Capacity(); got < 48000/5 {
		t.Errorf("MAIN ring holds %d frames, want at least %d (200ms at 48k)",
			got, 48000/5)
	}
}
