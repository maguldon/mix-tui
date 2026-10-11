package engine

import (
	"sync"
	"sync/atomic"

	"github.com/maguldon/mix-tui/internal/analyze"
	"github.com/maguldon/mix-tui/internal/audio"
	"github.com/maguldon/mix-tui/internal/dsp"
)

// Config parameterises an Engine. The MVP runs two decks, but nothing here
// hardcodes that: deck count is N throughout (spec §2.2).
type Config struct {
	SampleRate  int
	BlockFrames int // 512
	RingFrames  int // ~200ms at SampleRate
	DeckCount   int // 2 in the MVP; N supported
	// CuedIndex is the deck the crossfader treats as "right". The default
	// pairing is left=0, right=1.
	CuedIndex int
}

// withDefaults fills zero fields with their conventional values, so a Config
// written as `Config{DeckCount: 2}` is valid rather than producing a mixer
// that renders nothing.
func (c Config) withDefaults() Config {
	if c.SampleRate <= 0 {
		c.SampleRate = dsp.SampleRate
	}
	if c.BlockFrames <= 0 {
		c.BlockFrames = BlockFrames
	}
	if c.RingFrames <= 0 {
		c.RingFrames = c.SampleRate / 5 // ~200ms
	}
	if c.DeckCount <= 0 {
		c.DeckCount = 2
	}
	if c.CuedIndex == 0 && c.DeckCount > 1 {
		c.CuedIndex = 1
	}
	if c.CuedIndex < 0 || c.CuedIndex >= c.DeckCount {
		c.CuedIndex = 0
	}
	return c
}

// Engine owns all playback state and is the only thing that touches audio.
//
// Actions arrive from the UI and MIDI goroutines; RenderInto is called from
// the audio goroutine. They are deliberately decoupled: Apply takes the mutex,
// RenderInto does not hold it while mixing.
type Engine struct {
	// mu guards deck control state and mixer configuration. It is NOT held
	// during a mix — see RenderInto.
	mu    sync.Mutex
	decks []*Deck
	mixer *Mixer
	cfg   Config

	// ringOverruns counts renders the consumer could not keep up with.
	ringOverruns atomic.Uint64

	// scratch buffers for one rendered block. Preallocated: RenderInto runs on
	// the audio goroutine and must not allocate (spec §4.3).
	scratchMain dsp.Stereo
	scratchCue  dsp.Stereo

	// recorder captures the mixed MAIN block for offline comparison. Nil
	// unless SetRecorder was called, so the production device path pays
	// nothing: recording is the -dump verification bridge, not a runtime
	// feature. Guarded by recMu because it is set before the pump starts but
	// read on every render.
	recMu    sync.Mutex
	recorder Recorder
}

// Recorder captures rendered audio for later comparison against an offline
// render. Implementations receive a block that RenderInto is finished with and
// must copy anything they intend to keep: the buffer is reused next block.
type Recorder interface {
	RecordMain(block dsp.Stereo)
}

// SetRecorder installs a recorder, or nil to remove one.
func (e *Engine) SetRecorder(r Recorder) {
	e.recMu.Lock()
	e.recorder = r
	e.recMu.Unlock()
}

// New builds an engine with cfg's deck count.
func New(cfg Config) *Engine {
	cfg = cfg.withDefaults()
	e := &Engine{cfg: cfg}
	e.decks = make([]*Deck, cfg.DeckCount)
	for i := range e.decks {
		e.decks[i] = NewDeckAtRate(i, cfg.SampleRate)
	}
	e.mixer = NewMixer(e.decks, cfg.BlockFrames, cfg.SampleRate)
	e.mixer.SetCuedIndex(cfg.CuedIndex)
	e.scratchMain = make(dsp.Stereo, cfg.BlockFrames*2)
	e.scratchCue = make(dsp.Stereo, cfg.BlockFrames*2)
	return e
}

// Deck returns deck i, or nil when i is out of range. Callers in the audio
// path rely on the nil check; callers that already validated may assume
// non-nil.
func (e *Engine) Deck(i int) *Deck {
	if i < 0 || i >= len(e.decks) {
		return nil
	}
	return e.decks[i]
}

// DeckCount returns the number of decks.
func (e *Engine) DeckCount() int { return len(e.decks) }

// Config returns a copy of the engine's configuration.
func (e *Engine) Config() Config { return e.cfg }

