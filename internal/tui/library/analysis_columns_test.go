package library

import (
	"os"
	"path/filepath"
	"testing"
)

// TestSetAnalysisFillsTheBrowserColumns is the regression test for issue #10.
//
// Nothing ever wrote Track.BPM, Track.KeyCamelot or Track.KeyConfidence in
// production: the analyzer produced them, wrote them to the cache and to the
// engine's DeckState, and never touched the index -- which holds no reference to
// either. So the browser's BPM and KEY columns read "--" and "?" for every track
// forever, and sorting by BPM or key did nothing on real data.
func TestSetAnalysisFillsTheBrowserColumns(t *testing.T) {
	ix := NewIndex()
	ix.Add(Track{Path: "/m/a.mp3", Title: "A"})

	if !ix.SetAnalysis("/m/a.mp3", Analysis{
		BPM: 128, HasGrid: true,
		KeyCamelot: "8A", KeyName: "A minor", KeyConfidence: 0.9,
	}) {
		t.Fatal("SetAnalysis did not match an indexed track")
	}

	tr := ix.Tracks()[0]
	if tr.BPM != 128 || !tr.HasGrid {
		t.Errorf("BPM = %v (grid %v), want 128 with a grid", tr.BPM, tr.HasGrid)
	}
	if tr.KeyCamelot != "8A" || tr.KeyName != "A minor" {
		t.Errorf("key = %q/%q, want 8A/A minor", tr.KeyCamelot, tr.KeyName)
	}
}

// TestSetAnalysisRefusesALowConfidenceKey keeps a guess out of the list. A key
// shown as certain is worse than no key, because a DJ would crate-match on it.
func TestSetAnalysisRefusesALowConfidenceKey(t *testing.T) {
	ix := NewIndex()
	ix.Add(Track{Path: "/m/a.mp3"})
	ix.SetAnalysis("/m/a.mp3", Analysis{KeyCamelot: "3B", KeyConfidence: 0.2})

	if got := ix.Tracks()[0].KeyCamelot; got != "" {
		t.Errorf("a 0.2-confidence key was shown as %q", got)
	}
}

// TestSetAnalysisIgnoresUnknownPaths covers a track the user loaded directly,
// which is analysed but not indexed.
func TestSetAnalysisIgnoresUnknownPaths(t *testing.T) {
	ix := NewIndex()
	if ix.SetAnalysis("/not/indexed.mp3", Analysis{BPM: 100, HasGrid: true}) {
		t.Error("SetAnalysis reported a match for a track that is not indexed")
	}
}

// TestSetAnalysisKeepsAnExistingGrid guards against a cache entry with no grid
// wiping a tempo the index already had.
func TestSetAnalysisKeepsAnExistingGrid(t *testing.T) {
	ix := NewIndex()
	ix.Add(Track{Path: "/m/a.mp3", BPM: 140, HasGrid: true})
	ix.SetAnalysis("/m/a.mp3", Analysis{HasGrid: false})
	if got := ix.Tracks()[0].BPM; got != 140 {
		t.Errorf("a grid-less analysis cleared the BPM: %v", got)
	}
}

// TestApplyAnalysisReportsHowManyTracksItReached keeps the startup line honest.
func TestApplyAnalysisReportsHowManyTracksItReached(t *testing.T) {
	ix := NewIndex()
	ix.Add(Track{Path: "/m/a.mp3"})
	ix.Add(Track{Path: "/m/b.mp3"})
	n := ix.ApplyAnalysis(map[string]Analysis{
		"/m/a.mp3":    {BPM: 128, HasGrid: true},
		"/m/gone.mp3": {BPM: 90, HasGrid: true},
	})
	if n != 1 {
		t.Errorf("ApplyAnalysis reported %d, want 1", n)
	}
}

// TestCacheAllKeysEntriesByPath covers the mechanism that makes the columns work
// at startup: one directory read instead of a lookup per track.
func TestCacheAllKeysEntriesByPath(t *testing.T) {
	dir := t.TempDir()
	c := OpenCache(dir)

	a := filepath.Join(dir, "a.mp3")
	b := filepath.Join(dir, "b.mp3")
	for _, p := range []string{a, b} {
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := c.Put(a, Analysis{BPM: 128, HasGrid: true}); err != nil {
		t.Fatal(err)
	}
	if err := c.Put(b, Analysis{BPM: 90, HasGrid: true}); err != nil {
		t.Fatal(err)
	}

	all := c.All()
	if len(all) != 2 {
		t.Fatalf("All returned %d entries, want 2", len(all))
	}
	if all[a].BPM != 128 || all[b].BPM != 90 {
		t.Errorf("All returned the wrong values: %+v", all)
	}
}

// TestCacheAllSkipsEntriesWithNoPath keeps an old-format cache usable: those
// entries are skipped and the track simply re-analyses.
func TestCacheAllSkipsEntriesWithNoPath(t *testing.T) {
	dir := t.TempDir()
	c := OpenCache(dir)
	p := filepath.Join(dir, "a.mp3")
	if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	// An entry in the shape written before Path existed.
	if err := os.WriteFile(c.entryPath(p), []byte("bpm: 128\nhas_grid: true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if n := len(c.All()); n != 0 {
		t.Errorf("All returned %d entries; a path-less entry cannot be matched", n)
	}
	// And it must not be treated as stale-but-present: the track re-analyses.
	if _, ok := c.Get(p); ok {
		t.Error("a path-less entry was trusted")
	}
}

// TestCacheAllSkipsStaleEntries stops a replaced file keeping a wrong beatgrid.
func TestCacheAllSkipsStaleEntries(t *testing.T) {
	dir := t.TempDir()
	c := OpenCache(dir)
	p := filepath.Join(dir, "a.mp3")
	if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := c.Put(p, Analysis{BPM: 128, HasGrid: true}); err != nil {
		t.Fatal(err)
	}
	// Change the file: different size and mtime.
	if err := os.WriteFile(p, []byte("a much longer replacement"), 0o644); err != nil {
		t.Fatal(err)
	}
	if n := len(c.All()); n != 0 {
		t.Errorf("All returned %d entries; a replaced file must not keep its beatgrid", n)
	}
}

// TestCacheAllOnAnEmptyCacheMustNotError covers the common first-run state.
func TestCacheAllOnAnEmptyCacheMustNotError(t *testing.T) {
	if n := len(OpenCache(t.TempDir()).All()); n != 0 {
		t.Errorf("an empty cache returned %d entries", n)
	}
	if n := len((&Cache{}).All()); n != 0 {
		t.Errorf("an unopened cache returned %d entries", n)
	}
}
