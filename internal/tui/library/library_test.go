package library

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func tmpTracks(t *testing.T, n int) string {
	t.Helper()
	root := t.TempDir()
	for i := 0; i < n; i++ {
		name := filepath.Join(root, "track"+string(rune('a'+i))+".mp3")
		if err := os.WriteFile(name, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestScanFindsFiles(t *testing.T) {
	root := tmpTracks(t, 5)
	ix := NewIndex()
	if err := ix.Scan(context.Background(), []string{root}, nil, nil); err != nil {
		t.Fatal(err)
	}
	if ix.Len() != 5 {
		t.Errorf("found %d, want 5", ix.Len())
	}
}

func TestScanSkipsMissingRootWithoutFailing(t *testing.T) {
	// spec §8: one bad path must not empty the library
	root := tmpTracks(t, 3)
	ix := NewIndex()
	err := ix.Scan(context.Background(), []string{root, "/nonexistent/xyz"}, nil, nil)
	if err == nil {
		t.Error("expected an error for the missing root")
	}
	if ix.Len() != 3 {
		t.Errorf("good root yielded %d tracks, want 3", ix.Len())
	}
}

func TestScanRespectsContextCancel(t *testing.T) {
	root := tmpTracks(t, 3)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	ix := NewIndex()
	_ = ix.Scan(ctx, []string{root}, nil, nil)
	// must return promptly; may be partial
}

func TestSortDoesNotMutateInput(t *testing.T) {
	ts := []Track{{Title: "b", BPM: 200}, {Title: "a", BPM: 100}}
	orig := append([]Track{}, ts...)
	Sort(ts, SortBPM, false)
	if ts[0].Title != orig[0].Title {
		t.Error("Sort mutated its input")
	}
	if Sort(ts, SortBPM, false)[0].BPM != 100 {
		t.Error("ascending sort wrong")
	}
	if Sort(ts, SortBPM, true)[0].BPM != 200 {
		t.Error("descending sort wrong")
	}
}

func TestSortPlacesUndetectedLast(t *testing.T) {
	ts := []Track{{Title: "x", BPM: 0}, {Title: "y", BPM: 128}}
	s := Sort(ts, SortBPM, false)
	if s[len(s)-1].BPM != 0 {
		t.Error("undetected BPM must sort last")
	}
}

func TestSearchMatchesTitleArtistAndPath(t *testing.T) {
	ts := []Track{
		{Title: "Warehouse", Artist: "Klara", Path: "/m/a.wav"},
		{Title: "Samba", Artist: "Jockstrap", Path: "/m/b.wav"},
	}
	if len(Search(ts, "ware")) != 1 {
		t.Error("title match failed")
	}
	if len(Search(ts, "jock")) != 1 {
		t.Error("artist match failed")
	}
	if len(Search(ts, "/m/b")) != 1 {
		t.Error("path match failed")
	}
	if len(Search(ts, "")) != 2 {
		t.Error("empty query must return everything")
	}
}

func TestCacheRoundTrip(t *testing.T) {
	d := t.TempDir()
	c := OpenCache(d)
	a := Analysis{BPM: 128, KeyCamelot: "8A", KeyConfidence: 0.9,
		Size: 100, ModTime: time.Now()}
	if err := c.Put("/x/y.wav", a); err != nil {
		t.Fatal(err)
	}
	got, ok := c.Get("/x/y.wav")
	if !ok {
		t.Fatal("cache miss")
	}
	if got.BPM != 128 || got.KeyCamelot != "8A" {
		t.Errorf("round trip lost data: %+v", got)
	}
}

func TestCacheInvalidatesOnContentChange(t *testing.T) {
	// spec §4.1: replaced audio must invalidate
	d := t.TempDir()
	f := filepath.Join(d, "t.wav")
	os.WriteFile(f, []byte("original"), 0o644)
	st, _ := os.Stat(f)
	c := OpenCache(d)
	c.Put(f, Analysis{BPM: 128, Size: st.Size(), ModTime: st.ModTime()})
	if _, ok := c.Get(f); !ok {
		t.Fatal("fresh entry missing")
	}

	time.Sleep(10 * time.Millisecond)
	os.WriteFile(f, []byte("a completely different file"), 0o644)
	if _, ok := c.Get(f); ok {
		t.Error("cache hit after the file changed; stale analysis would be wrong")
	}
}

func TestCacheMissingRootDoesNotError(t *testing.T) {
	c := OpenCache(filepath.Join(t.TempDir(), "nonexistent"))
	if _, ok := c.Get("/x"); ok {
		t.Error("empty cache reported a hit")
	}
}

func TestCrateRoundTrip(t *testing.T) {
	p := filepath.Join(t.TempDir(), "crates.yaml")
	in := Crate{Name: "Warm", Tracks: []string{"/a.mp3", "/b.mp3"}}
	if err := SaveCrate(p, in); err != nil {
		t.Fatal(err)
	}
	out, err := LoadCrates(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 || len(out[0].Tracks) != 2 {
		t.Errorf("round trip lost data: %+v", out)
	}
}

// TestCrateSaveMerges pins spec 6.5: saving one crate must not delete the others.
func TestCrateSaveMerges(t *testing.T) {
	p := filepath.Join(t.TempDir(), "crates.yaml")
	if err := SaveCrate(p, Crate{Name: "Warm", Tracks: []string{"/a.mp3"}}); err != nil {
		t.Fatal(err)
	}
	if err := SaveCrate(p, Crate{Name: "Peak Time", Tracks: []string{"/b.mp3"}}); err != nil {
		t.Fatal(err)
	}
	out, err := LoadCrates(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 2 {
		t.Fatalf("got %d crates, want 2: saving one must not delete the other", len(out))
	}
}

// TestCrateSaveReplacesSameName checks a re-save updates rather than duplicating.
func TestCrateSaveReplacesSameName(t *testing.T) {
	p := filepath.Join(t.TempDir(), "crates.yaml")
	SaveCrate(p, Crate{Name: "Warm", Tracks: []string{"/a.mp3"}})
	SaveCrate(p, Crate{Name: "Warm", Tracks: []string{"/z.mp3"}})
	out, _ := LoadCrates(p)
	if len(out) != 1 {
		t.Fatalf("got %d crates, want 1", len(out))
	}
	if out[0].Tracks[0] != "/z.mp3" {
		t.Errorf("crate not replaced: %v", out[0].Tracks)
	}
}

// TestScanSkipsHiddenDirs keeps .git and dotfile directories out of a library.
func TestScanSkipsHiddenDirs(t *testing.T) {
	root := tmpTracks(t, 2)
	hidden := filepath.Join(root, ".hidden")
	os.MkdirAll(hidden, 0o755)
	os.WriteFile(filepath.Join(hidden, "secret.mp3"), []byte("x"), 0o644)
	ix := NewIndex()
	if err := ix.Scan(context.Background(), []string{root}, nil, nil); err != nil {
		t.Fatal(err)
	}
	if ix.Len() != 2 {
		t.Errorf("found %d, want 2 (hidden dir must be skipped)", ix.Len())
	}
}

// TestScanOnlyIndexesDecodableFormats keeps the index in step with audio.Decode.
// Listing a format the decoder cannot open means every listed track fails on
// load, which is worse than an absent track.
func TestScanOnlyIndexesDecodableFormats(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"a.wav", "b.mp3", "c.flac", "d.ogg",
		"e.m4a", "f.aiff", "g.aif", "h.mov", "i.wma"} {
		os.WriteFile(filepath.Join(root, name), []byte("x"), 0o644)
	}
	ix := NewIndex()
	if err := ix.Scan(context.Background(), []string{root}, nil, nil); err != nil {
		t.Fatal(err)
	}
	if ix.Len() != 4 {
		var got []string
		for _, tr := range ix.Tracks() {
			got = append(got, filepath.Base(tr.Path))
		}
		t.Errorf("indexed %d tracks %v, want the 4 the decoder can open", ix.Len(), got)
	}
}

// TestScanIgnoresNonAudio guards against indexing every file in a music folder.
func TestScanIgnoresNonAudio(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "a.mp3"), []byte("x"), 0o644)
	os.WriteFile(filepath.Join(root, "cover.jpg"), []byte("x"), 0o644)
	os.WriteFile(filepath.Join(root, "notes.txt"), []byte("x"), 0o644)
	os.WriteFile(filepath.Join(root, "set.cue"), []byte("x"), 0o644)
	ix := NewIndex()
	if err := ix.Scan(context.Background(), []string{root}, nil, nil); err != nil {
		t.Fatal(err)
	}
	if ix.Len() != 1 {
		t.Errorf("found %d, want 1 (only .mp3 is audio)", ix.Len())
	}
}

// TestScanPublishesProgress guards the incremental contract: the UI must be
// able to show progress rather than waiting for the whole walk.
func TestScanPublishesProgress(t *testing.T) {
	root := tmpTracks(t, 4)
	ix := NewIndex()
	var seen int
	var published int
	err := ix.Scan(context.Background(), []string{root},
		func(done, total int) { seen = done },
		func(Track) { published++ })
	if err != nil {
		t.Fatal(err)
	}
	if published != 4 {
		t.Errorf("published %d tracks, want 4", published)
	}
	if seen == 0 {
		t.Error("no progress reported")
	}
}

// TestScanRescansDoesNotDuplicate guards repeated scans, which happen when the
// user changes a library root.
func TestScanRescansDoesNotDuplicate(t *testing.T) {
	root := tmpTracks(t, 3)
	ix := NewIndex()
	ctx := context.Background()
	ix.Scan(ctx, []string{root}, nil, nil)
	ix.Scan(ctx, []string{root}, nil, nil)
	if ix.Len() != 3 {
		t.Errorf("after two scans Len = %d, want 3", ix.Len())
	}
}

// TestScanDoesNotDecode is the performance contract: scanning must not decode
// audio, or opening a library would take minutes. The fixtures are not valid
// audio, so a decode attempt would fail them.
func TestScanDoesNotDecode(t *testing.T) {
	root := tmpTracks(t, 2)
	ix := NewIndex()
	if err := ix.Scan(context.Background(), []string{root}, nil, nil); err != nil {
		t.Fatalf("scan must not decode (fixtures are not real audio): %v", err)
	}
}

// TestTracksIsACopy keeps a caller from mutating the index's internal slice.
func TestTracksIsACopy(t *testing.T) {
	root := tmpTracks(t, 2)
	ix := NewIndex()
	ix.Scan(context.Background(), []string{root}, nil, nil)
	got := ix.Tracks()
	if len(got) != 2 {
		t.Fatalf("got %d tracks", len(got))
	}
	got[0].Title = "mutated"
	if ix.Tracks()[0].Title == "mutated" {
		t.Error("Tracks returned the live slice")
	}
}

// TestSearchIsCaseInsensitive matches how a DJ actually types.
func TestSearchIsCaseInsensitive(t *testing.T) {
	ts := []Track{{Title: "Warehouse", Path: "/m/a.wav"}}
	if len(Search(ts, "WAREHOUSE")) != 1 {
		t.Error("search must be case-insensitive")
	}
}

// TestLoadCratesMissingFileYieldsEmpty treats an absent crate file as no
// crates, not an error: a fresh install has none.
func TestLoadCratesMissingFileYieldsEmpty(t *testing.T) {
	out, err := LoadCrates(filepath.Join(t.TempDir(), "nope.yaml"))
	if err != nil {
		t.Fatalf("missing crate file must not error: %v", err)
	}
	if len(out) != 0 {
		t.Errorf("got %d crates from a missing file", len(out))
	}
}

// TestCacheFileNameIsPathSafe guards against a path with separators in it
// escaping the cache directory.
func TestCacheFileNameIsPathSafe(t *testing.T) {
	dir := t.TempDir()
	c := OpenCache(dir)
	// Use a real file so the entry carries a real stamp.
	path := filepath.Join(dir, "t.wav")
	os.WriteFile(path, []byte("audio"), 0o644)
	if err := c.Put(path, Analysis{BPM: 1}); err != nil {
		t.Fatal(err)
	}
	if _, ok := c.Get(path); !ok {
		t.Error("cache failed to round-trip a real path")
	}

	// A path with separators must hash to a name inside the cache dir, not
	// escape it.
	weird := "../../escape.wav"
	if err := c.Put(weird, Analysis{BPM: 2}); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() == "escape.wav" || strings.Contains(e.Name(), "/") {
			t.Errorf("cache entry %q escaped the cache directory", e.Name())
		}
	}
}

// TestCacheUnstampedEntryIsNotTrusted covers the corrected staleness rule: an
// entry with no stamp describes a file we have never seen, so it must miss
// rather than serve an unverifiable beatgrid.
func TestCacheUnstampedEntryIsNotTrusted(t *testing.T) {
	d := t.TempDir()
	c := OpenCache(d)
	// A path that does not exist, with no stamp recorded.
	c.Put("/definitely/not/here.wav", Analysis{BPM: 128})
	if _, ok := c.Get("/definitely/not/here.wav"); ok {
		t.Error("an unstamped entry for an unseen file was trusted")
	}
}

func TestSortByTitleIsStable(t *testing.T) {
	ts := []Track{{Title: "b"}, {Title: "a"}, {Title: "c"}}
	s := Sort(ts, SortTitle, false)
	got := []string{s[0].Title, s[1].Title, s[2].Title}
	want := []string{"a", "b", "c"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("got %v, want %v", got, want)
	}
}
