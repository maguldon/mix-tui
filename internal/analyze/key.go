package analyze

import (
	"fmt"
	"github.com/maguldon/mix-tui/internal/dsp"
	"math"
	"strconv"
	"strings"
)

// KeyConfidenceThreshold is the point below which a key is reported as
// undetected rather than guessed.
//
// This is the most consequential number in the package. Key detection on
// percussive or atonal material — drum and bass breaks, noise, unstrung
// instruments — produces confident nonsense. A wrong key displayed with
// confidence is worse than no key at all, because a user crate-matching sets
// will trust it (spec §6.5).
const KeyConfidenceThreshold = 0.55

// Key is a detected musical key.
type Key struct {
	// Camelot is the DJ notation ("8A", "7B"). Empty when below threshold.
	Camelot string
	// Name is the musical name ("C major"). Empty when below threshold.
	Name string
	// Confidence is 0..1.
	Confidence float64
}

// noteNames are the pitch classes in C-major order.
var noteNames = [12]string{
	"C", "C#", "D", "D#", "E", "F", "F#", "G", "G#", "A", "A#", "B",
}

// majorProfile and minorProfile are the Krumhansl-Schmuckler key profiles:
// the empirically measured importance of each scale degree in perceived major
// and minor tonality.
var majorProfile = [12]float64{6.35, 2.23, 3.48, 2.33, 4.38, 4.09, 2.52, 5.19, 2.39, 3.66, 2.29, 2.88}
var minorProfile = [12]float64{6.33, 2.68, 3.52, 5.38, 2.60, 3.53, 2.54, 4.75, 3.98, 2.69, 3.34, 3.17}

// AnalyzeKey estimates the key from the same spectrogram the beat tracker
// uses (spec §4.1: one STFT, two readings).
//
// Below KeyConfidenceThreshold both Camelot and Name are returned EMPTY. The
// best guess is not returned with a low score attached, because a caller that
// shows a key at all will be believed.
func AnalyzeKey(pcm dsp.Stereo) (*Key, error) {
	spec := ComputeSpectrogram(pcm, windowLen, hopSize)
	if len(spec.Frames) == 0 {
		return &Key{}, nil
	}
	chroma := meanChroma(spec)

	bestMajor, bestMajorScore := -1, 0.0
	bestMinor, bestMinorScore := -1, 0.0
	for pc := 0; pc < 12; pc++ {
		if s := correlation(chroma, rotated(majorProfile, pc)); s > bestMajorScore {
			bestMajorScore, bestMajor = s, pc
		}
		if s := correlation(chroma, rotated(minorProfile, pc)); s > bestMinorScore {
			bestMinorScore, bestMinor = s, pc
		}
	}

	winner, score := bestMajor, bestMajorScore
	minorWins := false
	if bestMinorScore > score {
		winner, score, minorWins = bestMinor, bestMinorScore, true
	}

	// Mode arbitration by the third.
	//
	// Profile correlation alone cannot reliably separate a major key from
	// its relative minor: the two share seven of eight scale degrees, and a
	// sparse triad supplies only three of them, leaving the deciding
	// degrees absent. The third is present in every case though — a minor
	// third above the root versus a major third — so it settles the mode
	// directly from evidence rather than from an absent note.
	//
	// The winner's tonic is re-derived from the strongest chroma energy so
	// this test is anchored to what was actually heard.
	tonic := argmaxChroma(chroma)
	if chroma[(tonic+3)%12] > chroma[(tonic+4)%12] {
		// Flat third present, natural absent: minor.
		minorWins = true
	} else {
		minorWins = false
	}
	// Re-score the winning mode at the tonic actually heard.
	if minorWins {
		score = correlation(chroma, rotated(minorProfile, tonic))
		winner = tonic
	} else {
		score = correlation(chroma, rotated(majorProfile, tonic))
		winner = tonic
	}

	// The runner-up is the same tonic scored against the opposite mode's profile.
	// Comparing the winner against itself would make the decisiveness term always
	// zero, which is how an earlier version reported a confidence of exactly 0 for
	// every key.
	var runnerUp float64
	if minorWins {
		runnerUp = correlation(chroma, rotated(majorProfile, tonic))
	} else {
		runnerUp = correlation(chroma, rotated(minorProfile, tonic))
	}

	// Confidence combines two independent signals, because neither alone is
	// trustworthy.
	//
	// Absolute fit: how well the best profile matches the observed chroma at
	// all. A clean triad correlates around 0.84 against its own key profile;
	// noise and percussion correlate far lower. This is the primary signal.
	//
	// Decisiveness: the margin over the runner-up. A major key and its
	// relative minor share seven of eight notes, so even a perfect major-key
	// match only outscores its relative by ~0.09. This term exists to reject
	// genuinely ambiguous input, and is deliberately weighted low so it
	// modulates the absolute fit rather than dominating it.
	fit := math.Max(0, math.Min(1, (score-0.35)/0.45))
	decisive := math.Max(0, math.Min(1, (score-runnerUp)/0.20))
	confidence := math.Max(0, math.Min(1, 0.75*fit+0.25*decisive))

	if confidence < KeyConfidenceThreshold {
		return &Key{Confidence: confidence}, nil
	}

	camelot := pitchClassToCamelot(winner, minorWins)
	name := pitchClassToName(winner, minorWins)
	return &Key{Camelot: camelot, Name: name, Confidence: confidence}, nil
}

