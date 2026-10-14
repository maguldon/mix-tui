package engine

import (
	"sync/atomic"

	"github.com/maguldon/mix-tui/internal/analyze"
	"github.com/maguldon/mix-tui/internal/audio"
	"github.com/maguldon/mix-tui/internal/dsp"
)

// The render goroutine is the ONLY writer of a Deck's audio state. Apply runs
// on UI and MIDI goroutines and must not touch that state directly, because
// Render reads it concurrently and the audio thread may not block (spec §4.3).
//
// So Apply does not mutate the deck. It posts a command to a lock-free SPSC
// queue, and renderBlock drains the queue and applies the commands at the top
// of a block, before touching the resampler.
//
// This is the same single-owner shape as audio.Ring: the producer owns the
// write index, the consumer owns the read index, and neither needs a lock.
// The alternative — a mutex around Deck — would put a lock on the audio path,
// which the spec forbids outright.
//
// A consequence worth being explicit about: a control change takes effect at
// the start of the next block, up to BlockFrames (about 11.6ms) later. That is
// inaudible for a pitch fader and imperceptible for a transport, and it buys
// a control surface that cannot stall the audio thread.

// command is one control change, posted by Apply and applied by renderBlock.
type command struct {
	kind  cmdKind
	value float64
	slot  int
	// track is set only for cmdLoad. Pointers in a queue are fine because the
	// decoded track is immutable once handed over.
	track *audio.Track
	// beatgrid and key travel with the track so a load is atomic: a deck must
	// never briefly hold new audio with the previous track's analysis.
	beatgrid *analyze.BeatGrid
	key      *analyze.Key
	// grid is set only for cmdAnalysis; key is shared with cmdLoad.
	grid *analyze.BeatGrid
}

type cmdKind uint8

const (
	cmdPlay cmdKind = iota
	cmdPlayToggle
	cmdStop
	cmdPitch
	cmdSeekNorm
	cmdHotcueSet
	cmdHotcueJump
	cmdHotcueDelete
	cmdEQLow
	cmdEQMid
	cmdEQHigh
	cmdFilterCutoff
	cmdFilterReso
	cmdFilterKind
	cmdFilterCutoffHz
	// cmdLoad replaces the deck's audio. It travels through the queue like every
	// other deck mutation because Load touches render-owned state (the
	// resampler, the filter, the playhead). Calling it from the TUI goroutine
	// while the pump renders is a data race.
	cmdLoad
	// cmdAnalysis carries a background analysis result to the deck. It is a
	// command rather than a direct write because analysis finishes long after
	// the load, while the pump is rendering.
	cmdAnalysis
	// cmdNudge moves the playhead by a fraction of a second.
	cmdNudge
)

// commandQueueSize is a power of two so the index wrap is a MASK, not a
// modulo.
//
// It was previously used as commandQueueSize-1, which silently made the ring
// one slot smaller than the fullness check allowed: the 256th outstanding
// command was written to the slot the reader was about to consume, destroying
// the oldest unread command and racing the reader, while droppedCount — the
// counter whose whole purpose is to make a glitch observable — stayed at zero.
//
// The depth is a deliberate trade. It must absorb a MIDI storm without dropping
// commands, but a control change older than a few blocks is stale. 256 gives
// roughly three seconds of blocks at a pathological block rate, far more than
// any real burst, while keeping the backlog bounded.
const commandQueueSize = 256

// commandQueueMask is the wrap mask. With a power-of-two size the ring holds
// commandQueueSize-1 entries: a full ring cannot be told from an empty one
// without a spare slot, and using commandQueueSize for the count would make
// the writer lap the reader.
const commandQueueMask = commandQueueSize - 1

// commandQueue is a bounded lock-free SPSC queue of commands.
type commandQueue struct {
	// The array is commandQueueSize entries while the usable count is
	// commandQueueMask: the mask wraps an index across the full array, so
	// sizing the array to the mask rather than the size left one slot short and
	// w&mask could index past the end. The ring holds mask entries so a full
	// ring is distinguishable from an empty one; it needs the full size to
	// address them.
	buf   [commandQueueSize]command
	write atomic.Uint32
	read  atomic.Uint32
	// droppedCount counts commands lost to a full queue. Exposed through
	// Snapshot so a glitch has an observable cause rather than being mysterious.
	droppedCount atomic.Uint64
}

