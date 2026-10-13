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
//
// The factor of 60 is not optional: a beat is 60/BPM seconds, so at 128 BPM the
// period is 0.46875s. Dividing time.Second by BPM alone yields 7.8ms, sixty
// times too short, which would silently shrink every beat loop built on it.
// Nothing called this while it was wrong — the engine computes its own period in
// source frames (see deck.beatPeriodFrames) — so the error sat latent behind a
// correct-looking accessor.
func (g *BeatGrid) BeatPeriod() time.Duration {
	if g == nil || g.BPM <= 0 {
		return 0
	}
	return time.Duration(60 * float64(time.Second) / g.BPM)
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
	return BeatGridFromSpec(ComputeSpectrogram(pcm, windowLen, hopSize))
}

// BeatGridFromSpec is the beat tracker half of Analyze, taking a spectrogram
// the caller may already have. Spec 4.1 wants one STFT to serve both analyses;
// calling AnalyzeBeatGrid and AnalyzeKey separately ran the FFT twice.
func BeatGridFromSpec(spec *Spectrogram) (*BeatGrid, error) {
	if len(spec.Frames) < 4 {
		return &BeatGrid{Confidence: 0}, nil
	}

	onset, rawPeak := onsetEnvelope(spec)
	bpm, tempoConf := estimateTempo(onset, spec)
	beats := trackBeats(onset, bpm, spec)
	downbeats := markDownbeats(beats)

	confidence := combineConfidence(tempoConf, rawPeak, beats)
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
//
// It returns the envelope and the largest raw flux value in it. The raw peak
// has to be taken before normalise: a normalised envelope peaks at exactly 1 by
// construction, so anything reading its peak learns nothing. The raw peak is
// what distinguishes a real onset (a broadband jump, two to three orders of
// magnitude above the floor) from the gentle ripple a sustained tone produces
// against the Hann window overlap.
func onsetEnvelope(spec *Spectrogram) ([]float64, float64) {
	env := make([]float64, len(spec.Frames))
	prev := make([]float64, len(spec.Frames[0]))
	var rawPeak float64
	for f := range spec.Frames {
		if f == 0 {
			// The first frame has no predecessor, so every bin reads as a jump
			// from zero and the flux is the frame's total energy rather than an
			// onset. That made frame 0 the tallest point in the envelope for
			// every track, which did two things: normalise rescaled every real
			// onset down by it, and onsetCandidates never saw the first real
			// onset as a local maximum, because it always sat next to a taller
			// frame. Tracks then began their beat chain on the first off-beat
			// instead of the first downbeat.
			for b := range spec.Frames[f] {
				prev[b] = spec.magnitude(f, b)
			}
			continue
		}
		var flux float64
		for b := range spec.Frames[f] {
			m := spec.magnitude(f, b)
			if d := m - prev[b]; d > 0 {
				flux += d
			}
			prev[b] = m
		}
		if flux > rawPeak {
			rawPeak = flux
		}
		env[f] = flux
	}
	normalise(env)
	return env, rawPeak
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
	// A beat-to-beat interval should sit near ONE period, not half of one. The
	// window exists to absorb tempo drift, not to admit off-beat subdivisions:
	// at 0.5x a hat sits exactly on the acceptance boundary, so whether the
	// chain swallowed a hat depended on rounding the period to an integer frame.
	// Real drift is far below a quarter of a beat between consecutive beats.
	searchLo := int(periodFrames * 0.75)
	searchHi := int(periodFrames * 1.25)
	if searchLo < 1 {
		searchLo = 1
	}

	for i := 0; i < n; i++ {
		for j := i - 1; j >= 0; j-- {
			delta := cands[i] - cands[j]
			// Candidates are ascending, so delta only GROWS as j walks backwards.
			// Both bounds therefore have to break in that direction: too far is
			// hopeless, too close just means keep looking.
			//
			// Getting this backwards is catastrophic and silent. Real music puts
			// several onsets between beats, so for most i the very first
			// j = i-1 is nearer than searchLo. Breaking there left prev[i] = -1
			// for almost every candidate, the backtrack collapsed to a path of
			// length 1, and a 431-second track came back with 6 beats. It went
			// unnoticed because an impulse-train fixture has exactly one onset
			// per period, so j = i-1 already lands inside the window and the
			// guards are never reached.
			if searchHi > 0 && delta > searchHi {
				break
			}
			if delta < searchLo {
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

	// Backtrack from the best-scoring final beat, then keep going over whatever
	// the first chain did not consume.
	//
	// A single backtrack yields ONE contiguous run. Real music breaks the
	// chain: a quiet bar, a dense fill, a passage whose onsets land outside the
	// search window. Whatever lay outside the best run was simply discarded,
	// which on a real track threw away more than half the grid: a 431s track
	// came back with beats covering only its middle stretch. Repeating the
	// extraction over the remaining candidates recovers those stretches as
	// separate segments, which is the standard way out of this.
	used := make([]bool, n)
	var path []int

	// Every segment must land on the SAME grid, or the tracker recovers two
	// metrical levels at once. On a track with off-beat hats the kick chain and
	// the hat chain are both perfectly periodic; extracting both interleaves
	// them and reports twice the true beat rate. The first chain found is the
	// highest-scoring one, which is the strongest onsets, so it defines the
	// phase and later segments must agree with it.
	//
	// A quarter of a period is the tolerance: hats sit at exactly half a
	// period, so they are comfortably outside it, while genuine beat jitter in
	// real material is well inside.
	var phase float64
	havePhase := false
	phaseTol := periodFrames * 0.25

	// Rank candidates once by score, then walk that order skipping the ones
	// earlier segments claimed. Rescanning all n per segment is the same result
	// but quadratic in the number of segments, which is exactly the case a long
	// dense track produces.
	order := make([]int, n)
	for i := range order {
		order[i] = i
	}
	sort.Slice(order, func(a, b int) bool { return score[order[a]] > score[order[b]] })

	for _, best := range order {
		if used[best] {
			continue
		}
		// Walk back until the chain leaves this segment: either it runs out of
		// predecessors, or it reaches a beat an earlier segment already claimed.
		var chain []int
		for i := best; i >= 0 && !used[i]; i = prev[i] {
			chain = append(chain, cands[i])
			used[i] = true
			if prev[i] < 0 {
				break
			}
		}
		if len(chain) == 0 {
			continue
		}
		// chain is descending; the phase is read off its earliest beat.
		if !havePhase {
			phase = math.Mod(float64(chain[len(chain)-1]), periodFrames)
			havePhase = true
		}
		for _, f := range chain {
			off := math.Mod(float64(f)-phase, periodFrames)
			if off > periodFrames/2 {
				off = periodFrames - off
			}
			if off <= phaseTol {
				path = append(path, f)
			}
		}
	}
	// The backtracks walk newest to oldest, so path is descending, and later
	// segments can also land out of order. Beats must be chronological and free
	// of duplicates.
	sort.Sort(sort.IntSlice(path))

	// Phase agreement alone is not enough on dense material: a kick and a snare
	// a few frames apart both sit inside one period window, and separate segments
	// overlapping in time land on top of each other. Keeping the first of each
	// period slot restores one beat per period. Half a period is a safe floor:
	// the DP already refuses chains spaced below 0.75 of it, so this only merges
	// duplicates, never real beats.
	minGap := periodFrames * 0.5
	beats := make([]time.Duration, 0, len(path))
	last := math.Inf(-1)
	for _, f := range path {
		if float64(f)-last < minGap {
			continue
		}
		last = float64(f)
		beats = append(beats, frameToDuration(f, spec))
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

// frameToDuration converts a frame index to a time position.
//
// Frame f covers samples [f*hop, f*hop+WindowLen): the window is NOT centred on
// its nominal time. A transient's magnitude in frame f peaks when the window
// sits centred on it, and the flux is the frame-to-frame increase, so flux
// peaks roughly half a window BEFORE the transient actually happens. Reporting
// f*hop therefore biases every beat early by WindowLen/2 — 23ms here, which is
// 5% of a beat at 128 BPM and enough to make quantize feel loose.
//
// Adding half a window reports the window centre instead, the standard STFT
// convention. Only beat positions use this; key detection never reads a frame
// time, so a constant offset cannot affect it.
func frameToDuration(f int, spec *Spectrogram) time.Duration {
	centre := float64(f)*float64(spec.HopSize) + float64(spec.WindowLen)/2
	return time.Duration(centre / float64(spec.SampleRate) * float64(time.Second))
}

// combineConfidence weights the tempo score with how well the placed beats
// actually fit the period, so a strong autocorrelation peak that never
// produced a coherent beat sequence does not read as confident.
//
// rawPeak is the largest flux value BEFORE the envelope was normalised. A
// sustained tone still produces a periodic ripple in the envelope, because a
// stationary sine's windowed magnitudes wobble against the 4x Hann overlap, so
// regularity and tempo score alone will happily certify a sine as a beat. The
// absolute size of the flux is what says no: a real onset jumps the spectrum by
// orders of magnitude, a tone's ripple does not.
func combineConfidence(tempoConf, rawPeak float64, beats []time.Duration) float64 {
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

	// Spectral flux magnitude. The envelope is normalised to peak 1, so reading
	// its peak here always yielded exactly 1 and this term did nothing at all.
	// Measured on real material: a drum pattern peaks near 1.1e3, a sustained
	// sine near 0.09, so the knee sits between them with room to spare.
	energyTerm := math.Min(1, rawPeak*4)

	return math.Max(0, math.Min(1, 0.5*tempoConf+0.5*regularity)*energyTerm)
}
