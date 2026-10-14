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
	// Meter is the detected beats per bar. It is 4 unless the onset envelope
	// says otherwise, and it is what Downbeats is spaced by -- so 3/4 and 7/8
	// material gets its bars marked every 3 or 7 beats rather than every 4.
	Meter int
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
	meter := detectMeter(beats, onset, spec)
	downbeats := markDownbeats(beats, meter)

	// Coverage: beats actually placed against beats the analysed span and the
	// tempo imply. Regularity alone is not enough -- a short irregular chain
	// can look perfectly consistent, and that is how a 6-beat grid over a
	// 431-second track reported 0.60 confidence with beat features enabled.
	coverage := 1.0
	if expected := spec.duration().Seconds() * bpm / 60; expected > 0 {
		coverage = float64(len(beats)) / expected
	}

	confidence := combineConfidence(tempoConf, rawPeak, coverage, beats)
	return &BeatGrid{
		BPM:        bpm,
		Confidence: confidence,
		Beats:      beats,
		Downbeats:  downbeats,
		Meter:      meter,
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

// preferredTempo weights a tempo by how likely a listener is to tap it.
//
// Autocorrelation cannot choose the metrical level on its own, because the
// half-time peak of real music is nearly as strong as the beat: a 150 BPM kick
// pattern correlates almost as well at 75. Human tempo perception does not have
// that ambiguity, and the usual account of why is that tempi cluster near 125
// BPM and fall off by about an octave either way. Expressed in log2 so the
// falloff is symmetric: one octave down is exactly as plausible as one up.
//
// The centre is 125 rather than 120 because this application targets dance
// music, whose typical range sits above the classical 120 anchor.
const (
	preferredBPM  = 125.0
	preferredWide = 1.0 // in octaves
)

func preferredTempo(bpm float64) float64 {
	if bpm <= 0 {
		return 0
	}
	octaves := math.Log2(bpm / preferredBPM)
	return math.Exp(-0.5 * (octaves / preferredWide) * (octaves / preferredWide))
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

	// Octave resolution: ONE decision over the family {lag, 2lag, 4lag, lag/2,
	// lag/4}, weighted by autocorrelation strength AND by how plausible that
	// tempo is to a listener.
	//
	// This used to be two sequential rules -- prefer 2*lag, then prefer lag/2 --
	// where the second compared against the score the first had already
	// replaced. Doubling was therefore silently undone whenever the two peaks
	// fell in the ratio band (0.85, 1.18], so surviving rule 1 needed closer to
	// an 18% margin than the 0.85 constant claimed.
	//
	// Correcting that exposed why the peak cannot decide alone. On synthetic
	// kick-plus-hat patterns the HALF-time peak genuinely outscores the true
	// one for some tempi -- 1.09x at 128 BPM, 1.23x at 150 BPM -- which is what a
	// backbeat looks like. Any threshold on peak strength alone is therefore
	// unreliable in both directions: asking "is the other peak almost as strong"
	// fired on 150 and halved the tempo, and leaving the peak untouched broke
	// 128 and 150 the same way.
	//
	// Perceptual plausibility is the extra information, and it is why listeners
	// hear 150 rather than 75. Over 70..190 BPM every tempo resolves correctly
	// with preferredWide at one octave: at 0.9 it fixes 150 but flips 190, which
	// the true peak already had right and which the prior is not entitled to
	// override.
	lag, score := bestLag, bestScore
	bestWeight := scores[bestLag] * preferredTempo(60.0*framesPerSecond/float64(bestLag))
	for _, c := range []int{2 * bestLag, 4 * bestLag, bestLag / 2, bestLag / 4} {
		if c < minLag || c > maxLag || scores[c] <= 0 {
			continue
		}
		if w := scores[c] * preferredTempo(60.0*framesPerSecond/float64(c)); w > bestWeight {
			bestWeight = w
			lag, score = c, scores[c]
		}
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

// markDownbeats marks every meter'th beat as a bar start.
func markDownbeats(beats []time.Duration, meter int) []int {
	if meter < 1 {
		meter = 4
	}
	if len(beats) < meter {
		return nil
	}
	var down []int
	for i := 0; i < len(beats); i += meter {
		down = append(down, i)
	}
	return down
}

// detectMeter infers beats per bar from how strongly beats fall on a repeating
// cycle.
//
// In any meter, the first beat of each bar is accented -- a kick and snare
// together, a chord change, a breath -- while the others are not. Sampling the
// onset envelope at each beat therefore gives a sequence whose strength
// repeats with period equal to the bar length. Finding that period is the whole
// job: for each candidate bar length, score the best-aligned phase by how much
// stronger its accented beats are than the rest, and take the winner.
//
// Four is the default and the prior, because it holds across essentially all
// electronic music the app targets and a wrong bar line is worse than a
// right-looking one. A competing meter has to beat it by a clear margin to win,
// and it has to do so on several bars rather than one lucky accent, or the
// downbeat markers would flicker between metres from track to track.
func detectMeter(beats []time.Duration, onset []float64, spec *Spectrogram) int {
	const (
		minMeter = 2
		maxMeter = 8
		// A candidate must have enough beats to show the pattern repeating.
		// Two bars is the floor: one bar cannot distinguish 7 from 14.
		minBars = 2
		// meterMargin is how much better a non-4 candidate must score, as a
		// fraction of the 4/4 score, before it is preferred over the prior.
		meterMargin = 0.25
	)

	if len(beats) < minMeter*minBars || len(onset) == 0 {
		return 4
	}
	strength := make([]float64, len(beats))
	for i, b := range beats {
		strength[i] = onsetAt(onset, spec, b)
	}

	four := scoreMeter(strength, 4)
	// With no accent pattern there is nothing to compare against, and every
	// candidate would score near zero. Taking a margin over a zero baseline
	// would let any rounding noise win and invent a meter, so the prior stands.
	if four <= 0 {
		return 4
	}
	best, bestScore := 4, four
	for n := minMeter; n <= maxMeter; n++ {
		if n == 4 || len(beats) < n*minBars {
			continue
		}
		// Score against the same yardstick as 4/4 so the margin is meaningful.
		if s := scoreMeter(strength, n); s > bestScore*(1+meterMargin) {
			best, bestScore = n, s
		}
	}
	return best
}

// scoreMeter returns how strongly beats separate into accented and unaccented
// under a bar length of n, normalised by the mean onset strength so that a
// quiet passage and a loud one are comparable.
//
// The score is the mean accent minus the mean non-accent, over the best phase
// alignment. Positive means the accent pattern is real; near zero means beats
// are evenly strong and this bar length explains nothing.
func scoreMeter(strength []float64, n int) float64 {
	if n < 1 || len(strength) < n {
		return 0
	}
	total := 0.0
	for _, s := range strength {
		total += s
	}
	if total <= 0 {
		return 0
	}
	mean := total / float64(len(strength))

	// Try every phase; the first beat is not always the downbeat.
	best := math.Inf(-1)
	for phase := 0; phase < n; phase++ {
		var accented, plain, nAcc, nPlain float64
		for i, s := range strength {
			if i%n == phase {
				accented += s
				nAcc++
			} else {
				plain += s
				nPlain++
			}
		}
		if nAcc == 0 || nPlain == 0 {
			continue
		}
		if d := accented/nAcc - plain/nPlain; d > best {
			best = d
		}
	}
	return best / mean
}

// onsetAt samples the onset envelope at a beat position.
//
// The envelope is per-frame and beats are times, so this converts and clamps.
// A window of a few frames either side is averaged rather than a single frame
// read: the flux peaks roughly half a window before the transient it belongs
// to, and beat positions carry their own small error, so a point sample lands
// on a slope and reads lower than the onset actually was.
func onsetAt(onset []float64, spec *Spectrogram, at time.Duration) float64 {
	if len(onset) == 0 {
		return 0
	}
	frame := int(float64(at) / float64(time.Second) * float64(spec.SampleRate) / float64(spec.HopSize))
	const half = 2
	// Always divide by the full window width, skipping frames outside the
	// envelope rather than averaging only the ones that happen to fit.
	//
	// Averaging just the available frames looks equivalent and is not: at the
	// start of a track the window is half clipped, so beat 0 divides by 3 where
	// every later beat divides by 5 and reads ~1.7x louder. That is a louder
	// beat 0, which is a phantom accent at the start of the track, and the meter
	// detector reads it as one -- it then picked a bar length whose accent
	// happened to land on beat 0 over material with no accents at all.
	sum := 0.0
	for f := frame - half; f <= frame+half; f++ {
		if f < 0 || f >= len(onset) {
			continue
		}
		sum += onset[f]
	}
	return sum / float64(2*half+1)
}

// duration is the span of audio the spectrogram covers. Frame f covers
// [f*hop, f*hop+WindowLen), so the last frame reaches the end of the signal.
func (s *Spectrogram) duration() time.Duration {
	if len(s.Frames) == 0 {
		return 0
	}
	frames := (len(s.Frames) - 1) * s.HopSize
	return time.Duration(float64(frames+s.WindowLen) / float64(s.SampleRate) * float64(time.Second))
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
func combineConfidence(tempoConf, rawPeak, coverage float64, beats []time.Duration) float64 {
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

	// Coverage, full credit at half the track and above. Real music has
	// breakdowns and quiet bars, so demanding 1.0 would punish honest gaps; a
	// grid that only reached a sliver of the track is the case that matters.
	coverageTerm := math.Min(1, coverage/0.5)

	return math.Max(0, math.Min(1, 0.5*tempoConf+0.5*regularity)*energyTerm*coverageTerm)
}
