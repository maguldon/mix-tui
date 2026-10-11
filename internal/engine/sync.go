package engine

import (
	"math"
	"time"

	"github.com/maguldon/mix-tui/internal/analyze"
	"github.com/maguldon/mix-tui/internal/dsp"
)

// Sync and quantize are the two beat-dependent position controls.
//
// The governing rule from spec §5.6 is that sync RETUNES and quantize SNAPS,
// and neither may disturb a loop that is already playing. Both therefore act
// only on pitch and playhead, and both leave loop boundaries exactly as the
// user placed them. A DJ sets a loop, engages sync, and the loop must still be
// where their finger put it — if sync re-laid it, the loop would jump under
// them mid-performance.

// EngageSync matches this deck's tempo to master's by setting its pitch.
//
// It sets pitch ONLY. Loop start and end are never modified: retuning a deck
// whose loop is playing would move the loop, which is precisely the behaviour
// §5.6 forbids.
func (d *Deck) EngageSync(master *Deck) error {
	if d == nil || master == nil || d.beatgrid == nil || master.beatgrid == nil {
		return analyze.ErrNoBeatGrid
	}
	if d.beatgrid.BPM <= 0 || master.beatgrid.BPM <= 0 {
		return analyze.ErrNoBeatGrid
	}
	// The slave must play at the master's tempo. Tempo comes from the
	// resampling ratio, so a faster master needs a ratio above 1.
	ratio := master.beatgrid.BPM / d.beatgrid.BPM
	if math.IsNaN(ratio) || math.IsInf(ratio, 0) || ratio <= 0 {
		return analyze.ErrNoBeatGrid
	}
	// Clamp to the pitch fader's range. A 200 BPM master against a 60 BPM
	// track asks for 3.3x, which would be absurd; clamping locks both ends
	// and reports the mismatch through DeltaBeat instead.
	d.SetPitchNormalized(dsp.ClampNormalized(RatioToNormalized(ratio)))
	d.syncedTo = master
	return nil
}

// DisableSync stops tracking the master. The pitch fader keeps whatever value
// it holds, which is what a DJ expects: releasing sync must not slam the deck
// back to its original tempo.
func (d *Deck) DisableSync() { d.syncedTo = nil }

// SyncActive reports whether this deck is tempo-locked.
func (d *Deck) SyncActive() bool { return d.syncedTo != nil }

// SyncMaster returns the deck this one is locked to, or nil.
func (d *Deck) SyncMaster() *Deck { return d.syncedTo }

// nearestBeat returns the source frame of the grid beat closest to frame.
//
// The grid's explicit beat list is authoritative when present: it encodes
// where the analyser actually found beats, including any drift. With no list,
// the beat period is extrapolated from the BPM and the position's phase within
// it, which is exact for a steady tempo.
func (d *Deck) nearestBeat(frame float64) (float64, bool) {
	if d.beatgrid == nil || d.beatgrid.BPM <= 0 {
		return 0, false
	}
	if beats := d.beatgrid.Beats; len(beats) > 0 {
		// Binary search for the insertion point, then pick the nearer of the
		// two beats around it.
		//
		// This ran as a linear scan over every beat in the track, twice per
		// deck, on every Snapshot -- and Snapshot runs about thirty times a
		// second while holding the engine mutex. A seven-hour set at 120BPM is
		// 50,000 beats, which put half a millisecond of render-path work per
		// frame behind the same lock the mix takes. The list is sorted and
		// never mutates between loads, so the search is exact, not an
		// approximation.
		//
		// The search stays in frame space throughout. Converting the frame to
		// a duration and back would be lossy, and framesToDuration returns 0
		// for a deck with no track loaded, which silently collapsed the whole
		// search onto the first beat.
		lo, hi := 0, len(beats)
		for lo < hi {
			mid := int(uint(lo+hi) >> 1)
			if d.durationToFrames(beats[mid]) < frame {
				lo = mid + 1
			} else {
				hi = mid
			}
		}
		best, bestDiff := 0.0, math.Inf(1)
		for _, i := range [...]int{lo - 1, lo} {
			if i < 0 || i >= len(beats) {
				continue
			}
			f := d.durationToFrames(beats[i])
			if diff := math.Abs(f - frame); diff < bestDiff {
				best, bestDiff = f, diff
			}
		}
		return best, true
	}

	per := d.beatPeriodFrames()
	if per <= 0 {
		return 0, false
	}
	// Nearest multiple of the period.
	return math.Round(frame/per) * per, true
}

// QuantizeToNearestBeat nudges the playhead to the closest beat.
//
// It goes through seek, never a raw position write: quantize is a seek in
// every respect that matters, and writing the position directly would
// reintroduce exactly the click the seek primitive exists to prevent.
func (d *Deck) QuantizeToNearestBeat() error {
	if d.beatgrid == nil || d.beatgrid.BPM <= 0 {
		return analyze.ErrNoBeatGrid
	}
	target, ok := d.nearestBeat(d.pos)
	if !ok {
		return analyze.ErrNoBeatGrid
	}
	d.seekFrames(target)
	return nil
}

// NudgeBeats moves the playhead by a signed number of beats.
//
// It is a seek for the same reason quantize is, and it leaves loop boundaries
// untouched even when the deck is looping: the loop still wraps on its own
// schedule.
func (d *Deck) NudgeBeats(delta float64) error {
	per := d.beatPeriodFrames()
	if per <= 0 {
		return analyze.ErrNoBeatGrid
	}
	if math.IsNaN(delta) {
		return analyze.ErrNoBeatGrid
	}
	d.seekFrames(d.pos + per*delta)
	return nil
}

// beatPhaseError returns how far frame sits from its own nearest beat, signed:
// positive means the deck is PAST the beat (late), negative means early.
func (d *Deck) beatPhaseError(frame float64) (time.Duration, bool) {
	beat, ok := d.nearestBeat(frame)
	if !ok {
		return 0, false
	}
	return d.framesToDuration(frame - beat), true
}

// DeltaBeat reports how far this deck's position sits from its own nearest
// beat, relative to the master's — the ΔBEAT readout (spec §6.3).
//
// It compares the two decks' PHASE ERRORS rather than the difference of their
// nearest beat positions. Both decks quantise to the same beat whenever they
// share one, so a beat-to-beat difference reads a flat zero exactly when the
// user most wants feedback: two decks sitting on the same grid line but
// arriving at it from different sides. Comparing phase errors answers the
// question the readout exists for — how far apart are these two decks right
// now — and a positive result means the slave is late.
func (d *Deck) DeltaBeat(master *Deck) (time.Duration, error) {
	if d.beatgrid == nil || master.beatgrid == nil {
		return 0, analyze.ErrNoBeatGrid
	}
	slaveErr, ok1 := d.beatPhaseError(d.pos)
	masterErr, ok2 := master.beatPhaseError(master.pos)
	if !ok1 || !ok2 {
		return 0, analyze.ErrNoBeatGrid
	}

	offset := slaveErr - masterErr

	// Clamp to half a beat: beyond that the two are not the same beat any
	// more, and a large number would be misleading rather than informative.
	if half := d.framesToDuration(d.beatPeriodFrames() / 2); half > 0 {
		if offset > half {
			offset = half
		}
		if offset < -half {
			offset = -half
		}
	}
	return offset, nil
}
