package analyze

import (
	"context"
	"errors"
	"time"

	"github.com/maguldon/mix-tui/internal/dsp"
)

// Progress is the state a track moves through while it is being analysed
// (spec §4.1's analyzing -> ready pipeline).
type Progress string

const (
	// ProgressAnalyzing means the FFT and both readers are running. This is
	// the expensive part, so it is reported before the work rather than after.
	ProgressAnalyzing Progress = "analyzing"
	// ProgressReady means the results are available.
	ProgressReady Progress = "ready"
	// ProgressFailed means analysis could not complete; the track is still
	// playable, just without a grid or a key.
	ProgressFailed Progress = "failed"
)

// ErrNoAudio is returned for a track with no decodable samples. It is not a
// failure of the analysis: there is simply nothing to analyse.
var ErrNoAudio = errors.New("no audio to analyse")

// Result is everything one analysis pass produces.
//
// Grid and Key are never nil: the readers return an empty, zero-confidence
// value rather than nil, so a caller cannot dereference a missing result
// (spec §8). HasGrid and HasKey report whether each is actually usable, which
// is what the views key off -- a track whose analysis ran but found no tempo
// is different from one that was never analysed.
type Result struct {
	Grid *BeatGrid
	Key  *Key

	HasGrid bool
	HasKey  bool

	// Duration is how long the pass took, for the debug overlay.
	Duration time.Duration
}

// Analyze runs both readers over a single STFT.
//
// Spec 4.1: key detection shares one FFT pass with beatgrid detection, so it
// adds negligible cost once analysis exists. Calling AnalyzeBeatGrid and
// AnalyzeKey separately computed the spectrogram twice, doubling the cost of
// the most expensive thing the application does off the audio path.
//
// Analysis is cancellable because it runs in the background and a user who
// loads three tracks in a row should not pay for the first two.
func Analyze(ctx context.Context, pcm dsp.Stereo) (*Result, error) {
	if len(pcm) == 0 {
		return nil, ErrNoAudio
	}

	start := time.Now()
	spec := ComputeSpectrogram(pcm, windowLen, hopSize)

	if err := ctx.Err(); err != nil {
		return nil, err
	}

	grid, err := BeatGridFromSpec(spec)
	if err != nil {
		return nil, err
	}
	key, err := KeyFromSpec(spec)
	if err != nil {
		return nil, err
	}

	if err := ctx.Err(); err != nil {
		return nil, err
	}

	r := &Result{Grid: grid, Key: key, Duration: time.Since(start)}
	// A grid is usable when a tempo was actually found. The reader always
	// returns a grid, even for silence, so checking for nil would report every
	// track as having a tempo.
	r.HasGrid = grid != nil && grid.BPM > 0
	r.HasKey = key != nil && key.Camelot != ""
	return r, nil
}