// Apply dispatches an action.
//
// Every action against an unloaded or out-of-range deck returns silently: a
// keypress arriving before a track finishes loading is normal, not an error
// worth surfacing to the user (spec §5).
func (e *Engine) Apply(a Action) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if a.Target < 0 || a.Target >= len(e.decks) {
		return
	}
	d := e.decks[a.Target]

	switch a.Kind {
	case DeckPlay:
		d.Play()
	case DeckCue:
		// Cue is momentary-cue-to-headphones, but stopping is the observable
		// behaviour of a cue button on a deck with no separate cue source.
		d.Stop()
	case DeckSeek:
		// Value is normalized 0..1 of the track's duration.
		d.SeekNormalized(a.Value)
	case DeckPitch:
		d.SetPitch(a.Value)
	case DeckEQLow:
		d.SetEQBand(0, gainFromNormalized(a.Value))
	case DeckEQMid:
		d.SetEQBand(1, gainFromNormalized(a.Value))
	case DeckEQHigh:
		d.SetEQBand(2, gainFromNormalized(a.Value))
	case DeckFilterCutoff:
		d.SetFilterCutoff(a.Value)
	case DeckFilterReso:
		d.SetFilterResonance(a.Value)
	case DeckFilterKind:
		d.SetFilterKind(a.Value >= 0)
	case DeckHotcueSet:
		d.SetHotcue(a.Slot)
	case DeckHotcueJump:
		d.JumpToHotcue(a.Slot)
	case DeckHotcueDelete:
		d.DeleteHotcue(a.Slot)
	case DeckCueAssign:
		if s := e.mixer.strip(a.Target); s != nil {
			s.cueEnabled = a.Value != 0
		}
	case MixerChannelGain:
		if s := e.mixer.strip(a.Target); s != nil {
			s.trim = clampUnit(a.Value)
		}
	case MixerCrossfader:
		e.mixer.xfader = clampUnit(a.Value)

	// Sync, quantize, and nudge are beat-dependent and go through the same
	// command queue as everything else, so they cannot mutate a deck while
	// the render goroutine is reading it.
	// Sync and SyncToggle both engage/disengage on a press. A momentary button
	// sends a zero Value, so requiring a non-zero Value to engage would make a
	// plain keypress disable instead — the opposite of what the control means.
	case Sync:
		if d.SyncActive() {
			d.DisableSync()
		} else {
			_ = e.SyncPairLocked(a.Target, e.masterForLocked(a.Target))
		}
	case SyncToggle:
		if d.SyncActive() {
			d.DisableSync()
		} else {
			_ = e.SyncPairLocked(a.Target, e.masterForLocked(a.Target))
		}
	case Quantize:
		_ = d.QuantizeToNearestBeat()
	case NudgeBeats:
		_ = d.NudgeBeats(a.Value)
	case LoopBeat:
		_ = d.SetLoopBeat(loopBeatsFromNormalized(a.Value))
	case LoopIn:
		_ = d.SetLoopIn()
	case LoopOut:
		_ = d.SetLoopOut()
	case LoopRelease:
		d.ReleaseLoop()
	}
}

// SyncPair locks slave's tempo to master's. It is the programmatic entry
// point; the TUI and mixrender both go through it.
//
// Both decks must carry an analysed beatgrid. Sync is unavailable without one
// rather than approximate, because a wrong tempo match is worse than none
// (spec §5.6).
func (e *Engine) SyncPair(slave, master int) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.SyncPairLocked(slave, master)
}

// SyncPairLocked is SyncPair with e.mu already held. Callers inside Apply use
// it to avoid re-entering the mutex.
func (e *Engine) SyncPairLocked(slave, master int) error {
	if slave < 0 || slave >= len(e.decks) || master < 0 || master >= len(e.decks) {
		return analyze.ErrNoBeatGrid
	}
	return e.decks[slave].EngageSync(e.decks[master])
}

// masterForLocked returns the deck a sync action should treat as master, or -1
// when the slave is the cued deck and no other candidate exists.
// Callers hold e.mu.
func (e *Engine) masterForLocked(slave int) int {
	if m := e.cfg.CuedIndex; m != slave {
		return m
	}
	// The cued deck cannot be its own master. With two decks the other one is
	// the obvious choice; with more, take the lowest-index alternative.
	for i := 0; i < len(e.decks); i++ {
		if i != slave {
			return i
		}
	}
	return -1
}

// strip returns mixer strip i, or nil. Callers hold e.mu.
func (m *Mixer) strip(i int) *strip {
	if i < 0 || i >= len(m.strips) {
		return nil
	}
	return m.strips[i]
}

