package engine

import (
	"time"

	"github.com/maguldon/mix-tui/internal/dsp"
)

// Peaks is a min/max envelope over the loaded audio, with two entries per
// column (one per channel) so a hard-panned note still shows at full height.
type Peaks struct {
	Min, Max []float32
}

// TimeRange is a half-open range in track time. It is a named type so a pair of
// durations in the Snapshot says what it means without the view having to guess.
type TimeRange struct{ Start, End time.Duration }

// DeckState is an immutable view of one deck at the instant Snapshot was
// taken. The UI never reads Deck directly: doing so would race the audio
// goroutine (spec §5).
type DeckState struct {
	Index       int
	Playing     bool
	Position    time.Duration
	Duration    time.Duration
	Pitch       float64 // normalized -1..1
	BPM         float64 // 0 if no beatgrid
	HasBeatGrid bool
	// SampleRate is the deck's output rate, which may differ from the file's
	// when a resampler is converting. A view converting time to frames must use
	// this, not a package constant.
	SampleRate int
	// LoopActive and LoopBeatCount expose loop state so the TUI can render a
	// loop without reaching into the Deck (spec §5: the UI reads state through
	// Snapshot only).
	LoopActive    bool
	LoopBeatCount float64
	// LoopStart and LoopEnd bound the active loop in track time.
	LoopStart time.Duration
	LoopEnd   time.Duration
	// SavedLoopSlots are the loops stored in slots, in track time, keyed by slot
	// number.
	//
	// Deck.Loop() has always returned these; the Snapshot simply did not carry
	// them, so the TUI had no way to know a slot was occupied. Spec 6.2.1 asks
	// for saved slots to be visible alongside the hotcues.
	SavedLoopSlots map[int]TimeRange
	// Faulted marks a deck holding a track it cannot actually play audio from,
	// and FaultReason says why.
	//
	// Spec 8 asks for a fault marker on a decoder that fails mid-stream, which
	// cannot happen here: decoding is whole-file at load, so a bad file is
	// rejected before it ever reaches a deck. What this actually catches is the
	// case a user cannot otherwise diagnose -- a deck showing a title, a BPM and
	// a waveform while producing nothing, because the file has since been
	// removed or the track has run out.
	Faulted     bool
	FaultReason string
	// KeyCamelot is the detected key in DJ notation ("8A"), or empty when no
	// key was detected or the confidence was below threshold. KeyConfidence is
	// the raw 0..1 score. A view must show "unknown" rather than a low-confidence
	// guess: a displayed key is believed (spec §8).
	KeyCamelot    string
	KeyConfidence float64
	// DeltaMS is this deck's beat-phase offset from the master in milliseconds,
	// as computed by the engine against the real beatgrids. It is valid only
	// when HasDelta is true.
	DeltaMS  float64
	HasDelta bool
	// Cued reports whether the deck is routed to the headphone bus.
	Cued bool
	// Synced reports whether this deck is tempo-locked to another. Without it
	// the sync key could be pressed and nothing on screen would say so, which
	// is the worst property a mixing feature can have: you cannot tell whether
	// it is locked, and you cannot tell that it failed to lock.
	Synced bool
	Loaded bool
	// EQ holds the three band gains as -1..1 knob positions, and FilterCutoffNorm
	// is the filter knob as 0..1, with FilterHighpass selecting the slope.
	//
	// The EQ bars and the filter readout were rendered from hardcoded zeros,
	// because none of this reached the snapshot. Six EQ knobs and two filter
	// knobs are bound in the shipped Xone K2 mapping; all eight moved the
	// audio and none moved a pixel, so turning a physical knob looked broken.
	EQ               [3]float64
	FilterCutoffNorm float64
	FilterHighpass   bool
	// PCM is the loaded track's audio, shared with the Deck rather than copied.
	// A Snapshot is taken ~30 times a second, so copying audio into it would
	// allocate megabytes per frame; the slice is immutable once loaded, so a
	// view may read it safely. This is what lets the TUI draw a real waveform.
	PCM dsp.Stereo
	// Peaks is a min/max envelope of the loaded audio at PeaksWidth columns,
	// computed once when the track loads.
	//
	// It lives here rather than being derived in the view because deriving it
	// costs ~33ms for a five-minute track, and a view runs 30 times a second:
	// scanning the PCM per frame would burn two CPU-seconds per wall-clock
	// second and thrash the cache the audio thread depends on (spec §4.1
	// requires peaks be cached per track and resolution).
	Peaks      Peaks
	PeaksWidth int
	// DownbeatTimes are the bar lines, kept apart from BeatTimes so the view can
	// draw the two differently. They were never carried, so downbeats were
	// indistinguishable from ordinary beats even on the zoomed waveform -- and
	// on the unzoomed one they were not drawn at all.
	DownbeatTimes []time.Duration
	// Meter is the detected beats per bar, 0 when unknown. Carried alongside
	// DownbeatTimes so the view can say what the bar lines are rather than only
	// drawing them -- "7/8" next to a grid is the difference between a bar
	// pattern and a rendering quirk.
	Meter int
	// BeatTimes and CueTimes position the zoom view's markers. BeatTimes is
	// empty without a beatgrid.
	BeatTimes []time.Duration
	// CueTimes is a fixed-size buffer rather than a slice: Snapshot runs ~30
	// times a second, and appending into a fresh slice every call allocated
	// ~11KB per frame, which feeds the GC continuously during a set. The view
	// reads CueCount entries of it.
	CueTimes [numHotcues]time.Duration
	CueCount int
	Path     string
	Title    string
	Artist   string
	Level    float64 // linear RMS of the last block
	Peak     float64
}

// Snapshot is a consistent copy of the whole engine state for one UI refresh.
type Snapshot struct {
	Decks      []DeckState
	Crossfader float64 // -1..1
	// MasterGain and CueMix are the SETTINGS, not the output meters below.
	// The meters show what is coming out; these show where the faders are.
	// Without them the interface can render a level that responds to the music
	// but never shows the user moving a control, so the master fader and the
	// cue/program blend looked like dead keys.
	MasterGain  float64
	CueMix      float64 // 0 = cue only, 1 = program only
	MasterLevel float64
	CueLevel    float64
	MasterPeak  float64
	CuePeak     float64
	Overruns    uint64 // renders dropped because a ring was full
}
