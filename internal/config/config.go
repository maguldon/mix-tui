// Package config loads the application's user configuration: audio device
// settings, view defaults, and keybindings.
//
// Loading is strict — unknown YAML fields are errors — because these files are
// user-editable and a silently ignored typo produces a controller or setting
// that mysteriously does nothing (spec §5.3).
package config

// Config is the whole user configuration.
type Config struct {
	Audio AudioConfig           `yaml:"audio"`
	UI    UIConfig              `yaml:"ui"`
	Keys  map[string]KeyBinding `yaml:"keys"`
}

// AudioConfig holds device and buffer settings.
type AudioConfig struct {
	// SampleRate is the rate the engine renders at and the stream declares to
	// PipeWire. 0 means the 44100 Hz default, not "negotiate": there is no
	// supported way to read the graph's rate back (issue #23), so PipeWire
	// converts to whatever the device grants and Stats.RateMismatch reports the
	// disagreement.
	SampleRate int    `yaml:"sample_rate"`
	MainTarget string `yaml:"main_target"`
	CueTarget  string `yaml:"cue_target"`
	// RingFrames is the per-stream ring depth. It should comfortably exceed
	// TargetLatencyFrames so the render goroutine has room to run ahead.
	RingFrames int `yaml:"ring_frames"`
	// TargetLatencyFrames is how full the pump keeps each ring: the render-ahead
	// budget that trades latency for glitch resistance.
	TargetLatencyFrames int `yaml:"target_latency_frames"`
}

// UIConfig holds presentation defaults.
type UIConfig struct {
	// DefaultView is one of A, B, C, D. D is the default per spec §6.1.
	DefaultView string `yaml:"default_view"`
	// LibraryRoot is the directory scanned for tracks at startup, and the one
	// the settings screen edits.
	//
	// It was declared as LibraryNode and never read by anything: the scan
	// consulted the -library flag alone, so a config setting library_root
	// parsed, validated, and was then discarded. Because unknown fields are
	// errors here, a user who wrote it got a config that looked accepted and
	// did nothing -- the most expensive kind of silent failure.
	//
	// A leading "~" is expanded at load; see ExpandHome.
	LibraryRoot string `yaml:"library_root"`
}

// KeyBinding binds one key name ("q", "ctrl+s", "f1") to an action.
type KeyBinding struct {
	Action string `yaml:"action"`
	// Target is the deck an action applies to, or nil for the focused deck.
	//
	// It is a pointer because "no deck named" and "deck 0" have to be told
	// apart. As a plain int an omitted target reads as 0, every deck-scoped key
	// therefore resolved to deck A, and space played deck A while the deck
	// panel said B was focused -- a keyboard-complete application in which the
	// right-hand deck could not be played at all. Naming a target pins the key
	// to that deck, which is the point of the field.
	Target *int `yaml:"target"`
	// Slot is the hotcue or loop slot for actions that take one. Without it no
	// hotcue, loop or roll is reachable from the keyboard at all, since MIDI
	// mappings carry a slot and key bindings could not.
	Slot int `yaml:"slot"`
	// Value is "toggle", "inc", "dec", or a literal number.
	Value string `yaml:"value"`
	// Repeat allows the binding to fire on auto-repeat while held.
	//
	// A pointer, so "not written" and "written as false" are different.
	//
	// It was a plain bool, whose zero value is false -- so honouring it would
	// have meant suppressing auto-repeat on every binding in the tree, and
	// holding a transport key would play once and stop. Unset therefore means
	// repeat is allowed, which is what the application has always done, and
	// only an explicit `repeat: false` turns it off.
	//
	// Repeats() existed and had no callers, so the option did nothing in either
	// direction: a binding with `repeat: true` and one without behaved alike.
	Repeat *bool `yaml:"repeat"`
}

// DeckTarget returns the pinned deck, or -1 when the binding names none and the
// action should follow the focused deck.
func (b KeyBinding) DeckTarget() int {
	if b.Target == nil {
		return -1
	}
	return *b.Target
}

// Defaults returns the built-in configuration used when no file is present.
//
// TargetLatencyFrames follows spec §4.2: the render goroutine stays ~200ms
// ahead of playback, which is 8820 frames at 44.1kHz.
//
// RingFrames is deliberately four times that. The two used to be equal, which
// contradicted the field's own documented requirement that the ring "should
// comfortably exceed" the target and left it with no headroom at all: a ring
// filled to target is a ring that can only ever serve a target-sized callback,
// so any device asking for more underruns on every single callback. The cost is
// under a megabyte and it is not latency, because the pump still stops filling
// at the target.
func Defaults() *Config {
	return &Config{
		Audio: AudioConfig{
			SampleRate:          0, // negotiate
			RingFrames:          35280,
			TargetLatencyFrames: 8820,
		},
		UI:   UIConfig{DefaultView: "D"},
		Keys: map[string]KeyBinding{},
	}
}