// RenderInto renders one block from every deck through the mixer into the two
// output rings.
//
// It is called from the audio goroutine and never takes e.mu for the mix.
// Apply may run concurrently on a MIDI goroutine; the only shared state is
// per-deck control state, which Deck guards itself. Holding the engine lock
// across the mix would let a single Apply call stall the audio thread for a
// whole block — the exact failure spec §3.2 forbids.
func (e *Engine) RenderInto(main, cue *audio.Ring) error {
	if main == nil || cue == nil {
		return nil
	}
	// Snapshot the scratch buffers under the lock, then release it before
	// mixing. The mix itself reads s.cueEnabled directly, so there is no
	// routing snapshot to take here.
	//
	// There WAS one: it copied s.cueEnabled into a local under the lock and
	// wrote it straight back outside it, racing Apply's locked write on the
	// same address. The copy was a no-op by construction — mix reads the
	// field, not the local — so removing it removes the race and the
	// allocation alike.
	e.mu.Lock()
	mainBuf := e.scratchMain
	cueBuf := e.scratchCue
	e.mu.Unlock()

	if err := e.mixer.mix(mainBuf, cueBuf); err != nil {
		return err
	}

	// Tee the mixed MAIN block to the recorder, if one is installed. This runs
	// on the pump goroutine, not the realtime drain, so a recorder that
	// allocates is acceptable here — but never on the drain thread.
	e.recMu.Lock()
	rec := e.recorder
	e.recMu.Unlock()
	if rec != nil {
		rec.RecordMain(mainBuf)
	}
	// A short write means the ring was FULL, which is the opposite failure to
	// the underrun the Ring itself counts (a read finding it empty). The
	// render goroutine is supposed to run ahead; if the ring fills, something
	// downstream stopped consuming, and that needs its own visible cause
	// rather than being folded into the same number.
	//
	// Ring.Write returns FRAMES while the buffer length is in SAMPLES, so the
	// comparison must convert. Getting this wrong counts every healthy write
	// as an overrun, because a complete 512-frame write returns 512 and the
	// 1024-sample buffer compares as "short".
	if n := main.Write(mainBuf); n*2 < len(mainBuf) {
		e.ringOverruns.Add(1)
	}
	if n := cue.Write(cueBuf); n*2 < len(cueBuf) {
		e.ringOverruns.Add(1)
	}
	return nil
}

// Snapshot returns a consistent copy of the engine state for the UI.
//
// This allocates and takes the engine mutex, which is why the UI reads state
// through here and never through Deck directly (spec §5).
func (e *Engine) Snapshot() Snapshot {
	e.mu.Lock()
	defer e.mu.Unlock()

	s := Snapshot{
		Decks:      make([]DeckState, len(e.decks)),
		Crossfader: e.mixer.xfader,
	}
	for i, d := range e.decks {
		st := &s.Decks[i]
		st.Index = i
		st.Playing = d.IsPlaying()
		st.Position = d.Pos()
		st.Duration = d.Duration()
		st.Pitch = d.Pitch()
		st.HasBeatGrid = d.HasBeatGrid()
		ls := d.LoopSummary()
		st.Cued = e.mixer.Cued(i)
		st.PCM = d.PCM()
		st.Peaks, st.PeaksWidth = d.Peaks()
		if g := d.BeatGrid(); g != nil {
			st.BeatTimes = g.Beats
		}
		for slot := 1; slot <= numHotcues; slot++ {
			if at, ok := d.Hotcue(slot); ok {
				st.CueTimes[st.CueCount] = at
				st.CueCount++
			}
		}
		st.LoopActive = ls.Active
		st.LoopBeatCount = ls.BeatCount
		st.LoopStart = ls.Start
		st.LoopEnd = ls.End
		if k := d.Key(); k != nil {
			// Below the threshold analyze leaves Camelot empty, but gate here
			// too so a low-confidence key can never reach the view even if a
			// caller attaches one directly.
			if k.Confidence >= analyze.KeyConfidenceThreshold {
				st.KeyCamelot = k.Camelot
				st.KeyConfidence = k.Confidence
			}
		}
		if g := d.BeatGrid(); g != nil && st.HasBeatGrid {
			st.BPM = g.BPM
		}
		st.Loaded = d.HasTrack()
		if t := d.Track(); t != nil {
			st.Path = t.Path
			st.Title = t.Title
			st.Artist = t.Artist
		}
		if m := e.mixer.strip(i); m != nil {
			st.Level = m.level
			st.Peak = m.peak
		}
	}
	s.MasterLevel = e.mixer.masterLevel
	s.MasterPeak = e.mixer.masterPeak
	s.CueLevel = e.mixer.cueLevel
	s.CuePeak = e.mixer.cuePeak
	s.Overruns = e.ringOverruns.Load()
	return s
}

// Overruns returns the cumulative count of renders dropped because a ring was
// full.
func (e *Engine) Overruns() uint64 { return e.ringOverruns.Load() }

func clampUnit(v float64) float64 {
	if v < -1 {
		return -1
	}
	if v > 1 {
		return 1
	}
	return v
}

// gainFromNormalized converts a -1..1 knob position to a linear EQ gain where
// 0 is unity. The halves are asymmetric in dB but symmetric in knob travel,
// which is how a real channel strip feels.
func gainFromNormalized(v float64) float64 {
	if v < 0 {
		return 1 + v*3 // unity down to -12dB
	}
	return 1 + v*3 // unity up to +12dB
}
