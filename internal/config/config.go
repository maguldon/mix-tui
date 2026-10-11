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
	// SampleRate 0 means negotiate with the device at startup.
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
	LibraryNode string `yaml:"library_root"`
}

// KeyBinding binds one key name ("q", "ctrl+s", "f1") to an action.
type KeyBinding struct {
	Action string `yaml:"action"`
	Target int    `yaml:"target"`
	// Slot is the hotcue or loop slot for actions that take one. Without it no
	// hotcue, loop or roll is reachable from the keyboard at all, since MIDI
	// mappings carry a slot and key bindings could not.
	Slot int `yaml:"slot"`
	// Value is "toggle", "inc", "dec", or a literal number.
	Value string `yaml:"value"`
	// Repeat allows the binding to fire on auto-repeat while held.
	Repeat bool `yaml:"repeat"`
}

// Defaults returns the built-in configuration used when no file is present.
//
// The buffer sizes follow spec §4.2: the render goroutine stays ~200ms ahead of
// playback, which is 8820 frames at 44.1kHz.
func Defaults() *Config {
	return &Config{
		Audio: AudioConfig{
			SampleRate:          0, // negotiate
			RingFrames:          8820,
			TargetLatencyFrames: 8820,
		},
		UI:   UIConfig{DefaultView: "D"},
		Keys: map[string]KeyBinding{},
	}
}