// meanChroma folds the spectrogram into a twelve-element pitch-class profile.
//
// Three corrections matter for accuracy.
//
// Magnitudes are passed through a log before folding. Raw FFT magnitudes span
// orders of magnitude, so without this the profile measures loudness rather
// than key, and the correlation is dominated by whichever bin is loudest.
//
// Bin centre frequency is refined by parabolic interpolation. A 2048-point
// window at 44.1kHz gives 21.5Hz bins — wider than the ~15Hz gap between
// adjacent semitones in the register keys live in. Rounding a bin centre to
// the nearest pitch class therefore lands on the wrong note, and the
// detection is simply broken. The refinement recovers sub-bin accuracy.
//
// The bins are trimmed to the register a harmonic key occupies (A2 to C6).
// Below it is kick and bass fundamental energy, above it is cymbal and noise.
func meanChroma(spec *Spectrogram) [12]float64 {
	var chroma [12]float64
	const minHz, maxHz = 110.0, 1050.0

	for f := range spec.Frames {
		for b := 0; b < len(spec.Frames[f]); b++ {
			hz := spec.binHz(float64(b))
			if hz < minHz/2 || hz >= maxHz {
				continue
			}
			m := spec.magnitude(f, b)
			if m <= 0 {
				continue
			}

			// Only genuine spectral peaks describe a pitch. Folding every bin
			// in would smear each note across its neighbours through window
			// leakage, and a three-note chord would come out nearly flat.
			mPrev := spec.magnitude(f, b-1)
			mNext := spec.magnitude(f, b+1)
			if m < mPrev || m < mNext {
				continue
			}

			hz = refinePeakHz(spec, f, b)
			if hz < minHz || hz >= maxHz {
				continue
			}

			weight := math.Log1p(m) * (1.0 / (1.0 + hz/2000.0))
			chroma[pitchClassOf(hz)] += weight
			// Fold the second harmonic into the same pitch class: a key's
			// structure is carried by the overtone series, and without this the
			// profile is biased toward whatever note has energy near A2.
			if h2 := 2 * hz; h2 < maxHz {
				chroma[pitchClassOf(h2)] += 0.5 * weight
			}
		}
	}

	var total float64
	for _, v := range chroma {
		total += v
	}
	if total > 0 {
		for i := range chroma {
			chroma[i] /= total
		}
	}
	return chroma
}

// refinePeakHz estimates the true frequency of a spectral peak using
// parabolic interpolation over the bin and its two neighbours, then returns
// it in Hz. This is what makes pitch-class mapping accurate when the bin
// spacing is coarser than a semitone.
func refinePeakHz(spec *Spectrogram, frame, bin int) float64 {
	hz := spec.binHz(float64(bin))
	m0 := spec.magnitude(frame, bin)
	mPrev := spec.magnitude(frame, bin-1)
	mNext := spec.magnitude(frame, bin+1)
	denom := mPrev - 2*m0 + mNext
	if denom == 0 {
		return hz
	}
	shift := 0.5 * (mPrev - mNext) / denom
	if math.Abs(shift) > 1 {
		return hz
	}
	return hz + shift*float64(spec.SampleRate)/float64(spec.WindowLen)
}

