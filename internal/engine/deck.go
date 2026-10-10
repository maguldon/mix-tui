// Package engine owns all playback state and is the only thing that touches
// audio.
//
// The engine imports nothing from the device, MIDI, or terminal layers: it
// takes PCM in and emits PCM out. That is what makes it testable with no sound
// card and no terminal, and what lets the same code path play to a speaker or
// render to a file.
package engine

import (
	"math"
	"time"

	"github.com/maguldon/mix-tui/internal/analyze"
	"github.com/maguldon/mix-tui/internal/audio"
	"github.com/maguldon/mix-tui/internal/dsp"
)

// PitchRange is the pitch fader's range. A normalized pitch of 1.0 is +8%,
// matching a typical DJ deck's ±8% pitch fader (spec §2.1).
const PitchRange = 0.08

// BlockFrames is the render block size. 512 frames is about 11.6ms at 44.1kHz,
// comfortably above the latency a listener perceives as glitchy.
const BlockFrames = 512

// ActionKind and Action are defined in action.go; see that file for the full
// list of controls and the configuration spellings.

// Deck is one playback head with its own pitch, EQ, and filter.
type Deck struct {
	index   int
	track   *audio.Track
	pos     float64 // fractional source sample position
	playing bool

	pitchNorm float64
	resampler *dsp.Resampler

	eq     *dsp.EQ
	filter *dsp.Filter

	// Preallocated scratch buffers. Render runs on the render goroutine and
	// must not allocate (spec §4.3), so everything it touches lives here.
	inBuf  dsp.Stereo
	outBuf dsp.Stereo

	// beatgrid is optional. A deck with no grid still plays: beat-dependent
	// features report analyze.ErrNoBeatGrid and are disabled, while playback,
	// pitch, EQ, and filters keep working (spec §8).
	beatgrid *analyze.BeatGrid
	loopOn   bool

	// hotcues holds exact fractional frame positions, parallel to pos, so a
	// hotcue jump lands with the same precision as any other seek.
	hotcues   [numHotcues]float64
	hotcueSet [numHotcues]bool

	// pending carries control changes from Apply to the render goroutine. See
	// the type comment: a Deck has exactly one writer (Render) and one reader
	// (Apply) of audio state, which is what makes the audio path lock-free.
	pending commandQueue
}

// NewDeck returns a stopped, empty deck.
func NewDeck(index int) *Deck {
	d := &Deck{
		index:     index,
		resampler: dsp.NewResampler(dsp.SampleRate, dsp.SampleRate),
		eq:        dsp.NewEQ(),
		filter:    dsp.NewFilter(),
		inBuf:     make(dsp.Stereo, BlockFrames*2),
		outBuf:    make(dsp.Stereo, BlockFrames*2),
	}
	return d
}

// Load installs a track and rewinds to the start.
func (d *Deck) Load(t *audio.Track) {
	d.track = t
	d.pos = 0
	d.playing = false
	d.resampler.Reset()
	d.eq.Reset()
	d.filter.Reset()
}

// Index returns the deck's index.
func (d *Deck) Index() int { return d.index }

// HasTrack reports whether a track is loaded.
func (d *Deck) HasTrack() bool { return d.track != nil }

// IsPlaying reports whether the deck is rolling.
func (d *Deck) IsPlaying() bool { return d.playing }

// Duration returns the loaded track's length, or 0 when empty.
func (d *Deck) Duration() time.Duration {
	if d.track == nil {
		return 0
	}
	return d.track.Duration
}

// Pos returns the current playhead position.
func (d *Deck) Pos() time.Duration {
	return time.Duration(d.pos / float64(dsp.SampleRate) * float64(time.Second))
}

// PosFrames returns the playhead as an exact fractional sample frame, which
// is the precision-true form of Pos and the unit seeks use internally.
func (d *Deck) PosFrames() float64 { return d.pos }

