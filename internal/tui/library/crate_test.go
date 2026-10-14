package library

import (
	"os"
	"path/filepath"
	"testing"
)

func trackPaths() []string { return []string{"/m/a.mp3", "/m/b.mp3", "/m/c.mp3"} }

func indexed() *Index {
	ix := NewIndex()
	for _, p := range trackPaths() {
		ix.Add(Track{Path: p, Title: filepath.Base(p), BPM: 128, Duration: 180e9})
	}
	return ix
}

// TestCratesRoundTrip covers the whole store, which had none: LoadCrates and
// SaveCrate existed and had no production caller, so the crate pane rendered
// two placeholder rows and a DJ could not build a set (spec 6.5).
func TestCratesRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "crates.yaml")

	crates := []Crate{
		{Name: "Warm Up", Tracks: []string{"/m/a.mp3", "/m/b.mp3"}},
		{Name: "Peak Time", Tracks: []string{"/m/c.mp3"}},
	}
	if err := SaveCrates(path, crates); err != nil {
		t.Fatalf("SaveCrates: %v", err)
	}
	got, err := LoadCrates(path)
	if err != nil {
		t.Fatalf("LoadCrates: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("read back %d crates, want 2", len(got))
	}
	if got[0].Name != "Warm Up" || len(got[0].Tracks) != 2 {
		t.Errorf("crate 0 = %+v", got[0])
	}
	if got[1].Name != "Peak Time" {
		t.Errorf("crate 1 = %+v", got[1])
	}
}

// TestCrateTracksPreservesCrateOrder is the property that makes a crate worth
// having. It is a curated running order, and a browse order is not.
func TestCrateTracksPreservesCrateOrder(t *testing.T) {
	ix := indexed()
	// Deliberately not the scan order.
	ix.SetCrates([]Crate{{Name: "Set", Tracks: []string{"/m/c.mp3", "/m/a.mp3"}}})

	got, missing := ix.CrateTracks("Set")
	if missing != 0 {
		t.Errorf("missing = %d, want 0", missing)
	}
	if len(got) != 2 {
		t.Fatalf("got %d tracks, want 2", len(got))
	}
	if got[0].Path != "/m/c.mp3" || got[1].Path != "/m/a.mp3" {
		t.Errorf("order = %s, %s; want the crate's own", got[0].Path, got[1].Path)
	}
}

// TestCrateTracksCountsMissing keeps a crate naming a deleted file from
// silently showing a shorter set, which reads as data loss.
func TestCrateTracksCountsMissing(t *testing.T) {
	ix := indexed()
	ix.SetCrates([]Crate{{Name: "Set", Tracks: []string{"/m/a.mp3", "/m/gone.mp3"}}})

	got, missing := ix.CrateTracks("Set")
	if len(got) != 1 || missing != 1 {
		t.Errorf("got %d tracks and %d missing, want 1 and 1", len(got), missing)
	}
}

// TestAddToCrate covers the explicit add, which spec 6.5 requires: a crate is
// not a view of the filesystem, tracks go in by hand.
func TestAddToCrate(t *testing.T) {
	ix := indexed()
	ix.SetCrates([]Crate{{Name: "Set", Tracks: []string{"/m/a.mp3"}}})

	if !ix.AddToCrate("Set", "/m/b.mp3") {
		t.Fatal("AddToCrate reported no change for a new track")
	}
	got, _ := ix.CrateTracks("Set")
	if len(got) != 2 || got[1].Path != "/m/b.mp3" {
		t.Errorf("crate = %+v, want the track appended", got)
	}

	// Adding the same track twice is not a change, so the caller can say so.
	if ix.AddToCrate("Set", "/m/b.mp3") {
		t.Error("adding a duplicate reported a change")
	}
	if got, _ := ix.CrateTracks("Set"); len(got) != 2 {
		t.Errorf("a duplicate add grew the crate to %d", len(got))
	}
}

// TestAddToCrateCreatesIt covers adding with no crate selected, which is the
// common case. Refusing would mean a key that only works once the user knows to
// create a crate first.
func TestAddToCrateCreatesIt(t *testing.T) {
	ix := indexed()
	if !ix.AddToCrate(ix.NextCrateName(), "/m/a.mp3") {
		t.Fatal("AddToCrate did not create a crate")
	}
	if len(ix.Crates()) != 1 {
		t.Fatalf("crates = %d, want 1", len(ix.Crates()))
	}
}

