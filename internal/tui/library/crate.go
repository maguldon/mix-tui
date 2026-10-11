package library

import (
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// Crate is a named, ordered list of track paths.
type Crate struct {
	Name   string   `yaml:"name"`
	Tracks []string `yaml:"tracks"`
}

// LoadCrates reads a crate file. A missing file yields no crates and no error:
// a fresh installation has none, and that is not a failure.
func LoadCrates(path string) ([]Crate, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	if len(data) == 0 {
		return nil, nil
	}
	var crates []Crate
	if err := yaml.Unmarshal(data, &crates); err != nil {
		return nil, err
	}
	return crates, nil
}

// SaveCrate writes a crate into path, merging with whatever is already there.
//
// It merges rather than overwrites because saving one crate must not delete the
// others (spec §6.5). A crate with an existing name is replaced in place,
// since re-saving is how a user edits one.
func SaveCrate(path string, c Crate) error {
	existing, err := LoadCrates(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	out := make([]Crate, 0, len(existing)+1)
	replaced := false
	for _, e := range existing {
		if e.Name == c.Name {
			out = append(out, c)
			replaced = true
			continue
		}
		out = append(out, e)
	}
	if !replaced {
		out = append(out, c)
	}
	data, err := yaml.Marshal(&out)
	if err != nil {
		return err
	}
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	return os.WriteFile(path, data, 0o644)
}
