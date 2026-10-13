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
	index int
	// rate is the engine's negotiated sample rate. Positions are in source
	// frames, so every conversion to or from a duration divides by this rather
	// than by a package constant (spec §4.2: the code is rate-agnostic).
	rate    int
	track   *audio.Track
	pos     float64 // fractional source sample position
	playing bool

	pitchNorm float64
	resampler *dsp.Resampler

	eq *dsp.EQ
	// filterNorm is the filter knob as 0..1. SetCutoffNormalized consumes the
	// position and the filter then reports Hz, so the position is kept here for
	// the interface to draw a fader from.
	filterNorm float64
	filter     *dsp.Filter

	// Preallocated scratch buffers. Render runs on the render goroutine and
	// must not allocate (spec §4.3), so everything it touches lives here.
	inBuf  dsp.Stereo
	outBuf dsp.Stereo

	// settleResampler renders the short pre-roll used to prime the EQ and
	// filter at a seek, and primeBuf holds that pre-roll. Preallocated so
	// seeking does not allocate.
	settleResampler *dsp.Resampler
	primeBuf        dsp.Stereo

	// beatgrid is optional. A deck with no grid still plays: beat-dependent
	// features report analyze.ErrNoBeatGrid and are disabled, while playback,
	// pitch, EQ, and filters keep working (spec §8).
	beatgrid *analyze.BeatGrid
	// peaks caches the waveform envelope, computed once per loaded track.
	peaks peaksCache
	// key is the optional detected key. Below analyze.KeyConfidenceThreshold it
	// carries an empty Camelot, so the view renders "unknown" rather than a
	// guess it would be believed on.
	key    *analyze.Key
	loopOn bool

	// syncedTo is the deck this one is tempo-locked to, or nil. Written and
	// read on the render goroutine like the rest of the deck's state.
	syncedTo *Deck

	// loop is the active loop, in source frames. A wrap is a seek.
	loop      loopCore
	savedLoop map[int]SavedLoop
	rollStack []rollFrame

	// rateRatio converts the track's sample rate to the deck's. 1.0 when they
	// match; set in Load.
	rateRatio float64

	// hotcues holds exact fractional frame positions, parallel to pos, so a
	// hotcue jump lands with the same precision as any other seek.
	hotcues   [numHotcues]float64
	hotcueSet [numHotcues]bool

	// pending carries control changes from Apply to the render goroutine. See
	// the type comment: a Deck has exactly one writer (Render) and one reader
	// (Apply) of audio state, which is what makes the audio path lock-free.
	pending commandQueue
}

// NewDeck returns a stopped, empty deck at the default sample rate.
func NewDeck(index int) *Deck { return NewDeckAtRate(index, dsp.SampleRate) }

// NewDeckAtRate returns a stopped, empty deck rendering at rate.
func NewDeckAtRate(index, rate int) *Deck {
	if rate <= 0 {
		rate = dsp.SampleRate
	}
	d := &Deck{
		rate:  rate,
		index: index,
		// Initialised eagerly: SaveLoop and PushRoll must never write to a nil
		// map or slice, both of which panic rather than fail cleanly.
		savedLoop:       make(map[int]SavedLoop),
		rollStack:       make([]rollFrame, 0, 8),
		resampler:       dsp.NewResampler(rate, rate),
		eq:              dsp.NewEQ(rate),
		filter:          dsp.NewFilter(rate),
		settleResampler: dsp.NewResampler(rate, rate),
		rateRatio:       1,
		primeBuf:        make(dsp.Stereo, dsp.BiquadHistorySamples*2),
		inBuf:           make(dsp.Stereo, BlockFrames*2),
		outBuf:          make(dsp.Stereo, BlockFrames*2),
	}
	return d
}

// Load replaces the deck's audio and rewinds to the start.
//
// It applies immediately, so it is only safe BEFORE the render goroutine starts
// (or from the render goroutine itself). To replace a track while audio is
// running, use LoadWith, which posts the mutation through the command queue --
// Load touches the resampler, the filter and the playhead, all of which the
// pump reads concurrently.
func (d *Deck) Load(t *audio.Track) {
	if t == nil {
		return
	}
	d.loadNow(t)
}