// AttachBeatGrid installs an analysed grid. A nil grid leaves the deck
// playable with beatgrid-dependent features unavailable (spec §8).
func (d *Deck) AttachBeatGrid(g *analyze.BeatGrid) { d.beatgrid = g }

// BeatGrid returns the deck's grid, or nil when it has none.
func (d *Deck) BeatGrid() *analyze.BeatGrid { return d.beatgrid }

// HasBeatGrid reports whether beat-dependent features are available. Tasks 8
// and 9 gate loops, quantize, and sync on this rather than on a nil check at
// each call site.
func (d *Deck) HasBeatGrid() bool {
	return d.beatgrid != nil && d.beatgrid.Confidence > 0
}

// Play starts or resumes the deck.
func (d *Deck) Play() {
	d.post(command{kind: cmdPlay})
}

// play applies a play command on the render goroutine.
func (d *Deck) play() {
	if d.track == nil {
		return
	}
	d.playing = true
}

// Stop halts the deck, leaving the playhead where it is.
func (d *Deck) Stop() { d.post(command{kind: cmdStop}) }

// stop applies a stop command on the render goroutine.
func (d *Deck) stop() { d.playing = false }

// SetPitch sets the pitch fader from a normalized value in [-1, 1], where
// 1.0 is +PitchRange. Out-of-range and NaN inputs are clamped, because a MIDI
// knob or a config file can send anything and a deck running at 3x speed is
// not a defensible failure.
func (d *Deck) SetPitch(normalized float64) {
	d.post(command{kind: cmdPitch, value: normalized})
}

// setPitch applies a pitch change on the render goroutine.
func (d *Deck) setPitch(normalized float64) {
	d.pitchNorm = dsp.ClampNormalized(normalized)
	d.resampler.SetRatio(1 + d.pitchNorm*PitchRange)
}

// SetPitchNormalized is an alias for SetPitch, named for call sites that
// already hold a normalized ratio (sync, in a later task).
func (d *Deck) SetPitchNormalized(normalized float64) { d.SetPitch(normalized) }

// Pitch returns the normalized pitch fader position.
func (d *Deck) Pitch() float64 { return d.pitchNorm }

// PitchRatio returns the resampling ratio the pitch fader implies.
func (d *Deck) PitchRatio() float64 { return 1 + d.pitchNorm*PitchRange }

// RatioToNormalized inverts SetPitch: ratio 1.0 -> 0.0, 1.08 -> 1.0.
func RatioToNormalized(ratio float64) float64 {
	if ratio <= 0 || math.IsNaN(ratio) {
		return 0
	}
	return (ratio - 1) / PitchRange
}

// SetEQBand sets one EQ band's linear gain. Band: 0=low 1=mid 2=high.
// SetEQBand sets one EQ band's linear gain. Band: 0=low 1=mid 2=high.
//
// It posts rather than mutating: the render goroutine owns the filter state.
func (d *Deck) SetEQBand(band int, gain float64) {
	switch band {
	case 0:
		d.post(command{kind: cmdEQLow, value: gain})
	case 1:
		d.post(command{kind: cmdEQMid, value: gain})
	case 2:
		d.post(command{kind: cmdEQHigh, value: gain})
	}
}

// SetFilter sets the DJ filter. cutoffHz is clamped to a usable range.
func (d *Deck) SetFilter(highpass bool, cutoffHz, resonance float64) {
	if highpass {
		d.SetFilterKind(true)
	} else {
		d.SetFilterKind(false)
	}
	d.filter.SetParams(cutoffHz, resonance)
}

// FilterHighpass reports whether the filter is sweeping highs (true) or lows.
func (d *Deck) FilterHighpass() bool { return d.filter.IsHighpass() }

// SetFilterKind selects highpass or lowpass.
func (d *Deck) SetFilterKind(highpass bool) {
	v := -1.0
	if highpass {
		v = 1
	}
	d.post(command{kind: cmdFilterKind, value: v})
}

