package engine

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"sync"

	"gopkg.in/yaml.v3"
)

// CueStore persists per-track hotcues and saved loops (spec §10).
//
// Two parallel directories, hotcues/ and loops/, keyed by the same hash of the
// absolute path. Two directories rather than one file holding both so a write of
// a loop cannot lose a hotcue written a moment earlier, and so the two are
// invalidated together by sharing one key function.
//
// The path is hashed rather than used as a filename: it may contain separators,
// and a long basename would exceed a filesystem's name limit. This is the same
// identity Cache uses, so analysis, hotcues and loops all agree on which file is
// which -- and are all invalidated together when a file is replaced.
//
// A store that cannot be written still works: it misses, the deck behaves as
// though nothing were remembered, and nothing is lost that was not already only
// on disk. Failing a load because a config directory is read-only would be a
// far worse outcome than forgetting a cue.
type CueStore struct {
	hotcueDir string
	loopDir   string

	mu sync.Mutex
}

// OpenCueStore returns a store backed by dir's hotcues/ and loops/ children,
// creating them if needed.
func OpenCueStore(dir string) *CueStore {
	s := &CueStore{
		hotcueDir: filepath.Join(dir, "hotcues"),
		loopDir:   filepath.Join(dir, "loops"),
	}
	_ = os.MkdirAll(s.hotcueDir, 0o755)
	_ = os.MkdirAll(s.loopDir, 0o755)
	return s
}

// entryName is the shared filename for a track's remembered positions.
func entryName(path string) string {
	sum := sha256.Sum256([]byte(path))
	return hex.EncodeToString(sum[:16]) + ".yaml"
}

// ErrCueStoreUnset is returned when a store operation is attempted on a store
// with no directory, which is how a headless or test build disables
// persistence. It is distinct from "no such entry": callers treat the first as
// a misconfiguration worth reporting and the second as routine.
var ErrCueStoreUnset = errors.New("cue store has no directory")

// Load returns what is remembered for path. A store with nothing stored, or a
// read that fails, reports empty rather than an error: a missing cue file is
// the normal first-run case, not a failure.
func (s *CueStore) Load(path string) Cues {
	if s == nil || s.hotcueDir == "" || path == "" {
		return Cues{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var c Cues
	if data, err := os.ReadFile(filepath.Join(s.hotcueDir, entryName(path))); err == nil {
		// A corrupt file must not stop the loops from restoring, so the two
		// halves are read independently.
		_ = yaml.Unmarshal(data, &c.Hotcues)
	}
	if data, err := os.ReadFile(filepath.Join(s.loopDir, entryName(path))); err == nil {
		_ = yaml.Unmarshal(data, &c.Loops)
	}
	return c
}

// Save records what is remembered for path. Nothing is written when there is
// nothing to remember, so a track the user never marked does not leave a file
// behind.
func (s *CueStore) Save(path string, c Cues) error {
	if s == nil || s.hotcueDir == "" {
		return ErrCueStoreUnset
	}
	if path == "" {
		return errors.New("cue store: empty path")
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	if anyCueSet(c.Hotcues) {
		data, err := yaml.Marshal(c.Hotcues)
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(s.hotcueDir, entryName(path)), data, 0o644); err != nil {
			return err
		}
	}
	if len(c.Loops) > 0 {
		data, err := yaml.Marshal(c.Loops)
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(s.loopDir, entryName(path)), data, 0o644); err != nil {
			return err
		}
	}
	return nil
}

func anyCueSet(cs [numHotcues]Cue) bool {
	for _, c := range cs {
		if c.Set {
			return true
		}
	}
	return false
}