// LoadWith replaces the deck's audio along with its analysis, as one unit, so
// a deck never briefly holds new audio with the previous track's tempo.
func (d *Deck) LoadWith(t *audio.Track, grid *analyze.BeatGrid, key *analyze.Key) {
	d.post(command{kind: cmdLoad, track: t, beatgrid: grid, key: key})
}

// LoadNow replaces the deck's audio synchronously.
//
// It is only safe before the render goroutine starts, which is the startup
// path. Once rendering has begun, use Load: mutating here would race the
// resampler and the filter.
func (d *Deck) LoadNow(t *audio.Track, grid *analyze.BeatGrid, key *analyze.Key) {
	if t == nil {
		return
	}
	d.beatgrid, d.key = nil, nil
	if grid != nil && grid.BPM > 0 {
		d.beatgrid = grid
	}
	d.key = key
	d.loadNow(t)
}

// applyLoad is the render-side half of Load, run on the render goroutine.
func (d *Deck) applyLoad(t *audio.Track, grid *analyze.BeatGrid, key *analyze.Key) {
	if t == nil {
		return
	}
	// A cached grid measured against the previous track's length would place
	// beats wrongly, so a load without a grid clears one.
	if grid != nil && grid.BPM > 0 {
		d.beatgrid = grid
	} else {
		d.beatgrid = nil
	}
	d.key = key
	d.loadNow(t)
}

// loadNow does the actual mutation. It runs on the render goroutine only.
func (d *Deck) loadNow(t *audio.Track) {
	d.track = t
	d.pos = 0
	d.playing = false
	// The cached envelope belonged to the previous track.
	d.peaks.invalidate()
	// A track may be decoded at a different rate than the deck renders at —
	// a 48k file on a 44.1k deck, or the reverse. The resampler converts
	// between them so the deck's output rate always matches its declaration.
	// Without this the audio plays at the source rate's speed while the stream
	// declares the deck's, which is 9% fast and a semitone sharp at 48k.
	// SetRatio is INPUT frames consumed per OUTPUT frame produced, so
	// producing outRate frames per second from a source at inRate needs
	// inRate/outRate. Getting this backwards plays the track fast instead of
	// converting it.
	d.rateRatio = 1
	if t != nil && t.SampleRate > 0 {
		d.rateRatio = float64(t.SampleRate) / float64(d.rate)
	}
	d.applyResamplerRatio()
	d.eq.Reset()
	d.filter.Reset()
	d.warmPeaks()
}

// warmPeaks computes the waveform envelope for the track that was just loaded.
//
// This is the eager half of the peaks cache. Computing it lazily left the cost
// on whoever asked first -- the TUI goroutine inside Snapshot, which holds the
// engine lock, so a load stalled the render goroutine for the full 33ms scan of
// a five-minute track. Here it is paid once, on the render goroutine, at a
// moment when the deck is not yet playing.
func (d *Deck) warmPeaks() {
	if d.track == nil {
		return
	}
	d.peaks.set(d.track.Path, computePeaks(d.PCM(), peaksWidth))
}

// Index returns the deck's index.
func (d *Deck) Index() int { return d.index }

// SampleRate returns the rate this deck renders at.
func (d *Deck) SampleRate() int { return d.rate }

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
	return time.Duration(d.pos / float64(d.rate) * float64(time.Second))
}

// PosFrames returns the playhead as an exact fractional sample frame, which
// is the precision-true form of Pos and the unit seeks use internally.
func (d *Deck) PosFrames() float64 { return d.pos }

// AttachBeatGrid installs an analysed grid. A nil grid leaves the deck
// playable with beatgrid-dependent features unavailable (spec 8).
//
// Analysis arrives long after the track loaded -- it runs in the background
// over the whole file -- so this is called while the pump is rendering. It
// posts through the command queue for the same reason Load does: writing
// d.beatgrid directly raced both the render path and Snapshot.
func (d *Deck) AttachBeatGrid(g *analyze.BeatGrid) { d.post(command{kind: cmdAnalysis, grid: g}) }

// AttachKey stores a detected key for display. The view reads it through the
// Snapshot, never from the Deck directly.
func (d *Deck) AttachKey(k *analyze.Key) { d.post(command{kind: cmdAnalysis, key: k}) }

