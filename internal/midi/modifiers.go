package midi

import (
	"sync"
	"sync/atomic"

	"github.com/maguldon/mix-tui/internal/dsp"
	"github.com/maguldon/mix-tui/internal/engine"
)

// Handler turns one message plus held state into Actions. It is the top of the
// pure input path and the only thing the device layer calls.
//
// The table is held in an atomic pointer so hot reload swaps it with a single
// store that a concurrent Handle cannot observe half-applied. The remaining
// mutable state (held modifiers, encoder positions, toggles) is guarded by a
// mutex: Handle is called from a single device-read goroutine in production, but
// SetTable and ActiveMods can come from the UI thread, and the encoder map is
// read-modify-written per message.
type Handler struct {
	table atomic.Pointer[Table]

	mu      sync.Mutex
	mods    ModSet
	latched map[string]bool
	toggled map[string]bool
	// encoder is the accumulated position per relative control.
	encoder map[msgKey]float64
	// toggleState is the on/off parity per momentary-toggle control.
	toggleState map[msgKey]bool

	onAction func(engine.Action)
	counts   Counts
}

// NewHandler returns a Handler driving onAction for every resolved message.
func NewHandler(table *Table, onAction func(engine.Action)) *Handler {
	h := &Handler{
		mods:        ModSet{},
		latched:     make(map[string]bool),
		toggled:     make(map[string]bool),
		encoder:     make(map[msgKey]float64),
		toggleState: make(map[msgKey]bool),
		onAction:    onAction,
	}
	if table != nil {
		h.table.Store(table)
	}
	return h
}

// SetTable swaps the mapping atomically. The next Handle uses the new table.
func (h *Handler) SetTable(t *Table) { h.table.Store(t) }

// ActiveMods returns a copy of the held-modifier set for display.
func (h *Handler) ActiveMods() ModSet {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make(ModSet, len(h.mods))
	for k, v := range h.mods {
		out[k] = v
	}
	return out
}

// Counts returns a snapshot of the ignored/unmapped tallies.
func (h *Handler) Counts() map[string]uint64 { return h.counts.Snapshot() }

// Handle processes one message and emits zero or more Actions. It returns an
// error when the message was ignored (an Other category) or was well-formed but
// unmapped, so the caller can log once per category rather than per message.
func (h *Handler) Handle(m Message) error {
	table := h.table.Load()

	// Count ignored categories first: a message outside the CC/note vocabulary
	// is tallied and reported, and never reaches resolution (spec §7).
	if err := h.counts.Record(m); err != nil {
		return err
	}

	// A modifier message updates state and returns; it must not also fire
	// whatever control happens to share its CC.
	if table != nil {
		if name, src, ok := table.matchModifier(m); ok {
			h.applyModifier(name, src, m)
			return nil
		}
	}

	// Resolve against the currently held modifiers.
	h.mu.Lock()
	held := h.mods
	h.mu.Unlock()

	var c Control
	var bound bool
	if table != nil {
		c, bound = table.Resolve(held, m)
	}
	if !bound {
		// Count CC/note as unmapped; KindOther was already counted above.
		if m.Kind == KindCC || m.Kind == KindNote {
			h.counts.RecordUnmapped(m)
			return &ErrUnmapped{Msg: m}
		}
		return nil
	}

	key := msgKey{Kind: c.Kinds, Chan: c.Chan, Num: c.Num}
	h.mu.Lock()
	var value float64
	switch c.Mode {
	case ModeRelative:
		h.encoder[key] += RelativeDelta(m.Value)
		value = h.encoder[key]
	case ModeToggle:
		// Flip parity on each message and emit the new state.
		h.toggleState[key] = !h.toggleState[key]
		if h.toggleState[key] {
			value = 1
		} else {
			value = 0
		}
	case ModeMomentary:
		// Press emits 1, release emits 0, so a held control reads as active.
		if m.Kind == KindNote {
			if m.Velocity > 0 {
				value = 1
			} else {
				value = 0
			}
		} else {
			if m.Value > 0 {
				value = 1
			} else {
				value = 0
			}
		}
	default: // ModeAbsolute
		value = Absolute(m.Value)
	}
	h.mu.Unlock()

	// Every emitted value is clamped regardless of source (spec §5): a MIDI knob
	// sending an out-of-range value must not drive a deck past its limits.
	value = dsp.ClampNormalized(value)

	action := engine.Action{Kind: c.Action, Target: c.Target, Slot: c.Slot, Value: value}
	if h.onAction != nil {
		h.onAction(action)
	}
	return nil
}

// applyModifier updates the held set according to the modifier's declared mode.
func (h *Handler) applyModifier(name string, src ModSource, m Message) {
	h.mu.Lock()
	defer h.mu.Unlock()
	switch src.ModMode {
	case ModToggleMod:
		// Review focus #3: note-off must not disengage a toggled modifier. Only
		// a press (note-on, or a CC reaching the pressed value) flips parity.
		if m.Kind == KindNote && m.Velocity == 0 {
			return
		}
		if h.toggled[name] {
			h.mods = h.mods.Without(name)
			h.toggled[name] = false
		} else {
			h.mods = h.mods.With(name)
			h.toggled[name] = true
		}
	case ModLatching:
		// The controller reports a press; we track our own parity so the state
		// is stable and a note-off cannot clear it.
		if m.Kind == KindNote && m.Velocity == 0 {
			return
		}
		if h.latched[name] {
			h.mods = h.mods.Without(name)
			h.latched[name] = false
		} else {
			h.mods = h.mods.With(name)
			h.latched[name] = true
		}
	default: // ModMomentary — follow the wire.
		if m.Kind == KindNote {
			if m.Velocity > 0 {
				h.mods = h.mods.With(name)
			} else {
				h.mods = h.mods.Without(name)
			}
		} else {
			if m.Value == src.Pressed {
				h.mods = h.mods.With(name)
			} else {
				h.mods = h.mods.Without(name)
			}
		}
	}
}

// matchModifier reports whether a message is the source for a declared
// modifier. A CC modifier matches on channel+number; a note modifier on
// channel+number too (any velocity).
func (t *Table) matchModifier(m Message) (string, ModSource, bool) {
	for name, src := range t.mods {
		if m.Kind != src.Kind || m.Channel != src.Chan || m.Num != src.Num {
			continue
		}
		return name, src, true
	}
	return "", ModSource{}, false
}
