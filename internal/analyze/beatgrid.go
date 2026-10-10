package analyze

import (
	"errors"
	"math"
	"sort"
	"time"

	"github.com/maguldon/mix-tui/internal/dsp"
)

// ErrNoBeatGrid is returned when a beat-dependent operation is requested on a
// track with no analysed beatgrid. The operation is unavailable, not failed:
// playback, pitch, EQ, and filters keep working (spec §8).
var ErrNoBeatGrid = errors.New("no beatgrid for this track")

// BeatGrid is a detected tempo and the beat positions within a track.
type BeatGrid struct {
	// BPM is the detected tempo, in the conventional doubled sense: a beat
	// every 60/BPM seconds.
	BPM float64
	// Confidence is 0..1. Below roughly 0.5 the grid should be treated as
	// unusable, and beat-dependent features disabled.
	Confidence float64
	// Beats are positions from the start of the track.
	Beats []time.Duration
	// Downbeats are indices into Beats, marking the start of each bar.
	Downbeats []int
	// Phases carries the beat period in seconds, retained so a later task can
	// reason about the grid without recomputing it.
	Phases []float64
}

// BeatPeriod returns the time between beats, or 0 when the grid is unusable.
func (g *BeatGrid) BeatPeriod() time.Duration {
	if g == nil || g.BPM <= 0 {
		return 0
	}
	return time.Duration(float64(time.Second) / g.BPM)
}

// Tempo search bounds. Below 60 BPM a tracker starts locking onto half-time
// ghosts; above 200 it starts locking onto eighth-note subdivisions.
const (
	minBPM = 60.0
	maxBPM = 200.0
)

// AnalyzeBeatGrid detects tempo and beat positions.
//
// It never returns a nil grid for decodable audio, even when there is no beat
// to find: callers get a low-confidence grid they can check rather than a
// nil-pointer hazard (spec §8).
func AnalyzeBeatGrid(pcm dsp.Stereo) (*BeatGrid, error) {
	spec := ComputeSpectrogram(pcm, windowLen, hopSize)
	if len(spec.Frames) < 4 {
		return &BeatGrid{Confidence: 0}, nil
	}

	onset := onsetEnvelope(spec)
	bpm, tempoConf := estimateTempo(onset, spec)
	beats := trackBeats(onset, bpm, spec)
	downbeats := markDownbeats(beats)

	confidence := combineConfidence(tempoConf, beats, onset)
	return &BeatGrid{
		BPM:        bpm,
		Confidence: confidence,
		Beats:      beats,
		Downbeats:  downbeats,
		Phases:     []float64{60.0 / bpm},
	}, nil
}

// onsetEnvelope is the half-wave-rectified spectral flux: the frame-to-frame
// increase in magnitude, summed over bins. Peaks in this curve are onsets.
func onsetEnvelope(spec *Spectrogram) []float64 {
	env := make([]float64, len(spec.Frames))
	prev := make([]float64, len(spec.Frames[0]))
	for f := range spec.Frames {
		var flux float64
		for b := range spec.Frames[f] {
			m := spec.magnitude(f, b)
			if d := m - prev[b]; d > 0 {
				flux += d
			}
			prev[b] = m
		}
		env[f] = flux
	}
	normalise(env)
	return env
}

// normalise scales a slice to peak 1, leaving an all-zero slice alone.
func normalise(v []float64) {
	var peak float64
	for _, x := range v {
		if x > peak {
			peak = x
		}
	}
	if peak <= 0 {
		return
	}
	for i := range v {
		v[i] /= peak
	}
}

