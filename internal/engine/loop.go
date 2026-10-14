package engine

import (
	"errors"
	"math"
	"time"

	"github.com/maguldon/mix-tui/internal/analyze"
)

// ErrNoBeatGrid is re-exported from analyze so callers of a Deck do not have
// to import the analysis package to check for it.
var ErrNoBeatGrid = analyze.ErrNoBeatGrid

// Loops are three features over one mechanism: a loop wrap is a seek
// (spec §4.4.1).
//
// Playing from posA to posB, wrapping, and a loop-roll all reduce to
// "advance the read position, with correct resampler and filter state at the
// destination". That is why every loop here routes through d.seek rather than
// assigning d.pos: skipping the priming step is exactly what produces an
// audible seam at the loop point, and the seam is the one thing a loop must
// never have.

// ErrNoLoopIn is returned when a loop-out is attempted with no loop-in, or a
// release with nothing to release. The playhead is never moved by a failed
// operation: a stray keypress must not be able to disturb playback.
var ErrNoLoopIn = errors.New("no loop in point set")

// ErrNoLoopSlot is returned when recalling or saving a slot that was never
// used.
var ErrNoLoopSlot = errors.New("no loop in that slot")

// loopCore is the active loop, in fractional source frames.
//
// Frames rather than time.Duration because the loop boundary must land on an
// exact sample: rounding to the nearest millisecond at a 128 BPM loop point
// is up to 11 samples of error, which is an audible stumble on every wrap.
type loopCore struct {
	active bool
	// rolled marks a loop created by loop-roll, which is undone by popping the
	// roll stack rather than by releasing.
	rolled bool
	start  float64
	end    float64
	// beatCount is 0 for a free loop set by hand.
	beatCount float64
}

// SavedLoop is a loop persisted in a slot, in source frames.
type SavedLoop struct {
	Start, End float64
}

// LoopState is a copy of the deck's loop state for the UI.
type LoopState struct {
	Active     bool
	Start      time.Duration
	End        time.Duration
	BeatCount  float64
	Rolled     bool
	SavedSlots map[int]SavedLoop
}

// rollFrame is one entry of the roll stack: the state a roll replaced.
type rollFrame struct {
	pos  float64
	loop loopCore
}

// Loop returns the deck's current loop state.
// LoopSummary is the loop state a view needs, without the saved-slot map.
//
// Loop() deep-copies that map, which allocates on every call — and Snapshot
// runs ~30 times a second, so using it there fed the GC continuously during a
// set. The map is only for callers that genuinely need the saved slots.
type LoopSummary struct {
	Active    bool
	Start     time.Duration
	End       time.Duration
	BeatCount float64
	Rolled    bool
}

// LoopSummary returns the allocation-free view of loop state.
func (d *Deck) LoopSummary() LoopSummary {
	return LoopSummary{
		Active:    d.loop.active,
		Start:     d.framesToDuration(d.loop.start),
		End:       d.framesToDuration(d.loop.end),
		BeatCount: d.loop.beatCount,
		Rolled:    d.loop.rolled,
	}
}

func (d *Deck) Loop() LoopState {
	slots := make(map[int]SavedLoop, len(d.savedLoop))
	for k, v := range d.savedLoop {
		slots[k] = v
	}
	return LoopState{
		Active:     d.loop.active,
		Start:      d.framesToDuration(d.loop.start),
		End:        d.framesToDuration(d.loop.end),
		BeatCount:  d.loop.beatCount,
		Rolled:     d.loop.rolled,
		SavedSlots: slots,
	}
}

func (d *Deck) framesToDuration(f float64) time.Duration {
	if d.track == nil {
		return 0
	}
	return time.Duration(f / float64(d.rate) * float64(time.Second))
}

// beatPeriodFrames returns the length of one beat in source frames.
//
// A nil or zero BPM yields 0, which callers must treat as "no grid": every
// beat-dependent operation checks it before use rather than dividing by it.
func (d *Deck) beatPeriodFrames() float64 {
	if d.beatgrid == nil || d.beatgrid.BPM <= 0 {
		return 0
	}
	return float64(d.rate) * 60 / d.beatgrid.BPM
}

