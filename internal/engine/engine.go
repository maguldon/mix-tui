package engine

import (
	"sync"
	"sync/atomic"
	"time"

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
// the audio goroutine. Both take mu: Apply to write a control change, RenderInto
// to read the controls the mix needs. See RenderInto for why the mix runs under
// the lock and why that is still realtime-safe.
type Engine struct {
	// mu guards deck control state and mixer configuration. RenderInto holds
	// it across the mix.
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
// SetCueStore installs the store every deck uses to remember hotcues and saved
// loops per track (spec §10). Call it before anything is loaded: the restore
// happens as part of a load, so a store installed afterwards applies to the next
// track rather than the current one.
//
// A nil store disables persistence, which is what the renderer and the tests
// want -- they load fixtures repeatedly and must not accumulate state between
// runs.
func (e *Engine) SetCueStore(s *CueStore) {
	for _, d := range e.decks {
		d.SetCueStore(s)
	}
}

func (e *Engine) Deck(i int) *Deck {
	if i < 0 || i >= len(e.decks) {
		return nil
	}
	return e.decks[i]
}

// DeckCount returns the number of decks.
func (e *Engine) DeckCount() int { return len(e.decks) }

// ApplyAnalysis attaches a background analysis result to a deck.
//
// Grid and key travel as one command, so a deck never shows a BPM whose beats
// have not arrived yet. It is safe to call while audio is running: the result
// is queued and applied at the next block boundary.
func (e *Engine) ApplyAnalysis(deck int, grid *analyze.BeatGrid, key *analyze.Key) {
	d := e.Deck(deck)
	if d == nil {
		return
	}
	d.post(command{kind: cmdAnalysis, grid: grid, key: key})
}

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
	case DeckPlayPause:
		// Toggle, not Play: this is the key and pad that starts and stops a
		// deck, and Play() would leave a rolling deck rolling.
		d.TogglePlay()
	case DeckCue:
		// Cue is momentary-cue-to-headphones, but stopping is the observable
		// behaviour of a cue button on a deck with no separate cue source.
		d.Stop()
	case DeckSeek:
		// Value is normalized 0..1 of the track's duration.
		d.SeekNormalized(a.Value)
	case DeckPitch:
		d.SetPitch(a.step(d.Pitch(), StepSize))
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
		_ = d.SetLoopBeat(loopBeatsForAction(a.Value))
	case LoopIn:
		_ = d.SetLoopIn()
	case LoopOut:
		_ = d.SetLoopOut()
	case LoopRelease:
		d.ReleaseLoop()
	case DeckNudge:
		// Value is seconds. A nudge nudges the playhead; it is distinct from
		// NudgeBeats, which is the same idea expressed in musical time and so
		// needs a beatgrid.
		d.NudgeSeconds(a.Value)
	case LoopSave:
		_ = d.SaveLoop(a.Slot)
	case LoopRecall:
		_ = d.RecallLoop(a.Slot)
	case LoopRoll:
		// Momentary: a press rolls (PushRoll), a release returns (PopRoll).
		// The MIDI layer sends Value 0 on release, which is how a momentary
		// control reaches here. The default keymap binds "r", and until this
		// existed pressing it did nothing at all.
		if a.Value > 0 {
			_ = d.PushRoll(a.Value)
		} else {
			_ = d.PopRoll()
		}
	case MixerCueMix:
		e.mixer.cueMix = clampUnit(a.Value)
	case MixerMasterGain:
		e.mixer.masterGain = clampUnit(a.step(e.mixer.masterGain, StepSize))
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
// It is called from the audio goroutine and holds e.mu across the mix. Apply
// runs concurrently from UI and MIDI goroutines and takes the same mutex, so
// holding it is what makes the read well-defined rather than a race.
//
// Why the lock is held: mix reads cueEnabled, trim and xfader, and Apply writes
// all three. Releasing the lock first left the audio thread reading values
// while the control thread wrote them, which the race detector reported and
// which made the values read undefined rather than merely stale.
//
// Why it is still safe for realtime: the mix is non-blocking and allocates
// nothing, so it holds the lock for the length of one block (~11.6ms). A
// control action therefore lands on the next block boundary, which is
// inaudible -- and is the reason the crossfader does not zipper. Nothing called
// from the mix path re-enters Apply or Snapshot, so the lock cannot be
// re-entered; see mixLocked.
func (e *Engine) RenderInto(main, cue *audio.Ring) error {
	if main == nil || cue == nil {
		return nil
	}
	e.mu.Lock()
	mainBuf := e.scratchMain
	cueBuf := e.scratchCue
	err := e.mixer.mixLocked(mainBuf, cueBuf)
	e.mu.Unlock()
	if err != nil {
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

// otherDeck returns the deck that is not i, for beat-phase comparison.
func (e *Engine) otherDeck(i int) *Deck {
	for j, d := range e.decks {
		if j != i {
			return d
		}
	}
	return nil
}

// Snapshot returns a consistent copy of the engine state for the UI.
//
// This allocates and takes the engine mutex, which is why the UI reads state
// through here and never through Deck directly (spec §5).
// savedLoopSlots copies a deck's stored loops into Snapshot form.
//
// Deck.Loop() already had them; the Snapshot did not, so the view could not
// tell an occupied slot from an empty one. The copy matters because Loop()
// allocates a map per call and the Snapshot is taken 30 times a second.
func savedLoopSlots(d *Deck) map[int]TimeRange {
	if len(d.savedLoop) == 0 {
		return nil
	}
	out := make(map[int]TimeRange, len(d.savedLoop))
	for slot, l := range d.savedLoop {
		out[slot] = TimeRange{
			Start: d.framesToDuration(l.Start),
			End:   d.framesToDuration(l.End),
		}
	}
	return out
}

// deckFault reports whether a deck is holding audio it cannot play.
//
// The cases are the ones a user cannot otherwise diagnose: the deck renders a
// title, a BPM and a waveform, so it looks loaded, while the file it was decoded
// from has since gone, or the track has run to its end. Without a marker the
// panel is simply silent and the transport reads as though nothing is selected.
func deckFault(d *Deck) (bool, string) {
	if d.track == nil {
		return false, ""
	}
	if d.track.PCM == nil || len(d.track.PCM) == 0 {
		return true, "no audio data"
	}
	if d.pos >= float64(d.track.PCM.Frames()) && !d.loop.active {
		return true, "track finished"
	}
	return false, ""
}

// downbeatTimes resolves a grid's Downbeats -- which are indices into Beats --
// into absolute times, so the view can draw bar lines without knowing the
// indexing scheme. Out-of-range indices are dropped rather than panicking: a
// grid whose beat list was shortened still has valid downbeats among them.
func downbeatTimes(g *analyze.BeatGrid) []time.Duration {
	if g == nil || len(g.Downbeats) == 0 {
		return nil
	}
	out := make([]time.Duration, 0, len(g.Downbeats))
	for _, i := range g.Downbeats {
		if i < 0 || i >= len(g.Beats) {
			continue
		}
		out = append(out, g.Beats[i])
	}
	return out
}

func (e *Engine) Snapshot() Snapshot {
	e.mu.Lock()
	defer e.mu.Unlock()

	s := Snapshot{
		Decks:      make([]DeckState, len(e.decks)),
		Crossfader: e.mixer.xfader,
		// The control SETTINGS, not the meters. The UI needs these to show the
		// user where the faders are; without them master gain and the cue blend
		// move the audio while the interface reports nothing.
		MasterGain: e.mixer.masterGain,
		CueMix:     e.mixer.cueMix,
	}
	for i, d := range e.decks {
		st := &s.Decks[i]
		st.Index = i
		st.Playing = d.IsPlaying()
		st.Position = d.Pos()
		st.Duration = d.Duration()
		st.Pitch = d.Pitch()
		st.HasBeatGrid = d.HasBeatGrid()
		st.SampleRate = d.SampleRate()
		ls := d.LoopSummary()
		st.Cued = e.mixer.Cued(i)
		st.Synced = d.IsSynced()
		for b := 0; b < 3; b++ {
			st.EQ[b] = d.EQPosition(b)
		}
		st.FilterCutoffNorm = d.FilterCutoffNorm()
		st.FilterHighpass = d.FilterHighpass()
		st.PCM = d.PCM()
		st.Peaks, st.PeaksWidth = d.Peaks()
		if g := d.BeatGrid(); g != nil {
			st.BeatTimes = g.Beats
			st.DownbeatTimes = downbeatTimes(g)
			st.Meter = g.Meter
		}
		// Slots 0..numHotcues-1. This ran from 1 to numHotcues inclusive, which
		// skipped slot 0 -- the slot the "1" key sets -- and asked Hotcue for
		// slot 8, which does not exist and always reports false. So a hotcue set
		// with the first key was invisible in every view that draws cue markers,
		// while being perfectly recallable by the key that set it.
		for slot := 0; slot < numHotcues; slot++ {
			if at, ok := d.Hotcue(slot); ok {
				st.CueTimes[st.CueCount] = at
				st.CueCount++
			}
		}
		// The engine computes beat phase against the real grids; the view must
		// not re-derive it from position-modulo-beat, which assumes a beat
		// lands at t==0 and is therefore wrong for any detected grid.
		if other := e.otherDeck(i); other != nil {
			if d, err := d.DeltaBeat(other); err == nil {
				st.DeltaMS = float64(d) / float64(time.Millisecond)
				st.HasDelta = true
			}
		}
		st.LoopActive = ls.Active
		st.LoopBeatCount = ls.BeatCount
		st.LoopStart = ls.Start
		st.LoopEnd = ls.End
		st.SavedLoopSlots = savedLoopSlots(d)
		st.Faulted, st.FaultReason = deckFault(d)
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
