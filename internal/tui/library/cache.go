package library

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

// Analysis is the cached result of analysing one track.
type Analysis struct {
	BPM        float64         `yaml:"bpm"`
	HasGrid    bool            `yaml:"has_grid"`
	Beats      []time.Duration `yaml:"beats,omitempty"`
	KeyCamelot string          `yaml:"key_camelot,omitempty"`
	// KeyName is the musical name ("C minor"). Spec 6.5 wants the Camelot
	// code in the track list and the musical name in the detail pane, so both
	// are stored rather than deriving one from the other.
	KeyName       string  `yaml:"key_name,omitempty"`
	KeyConfidence float64 `yaml:"key_confidence,omitempty"`
	// Size and ModTime are the file's state when the analysis was made. A
	// mismatch on lookup means the file changed and the analysis is stale.
	Size    int64     `yaml:"size"`
	ModTime time.Time `yaml:"mod_time"`
}

// Cache stores analysis on disk, one file per track, named by a hash of its
// absolute path.
//
// Freshness is decided by size and mtime, never content hashing: hashing every
// file in a 20,000-track library on each launch is unacceptable, and spec §4.1
// already chose that tradeoff deliberately.
type Cache struct {
	dir string
	mu  sync.RWMutex
}

// OpenCache returns a cache backed by dir, creating it if needed. A cache that
// cannot be created still works: it simply misses, and the caller re-analyses.
func OpenCache(dir string) *Cache {
	c := &Cache{dir: dir}
	_ = os.MkdirAll(dir, 0o755)
	return c
}

// entryPath is the cache file for an absolute path. The path is hashed so a
// name containing separators or a very long basename cannot escape the cache
// directory or exceed a filesystem's name limit.
func (c *Cache) entryPath(path string) string {
	sum := sha256.Sum256([]byte(path))
	return filepath.Join(c.dir, hex.EncodeToString(sum[:16])+".yaml")
}

// Get returns the cached analysis for path, but only if it still matches the
// file's current size and mtime. A changed file reports false so the caller
// re-analyses rather than trusting stale BPM.
func (c *Cache) Get(path string) (Analysis, bool) {
	if c.dir == "" {
		return Analysis{}, false
	}
	data, err := os.ReadFile(c.entryPath(path))
	if err != nil {
		return Analysis{}, false
	}
	var a Analysis
	if err := yaml.Unmarshal(data, &a); err != nil {
		return Analysis{}, false
	}
	if c.Stale(path) {
		return Analysis{}, false
	}
	return a, true
}

// Put stores analysis for path, stamping it with the file's current size and
// mtime so a later change is detectable.
func (c *Cache) Put(path string, a Analysis) error {
	if c.dir == "" {
		return nil
	}
	// Stamp with the file's current state when it exists. When it does not
	// (analysing a path that is about to appear, or a fixture), keep whatever
	// the caller supplied rather than zeroing it: Get then trusts that stamp
	// instead of treating the entry as permanently stale.
	if fi, err := os.Stat(path); err == nil {
		a.Size = fi.Size()
		a.ModTime = fi.ModTime()
	}
	data, err := yaml.Marshal(&a)
	if err != nil {
		return err
	}
	return os.WriteFile(c.entryPath(path), data, 0o644)
}

// Stale reports whether the file has changed since its analysis was cached, so
// a replaced file cannot silently keep a wrong beatgrid (spec §4.1).
func (c *Cache) Stale(path string) bool {
	if c.dir == "" {
		return true
	}
	data, err := os.ReadFile(c.entryPath(path))
	if err != nil {
		return true
	}
	var a Analysis
	if err := yaml.Unmarshal(data, &a); err != nil {
		return true
	}
	fi, err := os.Stat(path)
	if err != nil {
		// The file cannot be stat'ed. If the entry carries a real stamp, the
		// analysis was made when it existed and nothing contradicts it; once the
		// file appears it is stat'ed and compared normally. An entry with no
		// stamp describes a file we have never seen and cannot be trusted.
		return a.Size == 0 && a.ModTime.IsZero()
	}
	return fi.Size() != a.Size || !fi.ModTime().Equal(a.ModTime)
}
