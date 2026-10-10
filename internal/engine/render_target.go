package engine

import (
	"github.com/maguldon/mix-tui/internal/audio"
	"github.com/maguldon/mix-tui/internal/dsp"
)

// RenderTarget abstracts the realtime consumer of rendered audio, so the
// milestones that add a real device (PipeWire in milestone 2) can attach
// without the engine knowing anything about audio devices.
//
// THE CONTRACT, which milestone 2 depends on: Consume runs on the audio
// callback's own thread. It must not block, must not allocate, and must not
// take a lock. A Dropout is recoverable; stalling the audio thread is not
// (spec §4.3).
//
// Returning false means the target had nothing to give — an underrun — which
// the caller reports rather than papering over.
type RenderTarget interface {
	// Consume fills out with audio and returns false if it had none.
	Consume(out dsp.Stereo) bool
}

// ringTarget adapts an audio.Ring to RenderTarget.
type ringTarget struct{ r *audio.Ring }

// RingTarget returns a RenderTarget backed by a ring buffer, which is what the
// engine itself renders into.
func RingTarget(r *audio.Ring) RenderTarget { return ringTarget{r} }

func (t ringTarget) Consume(out dsp.Stereo) bool { return t.r.Read(out) > 0 }

// funcTarget adapts a plain callback, which is what tests use.
type funcTarget struct{ fn func(dsp.Stereo) }

// FuncTarget returns a RenderTarget that hands each block to fn. Useful for
// capturing rendered audio without a ring.
func FuncTarget(fn func(dsp.Stereo)) RenderTarget { return funcTarget{fn} }

func (t funcTarget) Consume(out dsp.Stereo) bool {
	t.fn(out)
	return true
}