// estimateTempo autocorrelates the onset envelope and takes the strongest
// peak in the plausible tempo range.
func estimateTempo(env []float64, spec *Spectrogram) (float64, float64) {
	framesPerSecond := float64(spec.SampleRate) / float64(spec.HopSize)
	minLag := int(math.Round(framesPerSecond * 60 / maxBPM))
	maxLag := int(math.Round(framesPerSecond * 60 / minBPM))
	if maxLag >= len(env) {
		maxLag = len(env) - 1
	}
	if minLag < 1 || maxLag <= minLag {
		return 0, 0
	}

	// Autocorrelation, computed directly rather than via a zero-padded FFT:
	// the envelope is short enough that the direct sum is simpler and avoids
	// wrap-around artefacts at the lag boundaries.
	bestLag, bestScore := 0, -1.0
	var mean float64
	for _, v := range env {
		mean += v
	}
	mean /= float64(len(env))

	scores := make([]float64, maxLag+1)
	for lag := minLag; lag <= maxLag; lag++ {
		var sum, normA, normB float64
		for i := 0; i+lag < len(env); i++ {
			a := env[i] - mean
			b := env[i+lag] - mean
			sum += a * b
			normA += a * a
			normB += b * b
		}
		if normA > 0 && normB > 0 {
			scores[lag] = sum / math.Sqrt(normA*normB)
			if scores[lag] > bestScore {
				bestScore, bestLag = scores[lag], lag
			}
		}
	}
	if bestLag == 0 {
		return 0, 0
	}

	// Octave correction: a genuine tempo has comparable energy at twice its
	// period (the backbeat). If the peak at 2*lag is nearly as strong, the
	// tracker has locked onto double-time, which is the classic failure and
	// would report half the real tempo.
	lag := bestLag
	score := bestScore
	if 2*lag <= maxLag && scores[2*lag] > 0.85*score {
		lag = 2 * lag
		score = scores[lag]
	}
	// Likewise a peak at half the period suggests half-time reading.
	if lag/2 >= minLag && scores[lag/2] > 0.85*score {
		lag = lag / 2
		score = scores[lag]
	}

	bpm := 60.0 * framesPerSecond / float64(lag)

	// Parabolic interpolation around the peak for sub-frame resolution.
	if lag > minLag && lag < maxLag {
		y0, y1, y2 := scores[lag-1], scores[lag], scores[lag+1]
		if denom := y0 - 2*y1 + y2; denom != 0 {
			shift := 0.5 * (y0 - y2) / denom
			if math.Abs(shift) < 1 {
				bpm = 60.0 * framesPerSecond / (float64(lag) + shift)
			}
		}
	}

	if bpm < minBPM || bpm > maxBPM {
		return 0, 0
	}
	// Confidence is the peak height: a flat envelope (noise) correlates
	// weakly at every lag and so scores low.
	return bpm, math.Max(0, math.Min(1, score))
}

// trackBeats places beats using a dynamic-programming pass: each candidate
// onset is scored by its own strength and by how close it sits to the period
// implied by the best predecessor.
func trackBeats(env []float64, bpm float64, spec *Spectrogram) []time.Duration {
	if bpm <= 0 || len(env) == 0 {
		return nil
	}
	framesPerSecond := float64(spec.SampleRate) / float64(spec.HopSize)
	periodFrames := 60.0 / bpm * framesPerSecond
	if periodFrames < 1 {
		return nil
	}

	// Candidate onsets: local maxima above an adaptive threshold.
	cands := onsetCandidates(env)
	if len(cands) == 0 {
		return nil
	}

	// Dynamic programming. score[i] is the best total quality of a beat chain
	// ending at candidate i, so scores accumulate along the chain rather than
	// competing with the raw onset strength of a single beat.
	n := len(cands)
	score := make([]float64, n)
	prev := make([]int, n)
	for i := range prev {
		prev[i] = -1
		score[i] = env[cands[i]]
	}
	searchLo := int(periodFrames * 0.5)
	searchHi := int(periodFrames * 1.5)
	if searchLo < 1 {
		searchLo = 1
	}

	for i := 0; i < n; i++ {
		for j := i - 1; j >= 0; j-- {
			delta := cands[i] - cands[j]
			if delta < searchLo {
				// Candidates are ascending, so deltas only grow as j walks
				// backwards. Everything further back is even farther away, and
				// the DP cannot look past one period: a chain that skips a beat
				// is not a valid chain. Break, don't continue.
				break
			}
			if searchHi > 0 && delta > searchHi {
				continue
			}
			// Prefer predecessors near one period back. The penalty is scaled
			// to the onset-strength range so it can break ties without
			// overwhelming an actual chain.
			penalty := math.Abs(float64(delta)-periodFrames) / periodFrames
			bonus := score[j] + env[cands[i]] - penalty*0.5
			if bonus > score[i] {
				score[i] = bonus
				prev[i] = j
			}
		}
	}

	// Backtrack from the best-scoring final beat.
	bestIdx := 0
	for i := 1; i < n; i++ {
		if score[i] > score[bestIdx] {
			bestIdx = i
		}
	}
	var path []int
	for i := bestIdx; i >= 0; i = prev[i] {
		path = append(path, cands[i])
		if prev[i] < 0 {
			break
		}
	}
	// The backtrack walks newest to oldest, so path is descending. Beats must
	// be chronological: reverse it into ascending order.
	sort.Sort(sort.IntSlice(path))

	beats := make([]time.Duration, 0, len(path))
	for _, c := range path {
		beats = append(beats, frameToDuration(c, spec))
	}
	return beats
}

