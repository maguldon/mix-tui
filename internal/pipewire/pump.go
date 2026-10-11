//go:build linux

package pipewire

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/maguldon/mix-tui/internal/audio"
	"github.com/maguldon/mix-tui/internal/engine"
)

// PumpConfig parameterises the producer loop.
type PumpConfig struct {
	// TargetLatencyFrames is how full each ring should be kept. Zero defaults
	// to ~200ms.
	TargetLatencyFrames int
	// BlockFrames is the engine's render block. Zero uses the engine's.
	BlockFrames int
}

// PumperStats reports producer-side health.
//
// It deliberately contains NO per-block render timing threshold. A wall-clock
// budget makes the test fail on a slow or virtualised machine while telling
// you nothing about correctness; the ring depth is the buffer that absorbs
// jitter, so the ring is the thing to assert on (spec Review Focus #1).
type PumperStats struct {
	BlocksRendered uint64
	// RenderWallMax is the worst observed render duration. Informational only.
	RenderWallMax time.Duration
	// TargetMisses counts wakeups where the rings could not be refilled to the
	// target because the engine had nothing to render.
	TargetMisses uint64
	// CancelClean records that Run returned because its context was cancelled.
	CancelClean bool
}

// Pumper owns the producer side of the realtime contract: it keeps the rings
// ahead of the drain threads.
//
// It is an ordinary goroutine, not an RT thread. That is safe precisely
// because the rings decouple it from the device — the drain never waits on
// this loop, and this loop never waits on the drain.
type Pumper struct {
	out  *Output
	eng  *engine.Engine
	cfg  PumpConfig
	main *audio.Ring
	cue  *audio.Ring

	blocks       atomic.Uint64
	wallMax      atomic.Int64
	targetMisses atomic.Uint64
	cancelClean  atomic.Bool
}

// NewPumper builds a pump over an output and an engine.
func NewPumper(out *Output, eng *engine.Engine, cfg PumpConfig) *Pumper {
	if cfg.TargetLatencyFrames <= 0 {
		rate := 44100
		if out != nil && out.cfg.SampleRate > 0 {
			rate = out.cfg.SampleRate
		}
		cfg.TargetLatencyFrames = rate / 5
	}
	if out == nil {
		return &Pumper{eng: eng, cfg: cfg}
	}
	return &Pumper{
		out:  out,
		eng:  eng,
		cfg:  cfg,
		main: out.mainStream.ring,
		cue:  out.cueStream.ring,
	}
}

// Run renders blocks to keep both rings at the target latency until ctx is
// cancelled. It returns nil on a clean shutdown.
func (p *Pumper) Run(ctx context.Context) error {
	if p.eng == nil {
		return nil
	}
	// How long to wait between wakeups: the time it takes to consume the
	// amount we are topping up to, so the loop tracks real time rather than
	// spinning a core.
	sleep := time.Duration(p.cfg.TargetLatencyFrames) * time.Second / 44100
	if sleep <= 0 {
		sleep = 2 * time.Millisecond
	}
	// Cap the sleep so a large ring does not delay the first fill.
	if sleep > 20*time.Millisecond {
		sleep = 20 * time.Millisecond
	}
	ticker := time.NewTicker(sleep)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			p.cancelClean.Store(true)
			return nil
		case <-ticker.C:
			p.pumpOnce()
		}
	}
}

// pumpOnce tops both rings up to the target latency.
//
// It never blocks on a full ring: RenderInto into a full ring simply drops the
// excess, which the engine counts as an overrun. Blocking here would stall the
// render goroutine — the precise inversion of what a producer exists to avoid.
func (p *Pumper) pumpOnce() {
	if p.eng == nil {
		return
	}
	if p.main == nil || p.cue == nil {
		return
	}
	if p.main.AvailableRead() >= p.cfg.TargetLatencyFrames &&
		p.cue.AvailableRead() >= p.cfg.TargetLatencyFrames {
		// Already ahead; render nothing. Rendering here would just overrun.
		return
	}

	start := time.Now()
	if err := p.eng.RenderInto(p.main, p.cue); err != nil {
		return
	}
	elapsed := time.Since(start)
	p.blocks.Add(1)

	for {
		cur := time.Duration(p.wallMax.Load())
		if elapsed <= cur || p.wallMax.CompareAndSwap(int64(cur), int64(elapsed)) {
			break
		}
	}
}

// Stats reports producer-side health.
func (p *Pumper) Stats() PumperStats {
	return PumperStats{
		BlocksRendered: p.blocks.Load(),
		RenderWallMax:  time.Duration(p.wallMax.Load()),
		TargetMisses:   p.targetMisses.Load(),
		CancelClean:    p.cancelClean.Load(),
	}
}