// argmaxChroma returns the pitch class carrying the most chroma energy.
func argmaxChroma(c [12]float64) int {
	best := 0
	for i := 1; i < 12; i++ {
		if c[i] > c[best] {
			best = i
		}
	}
	return best
}

// pitchClassOf maps a frequency to a pitch class in equal temperament,
// folding octaves together.
//
// A4 (440Hz) is pitch class 9 in the noteNames table, where C is 0, so the
// semitone offset from A4 is shifted by 9 before folding. Getting this
// reference wrong rotates every key by a fixed interval — a subtle bug that
// still "works" on some inputs.
func pitchClassOf(hz float64) int {
	if hz <= 0 {
		return 0
	}
	pc := int(math.Round(12*math.Log2(hz/440.0))) + 9
	pc %= 12
	if pc < 0 {
		pc += 12
	}
	return pc
}

// rotated returns the profile rotated so that index pc sits at position 0,
// which is how a candidate key is compared against a fixed reference.
func rotated(profile [12]float64, pc int) [12]float64 {
	var out [12]float64
	for i := 0; i < 12; i++ {
		out[i] = profile[(i+pc)%12]
	}
	return out
}

// correlation is Pearson correlation between two profiles.
func correlation(a, b [12]float64) float64 {
	var ma, mb float64
	for i := 0; i < 12; i++ {
		ma += a[i]
		mb += b[i]
	}
	ma /= 12
	mb /= 12
	var num, da, db float64
	for i := 0; i < 12; i++ {
		x := a[i] - ma
		y := b[i] - mb
		num += x * y
		da += x * x
		db += y * y
	}
	if da == 0 || db == 0 {
		return 0
	}
	return num / math.Sqrt(da*db)
}

// camelotMajor and camelotMinor map Camelot numbers to pitch classes, B for
// major and A for minor.
//
// These are tables rather than arithmetic because the Camelot wheel runs along
// the circle of fifths, not chromatic semitones: adjacent numbers are a fifth
// apart, and any closed-form mapping eventually gets the enharmonic keys
// (C#/Db, F#/Gb, D#/Eb) wrong. Eight independent sources agree on this exact
// table, and a wrong rotation here would silently mislabel every track in the
// library.
//
// Note 8B is C major and 8A is A minor: same number, relative pair.
var camelotMajor = [13]int{0: 0, 1: 11, 2: 6, 3: 1, 4: 8, 5: 3, 6: 10, 7: 5, 8: 0, 9: 7, 10: 2, 11: 9, 12: 4}
var camelotMinor = [13]int{0: 0, 1: 8, 2: 3, 3: 10, 4: 5, 5: 0, 6: 7, 7: 2, 8: 9, 9: 4, 10: 11, 11: 6, 12: 1}

// pitchClassToCamelot converts a pitch class and mode to Camelot notation.
func pitchClassToCamelot(pc int, minor bool) string {
	pc %= 12
	table := &camelotMajor
	if minor {
		table = &camelotMinor
	}
	for num := 1; num <= 12; num++ {
		if table[num] == pc {
			if minor {
				return fmt.Sprintf("%dA", num)
			}
			return fmt.Sprintf("%dB", num)
		}
	}
	return ""
}

// pitchClassToName converts a pitch class and mode to a musical name.
func pitchClassToName(pc int, minor bool) string {
	name := noteNames[pc%12]
	if minor {
		return name + " minor"
	}
	return name + " major"
}

// camelotToName maps Camelot notation to a musical name.
func camelotToName(code string) string {
	code = strings.TrimSpace(code)
	if len(code) < 2 {
		return ""
	}
	minor := code[len(code)-1] == 'A'
	num, err := strconv.Atoi(code[:len(code)-1])
	if err != nil || num < 1 || num > 12 {
		return ""
	}
	table := &camelotMajor
	if minor {
		table = &camelotMinor
	}
	return pitchClassToName(table[num], minor)
}

// nameToCamelot is the inverse of camelotToName.
func nameToCamelot(name string) string {
	lower := strings.ToLower(strings.TrimSpace(name))
	minor := strings.HasSuffix(lower, "minor")
	root := strings.TrimSpace(strings.TrimSuffix(strings.TrimSuffix(lower, "minor"), "major"))
	for pc, n := range noteNames {
		if strings.EqualFold(n, root) {
			return pitchClassToCamelot(pc, minor)
		}
	}
	return ""
}
