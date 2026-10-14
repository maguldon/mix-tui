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

// DefaultCratesPath is where crates live: beside the config, in the same
// per-user directory as the mappings.
func DefaultCratesPath(configDir string) string {
	return filepath.Join(configDir, "crates.yaml")
}

// Normalize drops empty and duplicate track paths while preserving order.
//
// A crate is a curated running order, so order is the data. Two entries pointing
// at the same file would show the track twice and load it twice from one
// keystroke, and an empty entry is a track that does not exist.
func (c Crate) Normalize() Crate {
	out := Crate{Name: c.Name}
	seen := make(map[string]bool, len(c.Tracks))
	for _, p := range c.Tracks {
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		out.Tracks = append(out.Tracks, p)
	}
	return out
}

// Contains reports whether the crate already lists a track path.
func (c Crate) Contains(path string) bool {
	for _, p := range c.Tracks {
		if p == path {
			return true
		}
	}
	return false
}

// SaveCrates writes the whole crate list.
//
// It is the counterpart to LoadCrates and exists because SaveCrate re-reads and
// re-marshals the file on every call. For a UI that rewrites the list after each
// add or remove, that is a read-modify-write per keystroke, and it races with
// nothing only by accident: two edits in quick succession would each rewrite
// from their own copy.
func SaveCrates(path string, crates []Crate) error {
	norm := make([]Crate, 0, len(crates))
	seen := make(map[string]bool, len(crates))
	for _, c := range crates {
		c = c.Normalize()
		if c.Name == "" || seen[c.Name] {
			continue
		}
		seen[c.Name] = true
		norm = append(norm, c)
	}
	data, err := yaml.Marshal(&norm)
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