// onsetCandidates returns frame indices of local maxima in the onset
// envelope, above a threshold derived from the envelope's own statistics.
func onsetCandidates(env []float64) []int {
	if len(env) < 3 {
		return nil
	}
	// Adaptive threshold: mean plus a fraction of the mean absolute deviation
	// is far more robust on real music than a fixed value.
	var sum float64
	for _, v := range env {
		sum += v
	}
	mean := sum / float64(len(env))
	var dev float64
	for _, v := range env {
		dev += math.Abs(v - mean)
	}
	dev /= float64(len(env))
	threshold := mean + 0.6*dev

	var cands []int
	for i := 1; i < len(env)-1; i++ {
		if env[i] < threshold {
			continue
		}
		if env[i] >= env[i-1] && env[i] > env[i+1] {
			cands = append(cands, i)
		}
	}
	return cands
}

// markDownbeats marks every fourth beat as a bar start.
//
// A four-beat grid is assumed because it holds across essentially all
// electronic music the app targets. On acoustic material with 3/4 or 7/8
// time this will be wrong; the fix is bar-length detection from the onset
// envelope, which is deferred until a track in irregular time actually needs
// it. Reporting a plausible grid beats reporting none.
func markDownbeats(beats []time.Duration) []int {
	if len(beats) < 4 {
		return nil
	}
	var down []int
	for i := 0; i < len(beats); i += 4 {
		down = append(down, i)
	}
	return down
}

func frameToDuration(f int, spec *Spectrogram) time.Duration {
	return time.Duration(float64(f) * float64(spec.HopSize) / float64(spec.SampleRate) * float64(time.Second))
}

// combineConfidence weights the tempo score with how well the placed beats
// actually fit the period, so a strong autocorrelation peak that never
// produced a coherent beat sequence does not read as confident.
func combineConfidence(tempoConf float64, beats []time.Duration, env []float64) float64 {
	if len(beats) < 4 {
		return math.Min(tempoConf*0.5, 0.4)
	}
	var gaps []float64
	for i := 1; i < len(beats); i++ {
		gaps = append(gaps, beats[i].Seconds()-beats[i-1].Seconds())
	}
	sort.Float64s(gaps)
	median := gaps[len(gaps)/2]
	if median <= 0 {
		return 0
	}
	// Fraction of gaps within 10% of the median: a good track is near 1.
	var good int
	for _, g := range gaps {
		if math.Abs(g-median)/median < 0.10 {
			good++
		}
	}
	regularity := float64(good) / float64(len(gaps))

	// A flat envelope has no meaningful onsets at all.
	var peak float64
	for _, v := range env {
		if v > peak {
			peak = v
		}
	}
	energyTerm := math.Min(1, peak*4)

	return math.Max(0, math.Min(1, 0.5*tempoConf+0.5*regularity)*energyTerm)
}
