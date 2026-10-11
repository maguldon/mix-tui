package engine

import (
	"math"

	"github.com/maguldon/mix-tui/internal/dsp"
)

// strip is one deck's contribution to the mix.
type strip struct {
	deck *Deck
	// gain is the strip's current output gain, owned by the engine under its
	// mutex. mix reads a private copy so the audio path never takes the lock.
	gain float64
	// trim is the channel gain fader, 0..1 where 0 is unity, applied after the
	// crossfader so the two controls stay independent.
	trim float64
	// cueEnabled routes this deck to the headphone bus.
	cueEnabled bool
	// post is the deck's rendered audio for one block, preallocated. Render
	// writes here; mix consumes it. Keeping it per-strip means decks never
	// render into a shared buffer.
	post dsp.Stereo
	// level and peak are the most recent block measurements, for the UI. They
	// are written by the audio goroutine and read by the UI goroutine through
	// Snapshot, which takes the engine mutex.
	level float64
	peak  float64
}

// Mixer sums deck strips into the main and cue buses.
//
// Every buffer here is allocated once in New. mix runs on the audio goroutine
// and must not allocate (spec §4.3).
type Mixer struct {
	strips []*strip
	// xfader is -1 (full left) .. +1 (full right).
	xfader float64
	// cuedIndex is the deck the crossfader treats as "right" (spec §2.2). The
	// default pairing is left=0, right=1.
	cuedIndex int
	master    dsp.Stereo
	cue       dsp.Stereo
	limiter   *dsp.Limiter

	// cueMix is the headphone-monitor blend, 0..1: 0 is cue-only, 1 is
	// program-only. A DJ blends between them to cue a track against the
	// running one without losing the running one.
	cueMix float64
	// masterGain scales the output bus. It sits after the crossfader and the
	// channel faders so it is genuinely a master control, and before the
	// limiter so a hot mix is still caught.
	masterGain float64

	// masterLevel and masterPeak feed the snapshot.
	masterLevel float64
	masterPeak  float64
	cueLevel    float64
	cuePeak     float64
}

// NewMixer builds a mixer for count decks with the given block size.
//
// blockFrames must be positive: every buffer below is sized from it, and a
// zero would silently produce a mixer that renders nothing.
func NewMixer(decks []*Deck, blockFrames, rate int) *Mixer {
	if blockFrames < 1 {
		blockFrames = BlockFrames
	}
	if rate <= 0 {
		rate = dsp.SampleRate
	}
	m := &Mixer{
		strips:    make([]*strip, len(decks)),
		limiter:   dsp.NewLimiter(rate, 1.0, 1.0),
		master:    make(dsp.Stereo, blockFrames*2),
		cue:       make(dsp.Stereo, blockFrames*2),
		cuedIndex: 0,
		xfader:    0,
		// Unity defaults. cueMix is program-leaning rather than cue-leaning
		// because a freshly started session should sound like the mix, not like
		// headphones full of the deck the user has not cued yet.
		cueMix:     1,
		masterGain: 1,
		masterPeak: 0,
	}
	for i, d := range decks {
		m.strips[i] = &strip{
			deck: d,
			gain: 0,
			post: make(dsp.Stereo, blockFrames*2),
		}
	}
	return m
}

// SetCuedIndex selects which deck the crossfader treats as right. Out-of-range
// values are ignored: the crossfader must always have a valid pairing.
func (m *Mixer) SetCuedIndex(i int) {
	if i < 0 || i >= len(m.strips) {
		return
	}
	m.cuedIndex = i
}

// Cued reports whether a deck is routed to the headphone bus, which the TUI
// shows as the CUE indicator.
func (m *Mixer) Cued(i int) bool {
	if i < 0 || i >= len(m.strips) {
		return false
	}
	return m.strips[i].cueEnabled
}

// crossfadeAt maps a crossfader position to the constant-power gain pair.
//
// Equal-power (cos/sin) rather than equal-gain matters here: a linear fade
// through the centre audibly dips in level, which reads as a volume drop at
// the exact moment a crossfade should be seamless. At centre, cos and sin are
// both 0.707, so the sum holds constant power.
func crossfadeAt(xf float64) (left, right float64) {
	// x in [-1,1] maps to an angle in [0, pi/2].
	x := (xf + 1) / 2
	if x < 0 {
		x = 0
	}
	if x > 1 {
		x = 1
	}
	theta := x * 1.5707963267948966
	// sin for left (1 at x=0), cos for right (1 at x=1).
	return math.Sin(theta), math.Cos(theta)
}

