package library

import (
	"os"
	"path/filepath"
	"testing"
)

// TestEvictMissingDropsDeletedTracks covers spec 8's cleanup for a track deleted
// since the last scan. The index is rebuilt from the filesystem every start, so
// the track vanished; its analysis did not, and the cache grew forever
// describing a collection the user no longer had.
func TestEvictMissingDropsDeletedTracks(t *testing.T) {
	dir := t.TempDir()
	c := OpenCache(dir)

	here := filepath.Join(dir, "still-here.mp3")
	gone := filepath.Join(dir, "deleted.mp3")
	for _, p := range []string{here, gone} {
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := c.Put(here, Analysis{BPM: 128, HasGrid: true}); err != nil {
		t.Fatal(err)
	}
	if err := c.Put(gone, Analysis{BPM: 90, HasGrid: true}); err != nil {
		t.Fatal(err)
	}

	// The track is deleted between sessions.
	if err := os.Remove(gone); err != nil {
		t.Fatal(err)
	}

	if n := c.EvictMissing(); n != 1 {
		t.Errorf("EvictMissing removed %d, want 1", n)
	}
	if _, ok := c.Get(here); !ok {
		t.Error("the surviving track's analysis was evicted")
	}
	if _, ok := c.Get(gone); ok {
		t.Error("the deleted track's analysis survived")
	}
}

// TestEvictMissingLeavesUnreadableAndPathlessEntries keeps a guess from throwing
// away a correct analysis.
func TestEvictMissingLeavesUnreadableAndPathlessEntries(t *testing.T) {
	dir := t.TempDir()
	c := OpenCache(dir)

	// An entry that never recorded a path cannot be checked, so it stays.
	if err := os.WriteFile(c.entryPath("/some/track.mp3"), []byte("bpm: 128\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadDir(dir)

	if n := c.EvictMissing(); n != 0 {
		t.Errorf("EvictMissing removed %d entries it could not verify", n)
	}
	after, _ := os.ReadDir(dir)
	if len(before) != len(after) {
		t.Errorf("the cache changed: %d entries before, %d after", len(before), len(after))
	}
}

// TestEvictMissingOnAnEmptyOrUnopenedCache must not error.
func TestEvictMissingOnAnEmptyOrUnopenedCache(t *testing.T) {
	if n := OpenCache(t.TempDir()).EvictMissing(); n != 0 {
		t.Errorf("an empty cache removed %d", n)
	}
	if n := (&Cache{}).EvictMissing(); n != 0 {
		t.Errorf("an unopened cache removed %d", n)
	}
}