// AttachAnalysisNow installs a grid and key immediately rather than queueing.
//
// It is only safe before the render goroutine starts, or from that goroutine.
// It exists for the same reason LoadNow does: tests and the startup path need
// a deck populated synchronously, and forcing them all through the queue would
// mean forcing them all to drain a render loop they do not have.
func (d *Deck) AttachAnalysisNow(grid *analyze.BeatGrid, key *analyze.Key) {
	d.applyAnalysis(grid, key)
}

// applyAnalysis is the render-side half of AttachBeatGrid/AttachKey. The grid
// and key arrive together so a deck never shows a BPM without its beats.
func (d *Deck) applyAnalysis(grid *analyze.BeatGrid, key *analyze.Key) {
	// Both fields are assigned unconditionally, so a nil result clears the
	// previous one. Re-analysis that finds nothing must not leave the deck
	// claiming a grid belonging to the track it just replaced.
	d.beatgrid = grid
	d.key = key
}

// Key returns the detected key, or nil.
func (d *Deck) Key() *analyze.Key { return d.key }

// PCM returns the loaded audio. The slice is immutable once loaded, so a
// Snapshot can share it with a view without copying megabytes every frame.
//
// It takes no lock: the Deck's state is owned by the render goroutine and
// Snapshot() is called under the engine mutex, which is already the
// serialisation point for a consistent read.
func (d *Deck) PCM() dsp.Stereo {
	if d.track == nil {
		return nil // an empty deck has no audio, and Snapshot must not panic
	}
	return d.track.PCM
}

// Peaks returns the waveform envelope for the loaded track.
//
// The envelope is cached and warmed at load time, so this never scans: the scan
// costs ~33ms for a five-minute track, and this runs from Snapshot on a
// goroutine holding the engine mutex, thirty times a second. An empty envelope
// is returned when nothing is cached, which renders as a flat line.
func (d *Deck) Peaks() (Peaks, int) {
	path := ""
	if d.track != nil {
		path = d.track.Path
	}
	if p, ok := d.peaks.peek(path); ok {
		return p, peaksWidth
	}
	return Peaks{Min: make([]float32, peaksWidth*2), Max: make([]float32, peaksWidth*2)}, peaksWidth
}

// BeatGrid returns the deck's grid, or nil when it has none.
func (d *Deck) BeatGrid() *analyze.BeatGrid { return d.beatgrid }

// beatGridUsable is the confidence below which a detected beatgrid is treated
// as unusable.
//
// internal/analyze/beatgrid.go says a grid below roughly 0.5 "should be treated
// as unusable, and beat-dependent features disabled", and nothing enforced it:
// HasBeatGrid only tested Confidence > 0, so a 0.2 grid turned on sync,
// quantise, beat loops, zoom and the delta-beat readout. A weak grid is worse
// than none, because it makes those controls move without being right -- and
// unlike a missing grid, a wrong one gives the user no reason to distrust it.
//
// Spec 8 asks for exactly this: analysis that fails *or is low-confidence*
// leaves the deck playing with beat features disabled.
const beatGridUsable = 0.5