// SetLoopBeat arms a loop of n beats starting at the next beat boundary.
//
// The length is computed in SOURCE frames from the grid's beat period, never
// from elapsed output time. That distinction is the whole reason a 4-beat
// loop stays 4 beats at every pitch setting: the pitch fader changes how fast
// source samples are consumed, not how many source samples a beat spans.
func (d *Deck) SetLoopBeat(n float64) error {
	if d.track == nil {
		return ErrNoBeatGrid
	}
	per := d.beatPeriodFrames()
	if per <= 0 {
		return ErrNoBeatGrid
	}
	if n <= 0 {
		d.loop = loopCore{}
		return nil
	}

	start := d.nextBeatBoundary()
	total := float64(d.track.PCM.Frames())
	end := start + per*n

	// Near the track end there is not room ahead for the requested length.
	// Rather than silently dropping the loop (the button appears to do
	// nothing), place it earlier so it fits and still ends on the grid. A user
	// setting a loop over the outro wants it to play, not to be refused.
	if end > total {
		end = total
		start = total - per*n
		if start < 0 {
			start = 0
		}
		// Snap the shifted start back onto the beat grid.
		start = math.Floor(start/per) * per
		if start+per*n > total {
			// The grid does not divide evenly into the remaining track; fall
			// back to a plain fit.
			start = total - per*n
			if start < 0 {
				start = 0
			}
		}
		end = start + per*n
	}

	// Snap both ends to whole frames.
	//
	// The resampler reads integer source positions, so a loop boundary at a
	// half frame can never be hit exactly: playback always lands half a sample
	// past the intended point, and that residue is an audible step at every
	// wrap. Snapping costs at most half a sample of loop-length accuracy and
	// removes the seam entirely.
	start = math.Round(start)
	end = math.Round(end)
	if end <= start {
		// Not enough room for even one beat. Leave the deck unlooped rather
		// than arming a zero-length loop that would spin the render loop.
		d.loop = loopCore{}
		return nil
	}

	d.loop = loopCore{active: true, start: start, end: end, beatCount: n}
	return nil
}

// nextBeatBoundary returns the source frame of the first beat at or after the
// current position.
//
// Beats are quantised to the grid rather than to the current position so a
// loop placed with the button is musically aligned.
func (d *Deck) nextBeatBoundary() float64 {
	pos := d.pos
	per := d.beatPeriodFrames()
	if per <= 0 {
		return pos
	}
	if d.beatgrid == nil || len(d.beatgrid.Beats) == 0 {
		return pos
	}
	for _, b := range d.beatgrid.Beats {
		f := d.durationToFrames(b)
		if f >= pos {
			return f
		}
	}

	// Past the last recorded beat. Snap to the grid's phase rather than
	// jumping forward by a whole beat: the beat list is a finite sample of an
	// ongoing pulse, so the grid's phase still applies past its end. Returning
	// pos+per here instead would start the loop a beat in the future, which
	// reads as the button doing nothing for one beat.
	last := d.durationToFrames(d.beatgrid.Beats[len(d.beatgrid.Beats)-1])
	if last <= 0 {
		return pos
	}
	phase := math.Mod(pos, per)
	// The next beat boundary strictly after pos, on the same grid.
	candidate := pos - phase + per
	if candidate <= last {
		return candidate
	}
	// Beyond even the extrapolated boundary: use the position as-is rather
	// than a frame the listener is already past.
	return pos
}

// SetLoopIn marks the loop start at the current position.
func (d *Deck) SetLoopIn() error {
	if d.track == nil {
		return ErrNoLoopIn
	}
	d.loop.start = d.pos
	d.loop.active = true
	return nil
}

// SetLoopOut marks the loop end at the current position and arms the loop.
//
// An out-point before the in-point is an error and leaves the loop untouched:
// silently swapping the two would produce a loop spanning the wrong part of
// the track with no indication anything was wrong.
func (d *Deck) SetLoopOut() error {
	if d.track == nil {
		return ErrNoLoopIn
	}
	if d.pos <= d.loop.start {
		return ErrNoLoopIn
	}
	end := d.pos
	if total := float64(d.track.PCM.Frames()); end > total {
		end = total
	}
	if end <= d.loop.start {
		return ErrNoLoopIn
	}
	d.loop.end = end
	d.loop.active = true
	d.loop.beatCount = 0 // a hand-placed loop is free, not quantised
	return nil
}

// SaveLoop persists the active loop into a slot.
func (d *Deck) SaveLoop(slot int) error {
	if !d.loop.active {
		return ErrNoLoopIn
	}
	if slot < 0 {
		return ErrNoLoopSlot
	}
	if d.savedLoop == nil {
		d.savedLoop = make(map[int]SavedLoop)
	}
	d.savedLoop[slot] = SavedLoop{Start: d.loop.start, End: d.loop.end}
	d.persistCues()
	return nil
}

// RecallLoop activates a previously saved loop.
func (d *Deck) RecallLoop(slot int) error {
	s, ok := d.savedLoop[slot]
	if !ok {
		return ErrNoLoopSlot
	}
	d.loop = loopCore{active: true, start: s.Start, end: s.End}
	return nil
}

// ReleaseLoop stops looping, leaving the playhead where it is so playback
// continues from the loop's end.
//
// The armed length survives. It used to be zeroed here, so after releasing a
// loop the deck had no loop size at all and the panel showed nothing until the
// encoder happened to arm another loop -- which is precisely the "encoder with
// no readout feels broken" failure spec 6.2.2 forbids. Turning it would change
// nothing on screen until it landed on a length the user was already using.
//
// A beatCount of zero still means "no length chosen", which is what a free
// loop set by hand is, so the readout can tell the two apart.
func (d *Deck) ReleaseLoop() {
	d.loop.active = false
	d.loop.rolled = false
}

