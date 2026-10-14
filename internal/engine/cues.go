package engine

import "time"

// Cues is the per-track positional memory: hotcues and saved loop slots.
//
// Spec §4.4.3 and §10: both belong to the TRACK, not the deck, are stored in
// parallel per-track directories keyed by the same path identity, and are
// invalidated together when a file is replaced or moved.
//
// Positions are stored as fractions of the track's duration rather than as
// seconds or source frames, because a track can be decoded at a different rate
// than the deck renders at, and because a file replaced by a shorter one must
// not leave every cue pointing past its end. Both cases are handled by the
// restore step clamping to the track's length.

// Cue is one hotcue slot.
type Cue struct {
	// At is the cue's position as a fraction of the track's duration, 0..1.
	// It is a fraction rather than a time so a track re-decoded at another rate
	// restores to the same place.
	At  float64
	Set bool
}

// Cues is everything remembered about one track.
type Cues struct {
	Hotcues [numHotcues]Cue
	Loops   map[int]CueRange
}

// CueRange is a saved loop, as fractions of the track's duration.
type CueRange struct {
	Start, End float64
}

// Empty reports whether there is nothing worth writing to disk.
func (c Cues) Empty() bool {
	for _, h := range c.Hotcues {
		if h.Set {
			return false
		}
	}
	return len(c.Loops) == 0
}

// cueState reads the deck's current cues as fractions of the loaded track.
//
// It returns the zero value when no track is loaded or its length is unknown,
// which restores to "nothing remembered" rather than to a set of cues at
// nonsense positions.
func (d *Deck) cueState() Cues {
	var out Cues
	if d.track == nil || d.track.Duration <= 0 {
		return out
	}
	// frames -> seconds -> fraction of the track:
	//
	//	(frames / rate) / (Duration / time.Second)
	//
	// Duration is a time.Duration, so it is nanoseconds, and leaving that
	// conversion out puts every cue a billion times too early. The symptom is
	// quiet: the cue still exists and still lands inside the track, it is just
	// at 0 instead of 1s. Only a test that checks the restored position against
	// an expected time catches it -- every "is it set?" and "is it in range?"
	// assertion passes happily.
	frac := float64(time.Second) / (float64(d.rate) * float64(d.track.Duration))
	for i, set := range d.hotcueSet {
		if !set {
			continue
		}
		out.Hotcues[i] = Cue{At: d.hotcues[i] * frac, Set: true}
	}
	if len(d.savedLoop) > 0 {
		out.Loops = make(map[int]CueRange, len(d.savedLoop))
		for slot, s := range d.savedLoop {
			out.Loops[slot] = CueRange{
				Start: s.Start * frac,
				End:   s.End * frac,
			}
		}
	}
	return out
}

// applyCues restores remembered cues for a freshly loaded track.
//
// It runs on the render goroutine, after loadNow has cleared the previous
// track's positions, so nothing from the old track can survive.
//
// Positions are clamped to the track. A cue remembered as 0.9 of a ten-minute
// track, restored into a file replaced by a one-minute version, would otherwise
// sit past the end where pressing it seeks to a point the deck cannot reach --
// and the user has no way to see that the file changed underneath them.
func (d *Deck) applyCues(c Cues) {
	if d.track == nil || d.track.Duration <= 0 {
		return
	}
	frames := float64(d.rate) * float64(d.track.Duration) / float64(time.Second)
	clamp := func(f float64) float64 {
		if f < 0 {
			return 0
		}
		if f > 1 {
			return 1
		}
		return f
	}
	for i, h := range c.Hotcues {
		if !h.Set {
			continue
		}
		d.hotcues[i] = clamp(h.At) * frames
		d.hotcueSet[i] = true
	}
	if len(c.Loops) > 0 {
		d.savedLoop = make(map[int]SavedLoop, len(c.Loops))
		for slot, r := range c.Loops {
			start, end := clamp(r.Start), clamp(r.End)
			// A degenerate range would make every press of recall seek to a
			// point it cannot loop, so it is dropped rather than restored.
			if end <= start {
				continue
			}
			d.savedLoop[slot] = SavedLoop{Start: start * frames, End: end * frames}
		}
	}
}

// persistCues writes the loaded track's hotcues and saved loops to disk.
//
// It is called from the render goroutine after anything changes them. Writes are
// small and infrequent -- a hotcue press or a loop save, both deliberate -- so
// doing them inline costs less than the queue plumbing it would take to keep
// them off the render path, and a disk that stalls shows up here as the render
// budget going over rather than as silent data loss.
//
// Failure is silent by design. A cue that could not be saved is still set for
// this session, and the alternative -- surfacing an error from the render
// goroutine, once per hotcue press -- would have no useful place to go.
func (d *Deck) persistCues() {
	if d.cues == nil || d.track == nil || d.track.Path == "" {
		return
	}
	_ = d.cues.Save(d.track.Path, d.cueState())
}

// SetCueStore installs the store used to remember this deck's hotcues and saved
// loops. A nil store disables persistence and is what headless and test builds
// use.
func (d *Deck) SetCueStore(s *CueStore) { d.cues = s }
