package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"

	"gopkg.in/yaml.v3"
)

// Load reads a config file strictly.
//
// A missing file is NOT an error: it yields Defaults, so the application runs
// with no configuration at all. Every other failure — including an unknown
// field — is an error carrying the file path and a line number, because a
// configuration error without a location is not fixable without guessing.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Defaults(), nil
		}
		return nil, fmt.Errorf("%s: %w", path, err)
	}

	cfg := Defaults()
	// Decode onto the defaults so unspecified fields keep their default rather
	// than becoming zero — otherwise a partial config would silently zero the
	// ring size.
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(cfg); err != nil {
		if errors.Is(err, io.EOF) {
			// An empty file is valid and means "all defaults".
			return cfg, nil
		}
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if err := validate(cfg); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return cfg, nil
}

func validate(cfg *Config) error {
	switch cfg.UI.DefaultView {
	case "A", "B", "C", "D":
	default:
		return fmt.Errorf("invalid default_view %q (want A, B, C, or D)", cfg.UI.DefaultView)
	}
	if cfg.Audio.RingFrames < 0 {
		return fmt.Errorf("audio.ring_frames must be positive, got %d", cfg.Audio.RingFrames)
	}
	if cfg.Audio.TargetLatencyFrames < 0 {
		return fmt.Errorf("audio.target_latency_frames must be positive, got %d", cfg.Audio.TargetLatencyFrames)
	}
	if cfg.Audio.SampleRate < 0 {
		return fmt.Errorf("audio.sample_rate must be positive, got %d", cfg.Audio.SampleRate)
	}
	return nil
}
