package engine

import "sync/atomic"

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
}

type cmdKind uint8

const (
	cmdPlay cmdKind = iota
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
)

// commandQueueSize is a power of two so the index wrap is a mask.
//
// The depth is a deliberate trade. It must absorb a MIDI storm without
// dropping commands, but a control change older than a few blocks is stale: a
// pitch fader position from 100ms ago is not what the user wants. 256 gives
// roughly 3 seconds of blocks at a pathological block rate, far more than any
// real burst, while keeping the backlog bounded and stale-free in practice.
const commandQueueSize = 256

// commandQueue is a bounded lock-free SPSC queue of commands.
type commandQueue struct {
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
	if w-r >= commandQueueSize {
		q.droppedCount.Add(1)
		return
	}
	q.buf[w%(commandQueueSize-1)] = c
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
		fn(q.buf[r%(commandQueueSize-1)])
	}
	q.read.Store(r)
}

// dropped returns the count of commands discarded because the queue was full.
func (q *commandQueue) dropped() uint64 { return q.droppedCount.Load() }

// apply executes one command against the deck. It runs on the render goroutine
// only, so it mutates deck state without any lock.
func (d *Deck) apply(c command) {
	switch c.kind {
	case cmdPlay:
		d.play()
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
		d.filter.SetCutoffNormalized(c.value)
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
