package engine

// ActionKind names every control in the application.
//
// The list is deliberately complete rather than minimal: a mapping file is the
// extension surface (spec §5.4), so every feature must have an ActionKind or
// it cannot be driven by a MIDI controller. Adding a feature means adding a
// constant here; nothing else needs to change for it to become mappable.

// Deck actions.
const (
	// DeckPlay starts or resumes the deck on Target. It is idempotent: pressing
	// it on a rolling deck leaves it rolling. Use DeckPlayPause for the
	// play/pause button, which is what a key or a controller pad means.
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
	// DeckPlayPause toggles the deck on Target: rolling decks pause in place,
	// stopped and paused decks resume. It is the transport button.
	//
	// It is a separate kind from DeckPlay rather than a change to it, because
	// mixplay and mixrender start decks with DeckPlay and need that to mean
	// "make this play" -- under a toggle their second call would render silence.
	DeckPlayPause
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
	// Relative makes Value a STEP rather than an absolute position.
	//
	// A MIDI knob sends where the control IS: a value of 1.0 means fully open.
	// A keyboard inc/dec binding sends how far to MOVE, and 1.0 there means one
	// step, not the extreme. Without this distinction both were treated as
	// absolute, so pressing "-" once slammed master gain from unity to zero and
	// "=" slammed it to full -- two presses of a step control moving the whole
	// range, which is how a fader ends up looking broken.
	Relative bool
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
	DeckPlayPause:    "DeckPlayPause",
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

// TakesDeck reports whether an action applies to a deck rather than to the
// mixer, the view or the application.
//
// It exists because an Action's Target has two meanings that must not be
// confused: a deck index, and "no deck named". A keyboard binding that names no
// deck sends Target -1, meaning "whatever deck is focused"; a MIDI mapping
// always names one. Anything that would otherwise read Target as a deck index
// has to ask this first, or an untargeted binding silently addresses deck 0 --
// which is what made space play deck A while the interface said B was focused.
func (k ActionKind) TakesDeck() bool {
	switch {
	case k >= DeckPlay && k <= DeckPlayPause:
		return true
	case k >= DeckHotcueSet && k <= DeckHotcueDelete:
		return true
	case k >= LoopBeat && k <= LoopRoll:
		return true
	case k == Sync || k == SyncToggle || k == Quantize || k == NudgeBeats:
		return true
	}
	return false
}

// TakesDeck reports whether the action applies to a deck.
func (a Action) TakesDeck() bool { return a.Kind.TakesDeck() }

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

// step resolves an action's value against the control's current position.
//
// A relative action (a keyboard inc/dec binding) moves the control by its
// value; an absolute one (a MIDI knob, a config literal) sets it. Keeping this
// here rather than in the keymap means every control gets the same rule, and a
// knob at full scale still means "fully open" rather than "one step up".
//
// Applied only where the getter and setter agree on units: pitch and master
// gain are both normalized. The EQ and filter controls are left absolute
// because no normalized getter exists for them -- FilterCutoff reports Hz, not
// a fader position -- and stepping those against the wrong unit would be worse
// than the behaviour it replaced.
//
// StepSize is the fraction of a control's travel one key press moves. It is a
// knob-of-a-judgement value: small enough to be precise, large enough that a
// few presses cross the range.
func (a Action) step(current, perStep float64) float64 {
	if a.Relative {
		return current + a.Value*perStep
	}
	return a.Value
}

// StepSize is the fraction of a control's travel that one key press moves.
const StepSize = 0.05
