package engine

import "time"

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
	Loaded        bool
	Path          string
	Title         string
	Artist        string
	Level         float64 // linear RMS of the last block
	Peak          float64
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