// PushRoll starts a loop-roll of n beats.
//
// The roll is a temporary loop layered over whatever was playing. The prior
// position and loop are pushed so PopRoll can restore them exactly, which is
// what makes a roll usable mid-performance: hitting it and releasing returns
// you to precisely where you were.
func (d *Deck) PushRoll(beats float64) error {
	if d.track == nil {
		return ErrNoBeatGrid
	}
	per := d.beatPeriodFrames()
	if per <= 0 {
		return ErrNoBeatGrid
	}
	if beats <= 0 {
		beats = 1
	}

	// Push the state we are replacing.
	d.rollStack = append(d.rollStack, rollFrame{pos: d.pos, loop: d.loop})

	start := d.nextBeatBoundary()
	end := start + per*beats
	if total := float64(d.track.PCM.Frames()); end > total {
		end = total
	}
	if end <= start {
		// Not enough room to roll: undo the push so the stack does not grow
		// with frames that will never be popped.
		d.rollStack = d.rollStack[:len(d.rollStack)-1]
		return ErrNoLoopIn
	}

	d.loop = loopCore{active: true, rolled: true, start: start, end: end, beatCount: beats}
	d.seekFrames(start)
	return nil
}

// PopRoll ends the current roll and restores the previous state.
//
// On an empty stack it errors WITHOUT touching the playhead: a stray release
// must never be able to move playback.
func (d *Deck) PopRoll() error {
	if len(d.rollStack) == 0 {
		return ErrNoLoopIn
	}
	frame := d.rollStack[len(d.rollStack)-1]
	d.rollStack = d.rollStack[:len(d.rollStack)-1]
	d.loop = frame.loop
	// Restore through seek so resampler state is correct at the destination,
	// exactly as a loop wrap does.
	d.seekFrames(frame.pos)
	return nil
}

// wrapLoop returns the playhead to the loop start if it has passed the end.
//
// This is the whole feature: a wrap is a seek, not an assignment. Routing it
// through seekFrames primes the resampler from the destination's own history,
// which is why the wrap is inaudible (spec §4.4.1).
//
// It runs BEFORE the end-of-track check so a loop near the track end wraps
// rather than stopping the deck.
func (d *Deck) wrapLoop() {
	if !d.loop.active {
		return
	}
	// Guard against a degenerate loop that would spin the render loop forever.
	if d.loop.end <= d.loop.start {
		return
	}
	if d.pos >= d.loop.end {
		d.seekFrames(d.loop.start)
	}
}

// loopBeatSizes are the loop lengths a beat-loop button cycles through. The
// doubling sequence is what a DJ expects: a 4-beat loop rolls into 8, not
// into 3.
var loopBeatSizes = []float64{0.25, 0.5, 1, 2, 4, 8, 16, 32}

// loopBeatsFromNormalized maps a 0..1 knob or button position onto the beat
// sizes above. The mapping is by index rather than by value, because the
// sizes double and a linear interpolation between them would make every size
// unreachable.
func loopBeatsFromNormalized(v float64) float64 {
	if math.IsNaN(v) || v <= 0 {
		return loopBeatSizes[0]
	}
	if v >= 1 {
		return loopBeatSizes[len(loopBeatSizes)-1]
	}
	idx := int(v * float64(len(loopBeatSizes)))
	if idx >= len(loopBeatSizes) {
		idx = len(loopBeatSizes) - 1
	}
	return loopBeatSizes[idx]
}

// loopBeatsForAction interprets a LoopBeat action's Value, which carries two
// different things depending on where it came from.
//
// A MIDI encoder sends a normalized knob position in 0..1. A keyboard binding
// names a beat count directly -- w/e/y/u send 2, 4, 8 and 16, because that is
// what the key means and what the README documents.
//
// The engine used to run every value through the knob mapping, where `v >= 1`
// returns the largest size. 2, 4, 8 and 16 are all >= 1, so all four beat-loop
// keys produced a 32-beat loop and the encoder row read LOOP 32 for each.
//
// The two are told apart by range: 0..1 is a knob position, anything above is a
// beat count. A beat count is snapped to the nearest legal size rather than
// rejected, so a user who writes 3 gets a 4-beat loop instead of silence --
// and the snapping is the same one SetLoopBeat would apply anyway.
func loopBeatsForAction(v float64) float64 {
	if math.IsNaN(v) {
		return loopBeatSizes[0]
	}
	if v > 1 {
		return snapLoopBeats(v)
	}
	return loopBeatsFromNormalized(v)
}

// snapLoopBeats rounds a beat count to the nearest legal size, preferring the
// longer size on a tie.
//
// Ties are common because the sizes double: 3 is exactly between 2 and 4. A user
// who asks for 3 beats wants the bar, not the half-bar -- the same reason the
// sequence doubles in the first place.
func snapLoopBeats(n float64) float64 {
	if n <= 0 {
		return 0
	}
	best := loopBeatSizes[0]
	bestDist := math.Abs(n - best)
	for _, s := range loopBeatSizes[1:] {
		if d := math.Abs(n - s); d <= bestDist {
			best, bestDist = s, d
		}
	}
	return best
}