// SetFilterResonance sets filter resonance from a normalized 0..1 value.
func (d *Deck) SetFilterResonance(normalized float64) {
	d.post(command{kind: cmdFilterReso, value: normalized})
}

// SetFilterCutoff sets the cutoff from a normalized 0..1 knob position.
func (d *Deck) SetFilterCutoff(normalized float64) {
	d.post(command{kind: cmdFilterCutoff, value: normalized})
}

// FilterCutoff returns the current cutoff in Hz.
func (d *Deck) FilterCutoff() float64 { return d.filter.Cutoff() }

// Track returns the loaded track, or nil.
func (d *Deck) Track() *audio.Track { return d.track }

// SetHotcue stores the current playhead position in a slot.
//
// Assigning an occupied slot overwrites it: on a real deck, pressing the
// assign button on a lit cue re-cues it rather than doing nothing.
func (d *Deck) SetHotcue(slot int) {
	d.post(command{kind: cmdHotcueSet, slot: slot})
}

// setHotcue applies a hotcue assignment on the render goroutine.
func (d *Deck) setHotcue(slot int) {
	if slot < 0 || slot >= numHotcues {
		return
	}
	d.hotcues[slot] = d.pos
	d.hotcueSet[slot] = true
}

// JumpToHotcue seeks to a hotcue. An unassigned slot is a no-op: hitting a
// cue that was never set must not move the playhead somewhere arbitrary.
func (d *Deck) JumpToHotcue(slot int) {
	d.post(command{kind: cmdHotcueJump, slot: slot})
}

// jumpToHotcue applies a hotcue jump on the render goroutine.
func (d *Deck) jumpToHotcue(slot int) {
	if slot < 0 || slot >= numHotcues || !d.hotcueSet[slot] {
		return
	}
	d.seekFrames(d.hotcues[slot])
	d.playing = true
}

// DeleteHotcue clears a slot.
func (d *Deck) DeleteHotcue(slot int) {
	d.post(command{kind: cmdHotcueDelete, slot: slot})
}

// deleteHotcue clears a slot on the render goroutine.
func (d *Deck) deleteHotcue(slot int) {
	if slot < 0 || slot >= numHotcues {
		return
	}
	d.hotcues[slot] = 0
	d.hotcueSet[slot] = false
}

// Hotcue reports whether a slot holds a cue and where it points.
func (d *Deck) Hotcue(slot int) (time.Duration, bool) {
	if slot < 0 || slot >= numHotcues || !d.hotcueSet[slot] {
		return 0, false
	}
	return time.Duration(d.hotcues[slot] / float64(dsp.SampleRate) * float64(time.Second)), true
}

// numHotcues is the number of hotcue slots per deck, matching a CDJ.
const numHotcues = 8

// seek moves the playhead with correct resampler and filter state at the
// destination.
//
// This is THE primitive. Hotcues, nudges, sync corrections, and loop wraps all
// call it, which is why they are all seamless: getting the resampler's filter
// state right at a discontinuity is the whole problem, and there is exactly one
// place that solves it (spec §4.4.1).
func (d *Deck) seek(target time.Duration) {
	if d.track == nil {
		return
	}
	total := d.Duration()
	if target < 0 {
		target = 0
	}
	if target > total {
		target = total
	}
	// Convert through an exact frame count. Going via float64 seconds loses
	// precision at long positions: a 1-frame error at 440Hz shifts the sine's
	// phase enough to look like a completely different waveform.
	d.seekFrames(d.durationToFrames(target))
}

// durationToFrames converts a position to a whole sample frame.
func (d *Deck) durationToFrames(t time.Duration) float64 {
	return float64(t) / float64(time.Second) * float64(dsp.SampleRate)
}

