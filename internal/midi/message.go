package midi

import (
	"fmt"

	"github.com/maguldon/mix-tui/internal/engine"
)

// MsgKind classifies an input message into the three shapes the control layer
// reasons about.
type MsgKind int

const (
	KindCC MsgKind = iota
	KindNote
	KindOther // everything we ignore but still count
)

// OtherCategory names a message class outside the CC/note vocabulary. Each gets
// its own counter so the debug overlay can show that, for example, a controller
// is streaming clock the application deliberately ignores (spec §7). Silently
// dropping them is how a misconfigured setup stays misconfigured.
type OtherCategory int

const (
	OtherSysex OtherCategory = iota
	OtherProgramChange
	OtherPitchBend
	OtherAftertouch
	OtherClock
	OtherTransport
	NumOtherCategories
)

// Message is the hardware-independent input event. No gomidi type crosses into
// any package that makes decisions; the device layer converts once, at the edge,
// so the resolver, modifiers, and encoder logic are testable from literal
// tables with no controller attached.
type Message struct {
	Kind    MsgKind
	Channel uint8 // 0-15
	Num     uint8 // CC number or note number
	Value   uint8 // 0-127
	// Velocity is notes only. A CC message leaves it zero.
	Velocity uint8
	Other    OtherCategory
}

// String renders a message for logs and the debug overlay. It is terse on
// purpose: these appear where horizontal space is scarce.
func (m Message) String() string {
	switch m.Kind {
	case KindCC:
		return fmt.Sprintf("cc ch%d n%d=%d", m.Channel, m.Num, m.Value)
	case KindNote:
		return fmt.Sprintf("note ch%d n%d v%d", m.Channel, m.Num, m.Velocity)
	default:
		return "other " + m.Other.String()
	}
}

// String renders an OtherCategory for the debug overlay.
func (o OtherCategory) String() string {
	switch o {
	case OtherSysex:
		return "sysex"
	case OtherProgramChange:
		return "program-change"
	case OtherPitchBend:
		return "pitch-bend"
	case OtherAftertouch:
		return "aftertouch"
	case OtherClock:
		return "clock"
	case OtherTransport:
		return "transport"
	default:
		return "unknown"
	}
}

// Counts accumulates ignored-message tallies for the debug overlay.
type Counts struct {
	ignored  [NumOtherCategories]uint64
	unmapped uint64
}

// ErrIgnored reports that Record classified a message as ignored. Callers log
// once per category rather than once per message, since a controller streaming
// clock produces hundreds a second.
type ErrIgnored struct{ Category OtherCategory }

func (e *ErrIgnored) Error() string { return "ignored message: " + e.Category.String() }

// ErrUnmapped reports a well-formed CC or note that matched no binding — the
// most useful signal that a mapping file does not describe the controller in
// hand.
type ErrUnmapped struct{ Msg Message }

func (e *ErrUnmapped) Error() string { return "unmapped message: " + e.Msg.String() }

// Record tallies a message. It returns a non-nil error when the message was
// ignored (an Other category or, for CC/note, nothing bound to it), so the
// caller can react without re-inspecting it.
//
// The unmapped tally is incremented here only for CC and note messages; whether
// a given message is actually mapped is the resolver's call, so the caller
// reports that separately via RecordUnmapped.
func (c *Counts) Record(m Message) error {
	switch m.Kind {
	case KindOther:
		if m.Other >= 0 && int(m.Other) < int(NumOtherCategories) {
			c.ignored[m.Other]++
		}
		return &ErrIgnored{Category: m.Other}
	default:
		// A CC or note is not ignored on arrival; it may still turn out to be
		// unmapped, which RecordUnmapped reports.
		return nil
	}
}

// RecordUnmapped tallies a CC or note that matched no binding.
func (c *Counts) RecordUnmapped(m Message) {
	c.unmapped++
}

// Snapshot returns a copy of the tallies keyed by the same names String uses.
// It allocates, so it is for the UI thread, never the input path.
func (c Counts) Snapshot() map[string]uint64 {
	m := make(map[string]uint64, NumOtherCategories+1)
	for i := 0; i < int(NumOtherCategories); i++ {
		m[OtherCategory(i).String()] = c.ignored[i]
	}
	m["unmapped"] = c.unmapped
	return m
}

