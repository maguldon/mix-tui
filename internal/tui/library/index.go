// Package library scans a music collection, indexes it, caches analysis, and
// stores crates.
//
// Scanning runs in the background and never blocks the caller: a DJ opening a
// 20,000-track library must still be able to hit play while it indexes
// (spec §8).
package library

import (
	"sort"
	"strings"
	"sync"
	"time"
)

// Track is one entry in the library.
type Track struct {
	Path     string
	Title    string
	Artist   string
	Duration time.Duration
	BPM      float64
	HasGrid  bool
	// KeyCamelot is empty when no key was detected or confidence was low.
	KeyCamelot    string
	KeyConfidence float64
	// InLibrary marks a track that is loaded in a deck, which the crate view
	// highlights so the user can see what is playing.
	InLibrary bool
	// Added is the file's modification time, used for the "recently added"
	// sort order.
	Added time.Time
}

// SortKey selects the sort order.
type SortKey int

const (
	SortTitle SortKey = iota
	SortArtist
	SortBPM
	// SortByKey sorts by Camelot key. It cannot be named SortKey because that
	// is the type; a name collision the plan introduced.
	SortByKey
	SortAdded
)

// Index is a searchable, sortable collection of tracks.
type Index struct {
	mu     sync.RWMutex
	tracks []Track
	seen   map[string]bool
}

// NewIndex returns an empty index.
func NewIndex() *Index {
	return &Index{seen: make(map[string]bool)}
}

// Tracks returns a copy of the track list, so a caller cannot mutate the
// index's internal slice while a scan is appending to it.
func (ix *Index) Tracks() []Track {
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	out := make([]Track, len(ix.tracks))
	copy(out, ix.tracks)
	return out
}

// Len reports how many tracks are indexed.
func (ix *Index) Len() int {
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	return len(ix.tracks)
}

// Add inserts a track, ignoring duplicates by path. It lets a caller populate
// an index from a cache or a crate without going through a filesystem scan.
func (ix *Index) Add(t Track) bool { return ix.add(t) }

func (ix *Index) add(t Track) bool {
	ix.mu.Lock()
	defer ix.mu.Unlock()
	if ix.seen[t.Path] {
		return false
	}
	ix.seen[t.Path] = true
	ix.tracks = append(ix.tracks, t)
	return true
}

// reset clears the index so a rescan starts from a clean slate rather than
// accumulating duplicates.
func (ix *Index) reset() {
	ix.mu.Lock()
	defer ix.mu.Unlock()
	ix.tracks = nil
	ix.seen = make(map[string]bool)
}

// Sort orders a COPY of the tracks and returns it. It never mutates its input:
// the index owns its slice and a UI sort must not reshuffle it underneath a
// concurrent render.
//
// Entries with no detected BPM sort last in both directions — a track with
// unknown tempo sitting at the top of a BPM-sorted crate is a DJ's fastest way
// to lose time.
func Sort(tracks []Track, by SortKey, desc bool) []Track {
	out := make([]Track, len(tracks))
	copy(out, tracks)

	less := func(i, j int) bool {
		a, b := out[i], out[j]
		switch by {
		case SortBPM:
			if a.BPM == 0 || b.BPM == 0 {
				return b.BPM == 0 && a.BPM != 0 // detected first
			}
			return a.BPM < b.BPM
		case SortArtist:
			return strings.ToLower(a.Artist) < strings.ToLower(b.Artist)
		case SortAdded:
			// Ascending is oldest-first, so `desc` flips it to newest-first.
			// Returning newest-first unconditionally meant the toggle inverted
			// the meaning rather than flipping a defined baseline.
			return a.Added.Before(b.Added)
		case SortByKey:
			// Camelot is a wheel lookup, not a string: 10A must sort after 9A,
			// which lexicographic order gets backwards.
			return camelLess(a.KeyCamelot, b.KeyCamelot)
		default: // SortTitle
			return strings.ToLower(a.Title) < strings.ToLower(b.Title)
		}
	}
	sort.SliceStable(out, less)
	if desc {
		// Reverse, but keep undetected entries at the end.
		for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
			out[i], out[j] = out[j], out[i]
		}
		stableUndetected(out, by)
	}
	return out
}

// stableUndetected moves undetected entries to the end after a descending
// reverse would have put them first.
// stableUndetected moves entries with no detected value to the end, in both
// directions.
//
// It applies to key sorting as well as BPM. camelLess already ranks an
// undetected key last, but the descending path reverses the whole slice
// afterwards, which put every un-keyed track at the top of the list -- the
// opposite of what the ascending comparator promises.
func stableUndetected(ts []Track, by SortKey) {
	if by != SortBPM && by != SortByKey {
		return
	}
	undetected := func(t Track) bool {
		if by == SortByKey {
			_, _, ok := parseCamelot(t.KeyCamelot)
			return !ok
		}
		return t.BPM == 0
	}

	detected := ts[:0]
	var undet []Track
	for _, t := range ts {
		if undetected(t) {
			undet = append(undet, t)
		} else {
			detected = append(detected, t)
		}
	}
	copy(ts, append(detected, undet...))
}

// camelLess orders Camelot keys around the wheel: numeric part 1-12, then
// A (minor) before B (major).
func camelLess(a, b string) bool {
	an, aa, aok := parseCamelot(a)
	bn, ba, bok := parseCamelot(b)
	if aok != bok {
		return aok // an undetected key sorts last
	}
	if !aok {
		return false
	}
	if an != bn {
		return an < bn
	}
	return aa < ba
}

// parseCamelot splits a Camelot key into its number and its A/B letter.
//
// The number must be 1-12, the wheel's actual range. Without the range check
// "100A" parsed as a valid key and sorted after every real one, so a corrupt or
// mistyped key would quietly push a track to the end of the list instead of
// being treated as undetected.
func parseCamelot(s string) (num int, letter byte, ok bool) {
	if len(s) < 2 {
		return 0, 0, false
	}
	n := 0
	digits := 0
	for i := 0; i < len(s)-1; i++ {
		if s[i] < '0' || s[i] > '9' {
			return 0, 0, false
		}
		n = n*10 + int(s[i]-'0')
		digits++
	}
	if digits == 0 || (s[len(s)-1] != 'A' && s[len(s)-1] != 'B') {
		return 0, 0, false
	}
	if n < 1 || n > 12 {
		return 0, 0, false
	}
	return n, s[len(s)-1], true
}

// Search filters on filename, title, and artist, case-insensitively. An empty
// query returns everything, so clearing the box restores the full list.
func Search(tracks []Track, q string) []Track {
	q = strings.ToLower(strings.TrimSpace(q))
	if q == "" {
		out := make([]Track, len(tracks))
		copy(out, tracks)
		return out
	}
	out := make([]Track, 0, len(tracks))
	for _, t := range tracks {
		hay := strings.ToLower(t.Title + " " + t.Artist + " " + t.Path)
		if strings.Contains(hay, q) {
			out = append(out, t)
		}
	}
	return out
}