// post enqueues a command without blocking. A full queue drops the oldest
// command rather than blocking the caller: Apply may be running on a MIDI
// goroutine or in the TUI event loop, and neither may ever wait on audio.
func (q *commandQueue) post(c command) {
	w := q.write.Load()
	r := q.read.Load()
	if w-r >= uint32(commandQueueMask) {
		// Full. Drop the NEWEST rather than overwrite the oldest: the reader
		// may already be reading that slot, and clobbering it would both lose
		// a command and race.
		q.droppedCount.Add(1)
		return
	}
	q.buf[w&uint32(commandQueueMask)] = c
	q.write.Store(w + 1)
}

// drain applies every queued command through fn, then returns.
//
// It only ever runs on the render goroutine, which is the sole reader, so it
// can read the write index without synchronization beyond the atomic load.
func (q *commandQueue) drain(fn func(command)) {
	r := q.read.Load()
	w := q.write.Load()
	for ; r != w; r++ {
		fn(q.buf[r&uint32(commandQueueMask)])
	}
	q.read.Store(r)
}

// dropped returns the count of commands discarded because the queue was full.
func (q *commandQueue) dropped() uint64 { return q.droppedCount.Load() }

// apply executes one command against the deck. It runs on the render goroutine
// only, so it mutates deck state without any lock.
func (d *Deck) apply(c command) {
	switch c.kind {
	case cmdNudge:
		d.nudgeSeconds(c.value)
	case cmdAnalysis:
		d.applyAnalysis(c.grid, c.key)
	case cmdLoad:
		d.applyLoad(c.track, c.beatgrid, c.key)

	case cmdPlay:
		d.play()
	case cmdPlayToggle:
		d.togglePlay()
	case cmdStop:
		d.stop()
	case cmdPitch:
		d.setPitch(c.value)
	case cmdSeekNorm:
		d.seekNormalized(c.value)
	case cmdHotcueSet:
		d.setHotcue(c.slot)
	case cmdHotcueJump:
		d.jumpToHotcue(c.slot)
	case cmdHotcueDelete:
		d.deleteHotcue(c.slot)
	case cmdEQLow:
		d.eq.SetBand(0, gainFromNormalized(c.value))
	case cmdEQMid:
		d.eq.SetBand(1, gainFromNormalized(c.value))
	case cmdEQHigh:
		d.eq.SetBand(2, gainFromNormalized(c.value))
	case cmdFilterCutoff:
		d.filterNorm = c.value
		d.filter.SetCutoffNormalized(c.value)
	case cmdFilterCutoffHz:
		// The Hz-spelled path, for SetFilter's public API. The normalized knob
		// path above is what the UI and MIDI send, because that is what a knob
		// has; SetFilter takes Hz, which is not the same thing -- the knob is
		// logarithmic, so passing Hz straight to SetCutoffNormalized would
		// retune every filter by a curve.
		d.filterNorm = dsp.NormalizedCutoff(c.value)
		d.filter.SetParams(c.value, d.filter.Resonance())
	case cmdFilterReso:
		d.filter.SetResonance(c.value)
	case cmdFilterKind:
		d.filter.SetKind(c.value >= 0)
	}
}

// drainCommands applies everything Apply posted since the last block.
func (d *Deck) drainCommands() {
	d.pending.drain(d.apply)
}

// DroppedCommands returns the number of control changes discarded because they
// arrived faster than the render loop consumed them.
func (d *Deck) DroppedCommands() uint64 { return d.pending.dropped() }

// post enqueues a control change for the next block.
func (d *Deck) post(c command) { d.pending.post(c) }

// PostControl enqueues an arbitrary control change. Task 8's loop and sync
// features use this to schedule work onto the render goroutine.
func (d *Deck) PostControl(kind ActionKind, value float64, slot int) {
	d.pending.post(command{kind: controlKind(kind), value: value, slot: slot})
}

// controlKind maps an ActionKind onto its command. Unmapped actions return a
// zero-kind command, which apply ignores.
func controlKind(k ActionKind) cmdKind {
	switch k {
	case DeckPlay:
		return cmdPlay
	case DeckPlayPause:
		return cmdPlayToggle
	case DeckCue:
		return cmdStop
	case DeckPitch:
		return cmdPitch
	case DeckSeek:
		return cmdSeekNorm
	case DeckEQLow:
		return cmdEQLow
	case DeckEQMid:
		return cmdEQMid
	case DeckEQHigh:
		return cmdEQHigh
	case DeckFilterCutoff:
		return cmdFilterCutoff
	case DeckFilterReso:
		return cmdFilterReso
	case DeckFilterKind:
		return cmdFilterKind
	case DeckHotcueSet:
		return cmdHotcueSet
	case DeckHotcueJump:
		return cmdHotcueJump
	case DeckHotcueDelete:
		return cmdHotcueDelete
	}
	return cmdKind(255)
}
