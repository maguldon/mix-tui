//go:build linux

package pipewire

import (
	"fmt"
	"sync"

	"github.com/maguldon/mix-tui/internal/audio"

	"github.com/maguldon/mix-tui/internal/engine"
)

// OutputConfig describes the two-stream output.
type OutputConfig struct {
	// SampleRate is the rate both streams declare. Zero means negotiate with
	// the daemon, falling back to 44100 when the daemon will not say.
	SampleRate int
	// RingFrames is the per-stream ring depth. Zero defaults to ~200ms.
	RingFrames int
	// MainTarget and CueTarget optionally name a node.name or node.id to route
	// to. Empty means the daemon's default sink.
	MainTarget string
	CueTarget  string
}

// Output owns both playback streams.
//
// MAIN and CUE are entirely separate: distinct nodes, distinct rings, distinct
// drains. Sharing any of those is a cueing bug — headphone audio leaking to the
// PA is the single worst failure this application has, because it is the one
// a performer will not hear until it has already happened.
type Output struct {
	cfg Config

	mainStream *Stream
	cueStream  *Stream
	ctx        *Context

	// ownsCtx records whether this Output created the context. An Output built
	// by NewOutput does; one that merely wraps an existing context does not, and
	// must not close it.
	ownsCtx bool

	mu      sync.Mutex
	started bool
	closed  bool
}

// Config is an alias kept for readability at call sites that already have a
// local named config.
type Config = OutputConfig

// defaultRingFrames is the per-stream ring depth when the caller does not
// choose one.
//
// HEADROOM, NOT DEPTH. The pump keeps each ring filled to about 200ms (see
// NewPumper), so a ring of 200ms has by construction nothing spare: the moment
// the device asks for a buffer bigger than the target, the ring cannot possibly
// cover it and every callback comes up short.
//
// That is not hypothetical. A driver that cannot get realtime priority batches
// several quanta into one callback, and was measured handing this code
// 12,200-frame buffers against a former 8,820-frame ring — so roughly a third of
// every callback went out as stale buffer contents while the stats reported zero
// starves, because the ring was never empty, only short.
//
// One second costs well under a megabyte across both rings. It costs no
// latency: latency is the pump's target, not the ring's capacity, and the pump
// still stops filling at the target — it merely raises that target to cover an
// oversized device buffer (see Pumper.target).
func defaultRingFrames(rate int) int {
	if rate <= 0 {
		rate = 44100
	}
	return rate
}

// NewOutput creates both streams and the context they share.
func NewOutput(cfg OutputConfig) (*Output, error) {
	if cfg.SampleRate <= 0 {
		// There is no supported way to read the graph's rate back (issue #23),
		// so 0 means the 44100 default. Declaring it puts exactly one resampler
		// in the path — PipeWire's — rather than two, which is what a double
		// resample would cost. Removing that one resampler was measured against
		// a wildcard-rate stream and the difference fell below the noise floor,
		// so it does not justify rebuilding the engine when the device changes.
		cfg.SampleRate = 44100
	}
	if cfg.RingFrames <= 0 {
		cfg.RingFrames = defaultRingFrames(cfg.SampleRate)
	}

	o := &Output{cfg: cfg}

	ctx, err := NewContext("mix-tui")
	if err != nil {
		return nil, fmt.Errorf("pipewire: %w", err)
	}
	o.ctx = ctx
	o.ownsCtx = true

	mainRing := audio.NewRing(cfg.RingFrames)
	cueRing := audio.NewRing(cfg.RingFrames)

	main, err := NewStream(ctx, StreamConfig{
		NodeName:   "mix-tui: main",
		Target:     cfg.MainTarget,
		SampleRate: cfg.SampleRate,
		Channels:   2,
	}, mainRing)
	if err != nil {
		ctx.Close()
		return nil, fmt.Errorf("pipewire: main stream: %w", err)
	}
	o.mainStream = main

	cue, err := NewStream(ctx, StreamConfig{
		NodeName:   "mix-tui: cue",
		Target:     cfg.CueTarget,
		SampleRate: cfg.SampleRate,
		Channels:   2,
	}, cueRing)
	if err != nil {
		main.Destroy()
		ctx.Close()
		return nil, fmt.Errorf("pipewire: cue stream: %w", err)
	}
	o.cueStream = cue

	return o, nil
}

// Start connects both streams.
func (o *Output) Start() error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.closed {
		return fmt.Errorf("pipewire: output is closed")
	}
	if o.started {
		return nil
	}
	if err := o.mainStream.Start(); err != nil {
		return err
	}
	if err := o.cueStream.Start(); err != nil {
		// Unwind: a half-started pair would leave CUE silently dead.
		_ = o.mainStream.Stop()
		return err
	}
	o.started = true
	return nil
}

// Stop disconnects both streams. It is idempotent.
func (o *Output) Stop() error {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.started = false
	mainErr := o.mainStream.Stop()
	cueErr := o.cueStream.Stop()
	if mainErr != nil {
		return mainErr
	}
	return cueErr
}

// Close stops and destroys both streams, then the context. It is idempotent
// because teardown can be reached from both a signal handler and a deferred
// call.
func (o *Output) Close() error {
	o.mu.Lock()
	if o.closed {
		o.mu.Unlock()
		return nil
	}
	o.closed = true
	o.started = false
	main, cue, ctx, owns := o.mainStream, o.cueStream, o.ctx, o.ownsCtx
	o.mu.Unlock()

	// Order matters: stop the drains, then destroy the streams, then the
	// context. Destroying a stream while its drain can still fire would call
	// into freed memory on the audio thread.
	var firstErr error
	if main != nil {
		_ = main.Stop()
		if err := main.Destroy(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if cue != nil {
		_ = cue.Stop()
		if err := cue.Destroy(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if owns && ctx != nil {
		if err := ctx.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// SampleRate reports the rate both streams were created with.
func (o *Output) SampleRate() int { return o.cfg.SampleRate }

// MainTarget returns the consumer for the main bus.
func (o *Output) MainTarget() engine.RenderTarget { return o.mainStream.AsRenderTarget() }

// CueTarget returns the consumer for the headphone bus.
func (o *Output) CueTarget() engine.RenderTarget { return o.cueStream.AsRenderTarget() }

// Stats returns per-stream drain health.
func (o *Output) Stats() (main, cue Stats) {
	return o.mainStream.Stats(), o.cueStream.Stats()
}

// Underruns returns the summed ring underrun count, for the Snapshot.
//
// The rings count a read that found them empty. That is the consumer stalling,
// which is the opposite of the render side starving, so it is reported
// separately from the streams' starve counters.
func (o *Output) Underruns() uint64 {
	return o.mainStream.ring.Underruns() + o.cueStream.ring.Underruns()
}