// TestRemoveFromCrate covers removal.
func TestRemoveFromCrate(t *testing.T) {
	ix := indexed()
	ix.SetCrates([]Crate{{Name: "Set", Tracks: []string{"/m/a.mp3", "/m/b.mp3"}}})

	if !ix.RemoveFromCrate("Set", "/m/a.mp3") {
		t.Fatal("RemoveFromCrate reported no change")
	}
	got, _ := ix.CrateTracks("Set")
	if len(got) != 1 || got[0].Path != "/m/b.mp3" {
		t.Errorf("crate = %+v", got)
	}
	if ix.RemoveFromCrate("Set", "/m/a.mp3") {
		t.Error("removing an absent track reported a change")
	}
}

// TestInLibraryIsSet covers the field that existed for exactly this and was set
// by nothing.
func TestInLibraryIsSet(t *testing.T) {
	ix := indexed()
	if ix.Tracks()[0].InLibrary {
		t.Fatal("a track is marked in a library with no crates")
	}
	ix.SetCrates([]Crate{{Name: "Set", Tracks: []string{"/m/a.mp3"}}})

	var in, out int
	for _, tr := range ix.Tracks() {
		if tr.InLibrary {
			in++
		} else {
			out++
		}
	}
	if in != 1 || out != 2 {
		t.Errorf("%d marked in, %d not; want 1 and 2", in, out)
	}
}

// TestNextCrateNameDoesNotCollide checks it prefers the lowest free number, so
// deleting a crate does not renumber the rest.
func TestNextCrateNameDoesNotCollide(t *testing.T) {
	ix := NewIndex()
	if got := ix.NextCrateName(); got != "Crate 1" {
		t.Errorf("first name = %q", got)
	}
	ix.SetCrates([]Crate{{Name: "Crate 1"}, {Name: "Crate 3"}})
	if got := ix.NextCrateName(); got != "Crate 2" {
		t.Errorf("with Crate 1 and 3 taken, next = %q, want Crate 2", got)
	}
}

// TestNormalizeDropsEmptyAndDuplicatePaths covers the hygiene SaveCrates relies
// on: an empty entry is a track that does not exist and a duplicate shows the
// same track twice and loads it twice from one keystroke.
func TestNormalizeDropsEmptyAndDuplicatePaths(t *testing.T) {
	c := Crate{Name: "x", Tracks: []string{"/a", "", "/a", "/b", "/a"}}.Normalize()
	if len(c.Tracks) != 2 || c.Tracks[0] != "/a" || c.Tracks[1] != "/b" {
		t.Errorf("normalized = %v, want [/a /b]", c.Tracks)
	}
}

// TestCratesSurviveAMissingFile keeps a fresh install quiet: no crates is not a
// failure.
func TestCratesSurviveAMissingFile(t *testing.T) {
	got, err := LoadCrates(filepath.Join(t.TempDir(), "nope.yaml"))
	if err != nil {
		t.Errorf("a missing crate file errored: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %d crates from a missing file", len(got))
	}
}

// TestSaveCratesReplacesRatherThanMerging keeps the UI's write path honest.
// SaveCrate merged, which is right for a single-crate edit but would drop every
// crate the user had if the UI rewrote the list from a stale copy.
func TestSaveCratesReplacesRatherThanMerging(t *testing.T) {
	path := filepath.Join(t.TempDir(), "crates.yaml")
	if err := SaveCrates(path, []Crate{{Name: "One"}, {Name: "Two"}}); err != nil {
		t.Fatal(err)
	}
	if err := SaveCrates(path, []Crate{{Name: "Two"}}); err != nil {
		t.Fatal(err)
	}
	got, _ := LoadCrates(path)
	if len(got) != 1 || got[0].Name != "Two" {
		t.Errorf("after a full rewrite: %+v", got)
	}
}

// TestScanDoesNotWipeCrates covers a rescan. The index is rebuilt from the
// filesystem, and the crates are the user's curated data, not something the
// scan discovered.
func TestScanDoesNotWipeCrates(t *testing.T) {
	ix := indexed()
	ix.SetCrates([]Crate{{Name: "Keep", Tracks: []string{"/m/a.mp3"}}})

	dir := t.TempDir()
	for _, p := range trackPaths() {
		if err := os.WriteFile(filepath.Join(dir, filepath.Base(p)), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	ix.Scan(t.Context(), []string{dir}, nil, nil)

	if len(ix.Crates()) != 1 || ix.Crates()[0].Name != "Keep" {
		t.Errorf("a rescan changed the crates: %+v", ix.Crates())
	}
}

var _ = os.Stat
