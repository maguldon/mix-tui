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
	// LoopActive and LoopBeatCount expose loop state so the TUI can render a
	// loop without reaching into the Deck (spec §5: the UI reads state through
	// Snapshot only).
	LoopActive    bool
	LoopBeatCount float64
	// LoopStart and LoopEnd bound the active loop in track time.
	LoopStart time.Duration
	LoopEnd   time.Duration
	// KeyCamelot is the detected key in DJ notation ("8A"), or empty when no
	// key was detected or the confidence was below threshold. KeyConfidence is
	// the raw 0..1 score. A view must show "unknown" rather than a low-confidence
	// guess: a displayed key is believed (spec §8).
	KeyCamelot    string
	KeyConfidence float64
	// Cued reports whether the deck is routed to the headphone bus.
	Cued   bool
	Loaded bool
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
	Decks       []DeckState
	Crossfader  float64 // -1..1
	MasterLevel float64
	CueLevel    float64
	MasterPeak  float64
	CuePeak     float64
	Overruns    uint64 // renders dropped because a ring was full
}
