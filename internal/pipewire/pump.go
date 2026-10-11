//go:build linux

package pipewire

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/maguldon/mix-tui/internal/audio"
	"github.com/maguldon/mix-tui/internal/engine"
)

// engineBlockFrames is the render block the engine uses. The pump paces
// against it so its tick is one block of audio.
const engineBlockFrames = 512

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
	// Wake often, and fill to target on each wake. The tick is NOT the pacing
	// mechanism for the audio itself: a fixed sleep sized to the target
	// produced one block per wake, which is a fraction of what real time
	// consumes, and the pump silently fell behind. Waking at a block interval
	// and topping up whatever the rings are short by decouples pacing from
	// throughput.
	rate := p.rate()
	block := p.cfg.BlockFrames
	if block <= 0 {
		block = engineBlockFrames
	}
	tick := time.Duration(block) * time.Second / time.Duration(rate)
	if tick <= 0 {
		tick = time.Millisecond
	}
	// Do not spin when far ahead: a sleep longer than the target would let the
	// rings drain before the next wake.
	ticker := time.NewTicker(tick)
	defer ticker.Stop()

	// Fill once before the first tick so a run never opens with the drain
	// consuming an empty ring, which would seed the stats with false starves.
	p.pumpOnce()

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

// rate returns the sample rate the pump paces against.
func (p *Pumper) rate() int {
	if p.out != nil && p.out.cfg.SampleRate > 0 {
		return p.out.cfg.SampleRate
	}
	return 44100
}

// pumpOnce tops both rings up to the target latency.
//
// It renders REPEATEDLY until the rings reach target, which is what makes the
// pump track real time. Rendering a single block per wake is the bug this
// replaced: one block is 512 frames while a wake covers a whole tick's worth of
// consumption, so the pump delivered a fraction of what the device demanded and
// the rings drained.
//
// It never blocks on a full ring: RenderInto into a full ring drops the excess,
// which the engine counts as an overrun. Blocking here would stall the render
// goroutine — the precise inversion of what a producer exists to prevent.
func (p *Pumper) pumpOnce() {
	if p.eng == nil || p.main == nil || p.cue == nil {
		return
	}
	target := p.cfg.TargetLatencyFrames
	if target <= 0 {
		return
	}

	// Bound the work per wake so a single pass cannot spin if the rings report
	// as short for some reason. At 200ms of target and 512-frame blocks this
	// permits ~17 blocks, far more than one tick needs.
	maxBlocks := target / p.blockFrames()
	if maxBlocks < 1 {
		maxBlocks = 1
	}

	for i := 0; i < maxBlocks; i++ {
		if p.main.AvailableRead() >= target && p.cue.AvailableRead() >= target {
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
	// Reached the loop bound without hitting target: the engine could not keep
	// the rings at the requested depth. That is a real producer-side condition
	// and is reported, rather than being indistinguishable from a slow drain.
	if p.main.AvailableRead() < target || p.cue.AvailableRead() < target {
		p.targetMisses.Add(1)
	}
}

func (p *Pumper) blockFrames() int {
	if p.cfg.BlockFrames > 0 {
		return p.cfg.BlockFrames
	}
	return engineBlockFrames
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