// mixLocked renders every deck and sums the buses for one block.
//
// The caller MUST hold the engine mutex. The mix reads cueEnabled, trim and
// xfader, which Apply writes from UI and MIDI goroutines, so running unlocked
// would race them.
//
// It writes exactly blockFrames frames and performs no allocation, so holding
// the lock across it costs one block (~11.6ms) and never blocks on anything.
// A control change therefore lands on the next block boundary -- inaudible, and
// the reason the crossfader does not zipper.
func (m *Mixer) mixLocked(outMain, outCue dsp.Stereo) error {
	return m.mixWith(outMain, outCue)
}

func (m *Mixer) mixWith(outMain, outCue dsp.Stereo) error {
	frames := outMain.Frames()
	if outCue.Frames() < frames {
		frames = outCue.Frames()
	}
	// Clear only the region we use; a short ring read must not leave stale
	// samples audible in the tail.
	m.master = m.master[:frames*2]
	m.cue = m.cue[:frames*2]
	clear(m.master)
	clear(m.cue)

	for _, s := range m.strips {
		post := s.post[:frames*2]
		clear(post)
		if n := s.deck.Render(post); n < frames {
			// A deck that rendered short (end of track) leaves the rest zero,
			// which is silence. That is correct: a finished deck plays silence,
			// not its last block repeated.
			_ = n
		}
		s.level = rms(post)
		s.peak = peak(post)
	}

	// Route each strip to its bus by fader position, then to the cue bus if
	// selected. Strips other than the left/cued pair ride at unity on the side
	// they are nearest, so N decks all remain audible.
	leftGain, rightGain := crossfadeAt(m.xfader)
	for i, s := range m.strips {
		var g float64
		switch i {
		case 0:
			g = leftGain
		case m.cuedIndex:
			g = rightGain
		default:
			// Middle decks: nearest side at unity.
			if i < m.cuedIndex {
				g = leftGain
			} else {
				g = rightGain
			}
		}
		post := s.post[:frames*2]
		// trim is a channel fader: 0 means unity, 1 means fully closed. It is
		// applied after the crossfader so the two stay independent controls.
		g *= 1 - s.trim
		for i := range m.master {
			m.master[i] += post[i] * float32(g)
		}
		if s.cueEnabled {
			for i := range m.cue {
				m.cue[i] += post[i]
			}
		}
	}

	// masterGain scales the program bus before the limiter, so pulling it down
	// stops the limiter working as hard on material the DJ already judged to
	// be the right level.
	if m.masterGain != 1 {
		g := float32(m.masterGain)
		for i := range m.master {
			m.master[i] *= g
		}
	}

	// cueMix blends the headphone bus between program and cue. It reads both
	// buses after summing, so a DJ can hear the track they are mixing in
	// against the one they are cueing.
	if m.cueMix > 0 && m.cueMix < 1 {
		pg := float32(1 - m.cueMix)
		cg := float32(m.cueMix)
		for i := range m.cue {
			m.cue[i] = m.cue[i]*cg + m.master[i]*pg
		}
	}

	// The limiter runs on master only. The cue bus is a headphone monitor and
	// must stay unprocessed: a DJ matches by ear, and gain reduction would
	// make that judgement wrong.
	m.limiter.Process(m.master)

	m.masterLevel = rms(m.master)
	m.masterPeak = peak(m.master)
	m.cueLevel = rms(m.cue)
	m.cuePeak = peak(m.cue)

	copy(outMain, m.master)
	copy(outCue, m.cue)
	return nil
}

func rms(s dsp.Stereo) float64 {
	if len(s) == 0 {
		return 0
	}
	var acc float64
	n := len(s) / 2
	for i := 0; i < n; i++ {
		l := float64(s[i*2])
		r := float64(s[i*2+1])
		acc += (l*l + r*r) / 2
	}
	v := acc / float64(n)
	if v <= 0 {
		return 0
	}
	return math.Sqrt(v)
}

func peak(s dsp.Stereo) float64 {
	var p float64
	for _, v := range s {
		a := float64(v)
		if a < 0 {
			a = -a
		}
		if a > p {
			p = a
		}
	}
	return p
}