// HasBeatGrid reports whether beat-dependent features are available. Tasks 8
// and 9 gate loops, quantize, and sync on this rather than on a nil check at
// each call site.
func (d *Deck) HasBeatGrid() bool {
	return d.beatgrid != nil && d.beatgrid.Confidence >= beatGridUsable
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

// TogglePlay pauses a rolling deck in place and resumes a stopped or paused one.
//
// It is the play/pause button. The engine had no pause at all: Play() sets
// playing = true unconditionally, so the key that the help screen called
// "play/pause" could start a deck and never stop one, and the transport row
// simply stopped changing.
func (d *Deck) TogglePlay() { d.post(command{kind: cmdPlayToggle}) }

// togglePlay applies a play/pause command on the render goroutine.
//
// Pausing keeps the playhead, so resuming continues from where the deck stopped
// rather than from the top. That is the whole difference from a stop-and-return.
func (d *Deck) togglePlay() {
	if d.track == nil {
		return
	}
	d.playing = !d.playing
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
	d.applyResamplerRatio()
}

// applyResamplerRatio sets the resampler to the product of the rate conversion
// and the pitch fader, so moving one never discards the other.
func (d *Deck) applyResamplerRatio() {
	ratio := d.rateRatio
	if ratio <= 0 {
		ratio = 1
	}
	d.resampler.SetRatio(ratio * (1 + d.pitchNorm*PitchRange))
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
	return time.Duration(d.hotcues[slot] / float64(d.rate) * float64(time.Second)), true
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
	return float64(t) / float64(time.Second) * float64(d.rate)
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

	// Settle the EQ and filter rather than zeroing them.
	//
	// Their input is the RESAMPLER'S OUTPUT, not the raw track, so they must
	// be primed from the same signal they will actually see. Priming from raw
	// PCM leaves the delay line holding the wrong history, which shows up as a
	// ringing transient at the loop point — an audible click on every wrap.
	d.primeFilters(int(frame))
}

// Seek is the exported entry point for moving the playhead. It is not an
// Action: seeking is engine-internal (loops and sync call seek directly), and
// exposing it as a user action would bypass the modifier and mapping layers.
func (d *Deck) Seek(target time.Duration) { d.seek(target) }

// NudgeSeconds moves the playhead by a number of seconds.
//
// It is the time-domain counterpart to NudgeBeats: a jog or pitch-bend wheel
// sends a fraction of a second per tick, and has no grid to be musical about.
func (d *Deck) NudgeSeconds(sec float64) {
	d.post(command{kind: cmdNudge, value: sec})
}

// nudgeSeconds applies a time nudge on the render goroutine.
func (d *Deck) nudgeSeconds(sec float64) {
	if d.track == nil || math.IsNaN(sec) || sec == 0 {
		return
	}
	d.seekFrames(d.pos + sec*float64(d.rate))
}

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
	// Drain posted commands FIRST, before any early return below.
	//
	// This must happen even for a deck that has no track, which is precisely
	// the state a library load starts from. A load posts cmdLoad, and this
	// function is the only thing that applies it -- so with the old ordering
	// the deck returned at the d.track == nil guard above, never drained, and
	// the load sat unread forever. ENTER on an empty deck did nothing, with no
	// error and no visible state change. The command line path never hit this
	// because it loads with LoadNow before rendering starts.
	d.drainCommands()

	if d.track == nil || len(out) == 0 {
		for i := range out {
			out[i] = 0
		}
		return 0
	}

	// A stopped deck must still process its queue: otherwise the play command
	// that would start it sits unread behind the early return, and the deck
	// can never start.
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

	// Stop the block at the loop end.
	//
	// renderBlock would otherwise render a full block past the loop point and
	// wrap only afterwards, discarding the overshoot. That truncation is an
	// audible seam: the tail of the last block plays, then playback restarts
	// from the loop's beginning, and the two do not line up. Shortening the
	// block puts the wrap exactly on the loop boundary.
	//
	// The remaining length is rounded UP. Truncating would round a 0.5-frame
	// remainder to zero, which skips the shortening entirely and lets the
	// block run past the loop end.
	if d.loop.active && d.loop.end > d.loop.start {
		if remaining := d.loop.end - d.pos; remaining > 0 {
			limit := int(math.Ceil(remaining))
			if limit < want {
				want = limit
				out = out[:want*2]
			}
		}
	}

	return d.renderBlock(out)
}

// renderBlock produces exactly one block. out must be no longer than
// BlockFrames.
func (d *Deck) renderBlock(out dsp.Stereo) int {
	// Apply a wrap left pending by the previous block, BEFORE reading source.
	//
	// The wrap cannot happen after the resampler: it primes the resampler and
	// the filters for the destination, and the EQ/filter stage below would
	// then run over this block's pre-wrap audio, overwriting the very state the
	// wrap just established. Doing it here means every block is read,
	// filtered, and left consistent, and the next block starts already
	// rewound (spec §4.4.1).
	d.wrapLoop()

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
	// The playhead advances by the SAME combined ratio the resampler runs at.
	// ProcessFrom is given an absolute source position; if d.pos advanced by
	// pitch alone while the resampler consumed rate*source frames per output
	// frame, the two desynchronise and the pitch drifts. This is why a 48k
	// deck playing a 44.1k source rendered the tone at 480Hz.
	d.pos += float64(n) * d.rateRatio * d.PitchRatio()

	// EQ then filter, in the order a hardware signal chain runs.
	d.eq.Process(out, d.outBuf[:len(out)])
	copy(out, d.outBuf[:len(out)])
	d.filter.Process(out, d.outBuf[:len(out)])
	copy(out, d.outBuf[:len(out)])

	// Stop at the end rather than running off the buffer. A deck inside an
	// active loop must not stop here even at the track's end: wrapLoop on the
	// next block will bring it back (spec §4.4.1).
	total := float64(d.track.PCM.Frames())
	if d.pos >= total {
		d.pos = total
		if !d.loop.active {
			d.playing = false
		}
	}
	return n
}

