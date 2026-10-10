package engine

// ActionKind names every control in the application.
//
// The list is deliberately complete rather than minimal: a mapping file is the
// extension surface (spec §5.4), so every feature must have an ActionKind or
// it cannot be driven by a MIDI controller. Adding a feature means adding a
// constant here; nothing else needs to change for it to become mappable.

// Deck actions.
const (
	// DeckPlay starts or resumes the deck on Target.
	DeckPlay ActionKind = iota + 1
	DeckCue
	DeckPitch // Value normalized -1..1, 1.0 = +8%
	DeckEQLow // Value normalized -1..1, 0 = unity
	DeckEQMid
	DeckEQHigh
	DeckFilterKind   // Value >= 0 selects highpass, < 0 lowpass
	DeckFilterCutoff // Value normalized 0..1 across the filter range
	DeckFilterReso   // Value normalized 0..1
	DeckSeek         // Value normalized 0..1 position
	DeckNudge        // Value = seconds to nudge by
	DeckCueAssign    // routes the deck to the cue bus
)

// Hotcue actions. Slot selects which hotcue.
const (
	DeckHotcueSet ActionKind = iota + 100
	DeckHotcueJump
	DeckHotcueDelete
)

// Mixer actions.
const (
	MixerCrossfader ActionKind = iota + 200 // Value normalized -1..1
	MixerChannelGain
	MixerCueMix
	MixerMasterGain
)

// Loop actions.
const (
	LoopBeat ActionKind = iota + 300 // Value 0..1 across the beat sizes
	LoopIn                           // set loop in at the current position
	LoopOut
	LoopSave   // Slot
	LoopRecall // Slot
	LoopRelease
	LoopRoll // momentary; Value = roll length in beats
)

// Sync actions.
const (
	Sync ActionKind = iota + 400
	SyncToggle
	Quantize
	NudgeBeats // Value = beats to nudge by
)

// View actions. These never touch audio; the TUI consumes them.
const (
	ViewNext ActionKind = iota + 500
	ViewPrev
	FocusNext
	ZoomToggle
	ZoomIn
	ZoomOut
	DebugToggle
	HelpToggle
	Quit
)

// Library actions.
const (
	LibraryOpen ActionKind = iota + 600
	LibraryClose
	LibraryLoad // Slot = track index in the current view
	LibrarySearch
	LibrarySort
	LibraryFocusNext
)

// ActionKind identifies a control. Every key press, knob turn, and menu item
// resolves to one of these; nothing else may touch deck state (spec §5).
type ActionKind int

// Action is a single control movement. Value is normalized to [-1, 1] for
// continuous controls; Slot carries a hotcue or loop number where the action
// takes one.
type Action struct {
	Kind   ActionKind
	Target int
	Value  float64
	Slot   int
}

// actionNames maps each kind to the spelling used in configuration files. The
// mapping is the API: renaming an entry here breaks users' mapping files, so
// it is treated as a compatibility surface.
var actionNames = map[ActionKind]string{
	DeckPlay:         "DeckPlay",
	DeckCue:          "DeckCue",
	DeckPitch:        "DeckPitch",
	DeckEQLow:        "DeckEQLow",
	DeckEQMid:        "DeckEQMid",
	DeckEQHigh:       "DeckEQHigh",
	DeckFilterKind:   "DeckFilterKind",
	DeckFilterCutoff: "DeckFilterCutoff",
	DeckFilterReso:   "DeckFilterReso",
	DeckSeek:         "DeckSeek",
	DeckNudge:        "DeckNudge",
	DeckCueAssign:    "DeckCueAssign",
	DeckHotcueSet:    "DeckHotcueSet",
	DeckHotcueJump:   "DeckHotcueJump",
	DeckHotcueDelete: "DeckHotcueDelete",
	MixerCrossfader:  "MixerCrossfader",
	MixerChannelGain: "MixerChannelGain",
	MixerCueMix:      "MixerCueMix",
	MixerMasterGain:  "MixerMasterGain",
	LoopBeat:         "LoopBeat",
	LoopIn:           "LoopIn",
	LoopOut:          "LoopOut",
	LoopSave:         "LoopSave",
	LoopRecall:       "LoopRecall",
	LoopRelease:      "LoopRelease",
	LoopRoll:         "LoopRoll",
	Sync:             "Sync",
	SyncToggle:       "SyncToggle",
	Quantize:         "Quantize",
	NudgeBeats:       "NudgeBeats",
	ViewNext:         "ViewNext",
	ViewPrev:         "ViewPrev",
	FocusNext:        "FocusNext",
	ZoomToggle:       "ZoomToggle",
	ZoomIn:           "ZoomIn",
	ZoomOut:          "ZoomOut",
	DebugToggle:      "DebugToggle",
	HelpToggle:       "HelpToggle",
	Quit:             "Quit",
	LibraryOpen:      "LibraryOpen",
	LibraryClose:     "LibraryClose",
	LibraryLoad:      "LibraryLoad",
	LibrarySearch:    "LibrarySearch",
	LibrarySort:      "LibrarySort",
	LibraryFocusNext: "LibraryFocusNext",
}

var actionKinds = func() map[string]ActionKind {
	m := make(map[string]ActionKind, len(actionNames))
	for k, v := range actionNames {
		m[v] = k
	}
	return m
}()

// ParseActionKind resolves a configuration-file spelling to an ActionKind.
//
// An unknown name is an error rather than a silent no-op: a typo in a mapping
// file otherwise produces a controller that mysteriously does nothing
// (spec §5.3).
func ParseActionKind(s string) (ActionKind, error) {
	k, ok := actionKinds[s]
	if !ok {
		return 0, &UnknownActionError{Name: s}
	}
	return k, nil
}

// UnknownActionError reports a configuration spelling that matches no action.
type UnknownActionError struct{ Name string }

func (e *UnknownActionError) Error() string {
	return "unknown action " + e.Name + " (check spelling against the action list)"
}

// FormatActionKind renders an ActionKind as its configuration spelling.
func FormatActionKind(k ActionKind) string {
	if s, ok := actionNames[k]; ok {
		return s
	}
	return "Unknown"
}

// String makes ActionKind printable and satisfies fmt.Stringer, so a bad value
// in a debug overlay names itself rather than printing a number.
func (k ActionKind) String() string { return FormatActionKind(k) }

// String renders an Action for logging. It is deliberately terse: these appear
// in the debug overlay where horizontal space is scarce.
func (a Action) String() string {
	s := a.Kind.String() + " target=" + itoa(a.Target)
	if a.Slot != 0 {
		s += " slot=" + itoa(a.Slot)
	}
	return s
}

func itoa(v int) string {
	if v == 0 {
		return "0"
	}
	neg := v < 0
	if neg {
		v = -v
	}
	var buf [20]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