// actionNames is the YAML spelling for every action this layer exposes, mapped
// to the engine kind that implements it.
//
// Four names deliberately differ from the engine's Go identifiers:
// CrossfaderSet/MixerCrossfader, MasterGain/MixerMasterGain,
// ZoomLevel/ZoomIn, FocusDeck/FocusNext. The YAML spelling is this plan's
// public vocabulary and engine's identifiers are used across milestone 1 code
// and its tests, so the mapping lives here rather than renaming working
// constants. Format and Parse read this one table, so the round trip holds by
// construction.
var actionNames = map[string]engine.ActionKind{
	"DeckPlay":         engine.DeckPlay,
	"DeckCue":          engine.DeckCue,
	"CueAssign":        engine.DeckCueAssign,
	"DeckCueAssign":    engine.DeckCueAssign,
	"DeckPitch":        engine.DeckPitch,
	"DeckEQLow":        engine.DeckEQLow,
	"DeckEQMid":        engine.DeckEQMid,
	"DeckEQHigh":       engine.DeckEQHigh,
	"DeckFilterKind":   engine.DeckFilterKind,
	"DeckFilterCutoff": engine.DeckFilterCutoff,
	"DeckFilterReso":   engine.DeckFilterReso,
	"DeckNudge":        engine.DeckNudge,
	"DeckSeek":         engine.DeckSeek,
	"DeckHotcueSet":    engine.DeckHotcueSet,
	"DeckHotcueJump":   engine.DeckHotcueJump,
	"DeckHotcueDelete": engine.DeckHotcueDelete,
	"MixerChannelGain": engine.MixerChannelGain,
	"MixerCrossfader":  engine.MixerCrossfader,
	"MixerCueMix":      engine.MixerCueMix,
	"MixerMasterGain":  engine.MixerMasterGain,
	"FocusNext":        engine.FocusNext,
	"NudgeBeats":       engine.NudgeBeats,
	"CrossfaderSet":    engine.MixerCrossfader,
	"ChannelGain":      engine.MixerChannelGain,
	"CueMix":           engine.MixerCueMix,
	"MasterGain":       engine.MixerMasterGain,
	"ZoomToggle":       engine.ZoomToggle,
	"ZoomLevel":        engine.ZoomIn,
	"ZoomIn":           engine.ZoomIn,
	"ZoomOut":          engine.ZoomOut,
	"ViewNext":         engine.ViewNext,
	"ViewPrev":         engine.ViewPrev,
	"FocusDeck":        engine.FocusNext,
	"LoopBeat":         engine.LoopBeat,
	"LoopIn":           engine.LoopIn,
	"LoopOut":          engine.LoopOut,
	"LoopSave":         engine.LoopSave,
	"LoopRecall":       engine.LoopRecall,
	"LoopRelease":      engine.LoopRelease,
	"LoopRoll":         engine.LoopRoll,
	"Sync":             engine.Sync,
	"SyncToggle":       engine.SyncToggle,
	"Quantize":         engine.Quantize,
	"LibraryOpen":      engine.LibraryOpen,
	"LibraryClose":     engine.LibraryClose,
	"LibraryLoad":      engine.LibraryLoad,
	"LibrarySearch":    engine.LibrarySearch,
	"LibrarySort":      engine.LibrarySort,
	"LibraryFocusNext": engine.LibraryFocusNext,
	"HelpToggle":       engine.HelpToggle,
	"DebugToggle":      engine.DebugToggle,
	"Quit":             engine.Quit,
}

var actionKinds = func() map[engine.ActionKind]string {
	m := make(map[engine.ActionKind]string, len(actionNames))
	for name, kind := range actionNames {
		m[kind] = name
	}
	// A kind can have more than one accepted spelling (CueAssign and
	// DeckCueAssign both drive engine.DeckCueAssign; so do MasterGain and
	// MixerMasterGain). Round-trip needs one canonical output, so prefer the
	// name that matches the engine identifier.
	//
	// This must be a full second pass rather than a fixup while building the
	// map: iteration order is random, so whichever alias happened to be
	// visited last used to win, and FormatActionKind returned a different name
	// from one run to the next.
	for kind := range m {
		engineName := engine.FormatActionKind(kind)
		if engineName == "Unknown" {
			continue
		}
		if k2, ok := actionNames[engineName]; ok && k2 == kind {
			m[kind] = engineName
		}
	}
	return m
}()

// ParseActionKind resolves a YAML spelling to the engine kind it drives.
//
// An unknown name is an error, not a silent no-op: a typo in a mapping file
// otherwise yields a controller that mysteriously does nothing (spec §5.3).
func ParseActionKind(s string) (engine.ActionKind, error) {
	k, ok := actionNames[s]
	if !ok {
		return 0, fmt.Errorf("unknown action %q (check spelling against the action list)", s)
	}
	return k, nil
}

// ParseAction is an alias for ParseActionKind, exported under a shorter name for
// callers outside this package (notably internal/config) that share the action
// vocabulary. One table, so keys and controllers cannot drift apart.
func ParseAction(s string) (engine.ActionKind, error) { return ParseActionKind(s) }

// FormatActionKind renders a kind as its YAML spelling. An unmapped kind
// returns a visible marker rather than an empty string, so a debug overlay
// shows that something is wrong instead of printing nothing.
func FormatActionKind(k engine.ActionKind) string {
	if s, ok := actionKinds[k]; ok {
		return s
	}
	return "Unknown"
}