// primeFilters settles the EQ and filter delay lines from the signal the deck
// will actually present at frame.
//
// The filters sit AFTER the resampler, so their input is resampled audio.
// Their history must therefore be produced by the resampler rather than taken
// from the raw track: a filter primed with pre-resampling samples converges to
// the wrong state and rings audibly at the seam.
//
// scratch buffers are used, so this must run on the render goroutine and must
// not be called concurrently with renderBlock.
func (d *Deck) primeFilters(frame int) {
	if d.track == nil || frame < 1 {
		d.eq.Reset()
		d.filter.Reset()
		return
	}

	// Produce enough resampler output to settle the delay lines. The filter's
	// own history is two frames, but a resonant filter needs more input than
	// that to approach its steady state, so feed generously.
	n := dsp.BiquadHistorySamples
	if frame < n {
		n = frame
	}
	hist := d.primeBuf[:n*2]
	for i := range hist {
		hist[i] = 0
	}

	// Render n frames of resampler output ending at `frame` using a scratch
	// resampler, so the deck's own state (already primed at frame) is
	// untouched. The scratch resampler is allocated once in NewDeck: seeking
	// must not allocate (spec §4.3).
	d.settleResampler.SetRatio(d.rateRatio * d.PitchRatio())
	d.settleResampler.Prime(d.track.PCM, frame-n, d.settleResampler.HistorySamples())
	if got := d.settleResampler.ProcessFrom(d.track.PCM, frame-n, hist); got != n {
		for i := got * 2; i < len(hist); i++ {
			hist[i] = 0
		}
	}

	d.eq.Prime(hist)
	d.filter.Prime(hist)
}

// IsSynced reports whether this deck is currently tempo-locked to another.
//
// This exists so the interface can show sync state. syncedTo is private and
// nothing on Deck reported it, so pressing the sync key changed the engine
// with no way for the UI to say so -- and no way for the user to tell a
// successful lock from a failed one.
func (d *Deck) IsSynced() bool { return d != nil && d.syncedTo != nil }

// EQBand returns band n's gain as a LINEAR value (1.0 is flat), read on the
// render goroutine.
func (d *Deck) EQBand(band int) float64 {
	if d.eq == nil || band < 0 || band > 2 {
		return 1
	}
	return d.eq.Gain(band)
}

// EQPosition returns band n's knob position as -1..1, which is what a fader
// wants. See eqPosition for why this is not EQBand's value.
func (d *Deck) EQPosition(band int) float64 {
	if d.eq == nil || band < 0 || band > 2 {
		return 0
	}
	return d.eqPosition(band)
}

// FilterCutoffNorm reports the filter knob as 0..1 of its travel.
//
// FilterCutoff reports Hz, which is not something to draw on a fader. The
// knob's own 0..1 position is what the interface needs.
func (d *Deck) FilterCutoffNorm() float64 {
	return d.filterNorm
}

// eqPosition converts a band's linear gain back to its -1..1 knob position,
// the inverse of gainFromNormalized.
//
// EQ.Gain reports a LINEAR gain (1.0 is flat), which is the wrong shape to draw
// on a fader and the wrong shape to compare against a knob: mapping 1.0 to the
// top of the meter meant a boosted band and a flat one both pinned the bar
// full, so no EQ movement was ever visible.
func (d *Deck) eqPosition(band int) float64 {
	g := d.EQBand(band)
	if g <= 0 {
		g = 0.01
	}
	// gain = 1 + v*3  =>  v = (gain - 1) / 3
	v := (g - 1) / 3
	if v < -1 {
		v = -1
	}
	if v > 1 {
		v = 1
	}
	return v
}