// seekFrames moves the playhead to an exact fractional sample frame. This is
// the precision-true seek: positions are frames, and durations are only a
// display convenience.
func (d *Deck) seekFrames(frame float64) {
	if d.track == nil {
		return
	}
	total := float64(d.track.PCM.Frames())
	if frame < 0 {
		frame = 0
	}
	if frame > total {
		frame = total
	}
	d.pos = frame

	// Prime the resampler from the destination's own history. This is the
	// line that makes seeks, hotcues, and loop wraps silent.
	d.resampler.Prime(d.track.PCM, int(frame), d.resampler.HistorySamples())

	// Filters carry a two-sample delay line whose discontinuity is filtered
	// away rather than clicked, so resetting is sufficient and cheaper than
	// priming.
	d.eq.Reset()
	d.filter.Reset()
}

// Seek is the exported entry point for moving the playhead. It is not an
// Action: seeking is engine-internal (loops and sync call seek directly), and
// exposing it as a user action would bypass the modifier and mapping layers.
func (d *Deck) Seek(target time.Duration) { d.seek(target) }

// SeekNormalized moves the playhead to a normalized 0..1 position of the
// track.
//
// Values outside 0..1 and NaN are clamped rather than wrapping: a scrub past
// the end of a track should land at the end, not at an undefined frame.
func (d *Deck) SeekNormalized(normalized float64) {
	d.post(command{kind: cmdSeekNorm, value: normalized})
}

// seekNormalized applies a normalized seek on the render goroutine.
func (d *Deck) seekNormalized(normalized float64) {
	if d.track == nil || math.IsNaN(normalized) {
		return
	}
	if normalized < 0 {
		normalized = 0
	}
	if normalized > 1 {
		normalized = 1
	}
	d.seek(time.Duration(float64(d.Duration()) * normalized))
}

// Render produces audio into out, returning the number of stereo frames
// actually produced.
//
// It may produce fewer than out holds. It must not allocate, block, or take a
// lock: it runs on the render goroutine every block, and any allocation here is
// a potential dropout (spec §4.3). Callers must honour the return value —
// treating the unwritten remainder as audio inserts a discontinuity
// indistinguishable from a seek seam.
func (d *Deck) Render(out dsp.Stereo) int {
	if d.track == nil || len(out) == 0 {
		for i := range out {
			out[i] = 0
		}
		return 0
	}

	// Drain posted commands BEFORE the playing check. A stopped deck must
	// still process its queue: otherwise the play command that would start it
	// sits unread behind the early return, and the deck can never start.
	d.drainCommands()

	if !d.playing {
		for i := range out {
			out[i] = 0
		}
		return 0
	}

	want := out.Frames()
	if want > BlockFrames {
		// The scratch buffers are sized for one block. A longer request is
		// rendered a block at a time rather than reallocating.
		produced := 0
		for produced < want {
			chunk := out[produced*2:]
			if chunk.Frames() > BlockFrames {
				chunk = chunk[:BlockFrames*2]
			}
			n := d.renderBlock(chunk)
			if n == 0 {
				break
			}
			produced += n
		}
		return produced
	}
	return d.renderBlock(out)
}

// renderBlock produces exactly one block. out must be no longer than
// BlockFrames.
func (d *Deck) renderBlock(out dsp.Stereo) int {
	for i := range out {
		out[i] = 0
	}

	// Hand the resampler the source and the absolute position. ProcessFrom
	// reads only what this block needs, so cost is proportional to the block
	// rather than to the track length, and the resampler cannot drift out of
	// step with the deck's position.
	n := d.resampler.ProcessFrom(d.track.PCM, int(d.pos), out)
	if n == 0 {
		d.playing = false
		return 0
	}
	d.pos += float64(n) * d.PitchRatio()

	// EQ then filter, in the order a hardware signal chain runs.
	d.eq.Process(out, d.outBuf[:len(out)])
	copy(out, d.outBuf[:len(out)])
	d.filter.Process(out, d.outBuf[:len(out)])
	copy(out, d.outBuf[:len(out)])

	// Stop at the end rather than running off the buffer.
	total := float64(d.track.PCM.Frames())
	if d.pos >= total {
		d.pos = total
		d.playing = false
	}
	return n
}
