package main

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/maguldon/mix-tui/internal/tui/library"
)

// wavFixture writes a decodable .wav and returns its path, so a scan has
// something real to find. Indexing stats and probes files, so an empty file
// would be skipped as undecodable and the test would pass for the wrong reason.
func wavFixture(t *testing.T, dir, name string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	src := "../../internal/testdata/fixtures/sine440.wav"
	data, err := os.ReadFile(src)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// TestScanLibraryIndexesAndAppliesCachedAnalysis guards the extraction of the
// scan into its own function.
//
// ApplyAnalysis and EvictMissing moved here along with the scan, and that is
// the part with teeth: applied to an index that is still empty, ApplyAnalysis
// restores nothing and reports zero. A future edit that hoisted the walk back
// out of this function, or reordered it, would silently turn "every track knows
// its BPM on first paint" back into "every track learns its BPM after a second
// play" -- and every test in the repo would still pass, because the browser
// test drives the index directly and never goes near startup.
func TestScanLibraryIndexesAndAppliesCachedAnalysis(t *testing.T) {
	root := t.TempDir()
	track := wavFixture(t, root, "Artist - Title.mp3")

	cacheDir := t.TempDir()
	cache := library.OpenCache(filepath.Join(cacheDir, "analysis"))
	if err := cache.Put(track, library.Analysis{
		Path: track, BPM: 128, HasGrid: true, KeyCamelot: "8A", KeyConfidence: 0.9,
	}); err != nil {
		t.Fatalf("seed cache: %v", err)
	}

	ix := library.NewIndex()
	scanLibrary(context.Background(), ix, root, cache)

	if ix.Len() != 1 {
		t.Fatalf("indexed %d tracks, want 1", ix.Len())
	}
	got := ix.Tracks()[0]
	if got.Path != track {
		t.Errorf("indexed %q, want %q", got.Path, track)
	}
	// The cached values are the whole reason ApplyAnalysis is in here: without it
	// these stay zero and the browser's BPM column reads "--" until the track is
	// played a second time.
	if got.BPM != 128 || !got.HasGrid {
		t.Errorf("BPM = %v (grid %v), want 128 with a grid: the cache was not applied after the scan", got.BPM, got.HasGrid)
	}
	if got.KeyCamelot != "8A" {
		t.Errorf("key = %q, want 8A", got.KeyCamelot)
	}
}

// TestScanLibraryOnAnEmptyRootDoesNothing covers a fresh install, where no
// library has been pointed at yet. It must not create an index entry, and must
// not report a scan error for a root that was never set.
func TestScanLibraryOnAnEmptyRootDoesNothing(t *testing.T) {
	ix := library.NewIndex()
	scanLibrary(context.Background(), ix, "", library.OpenCache(t.TempDir()))
	if ix.Len() != 0 {
		t.Errorf("indexed %d tracks from an unset library root", ix.Len())
	}
}

// TestScanLibraryEvictsAnalysesForDeletedTracks keeps the spec §8 promise that
// the cache does not grow forever describing a collection the user no longer has.
// EvictMissing moved into the scan goroutine, so this is the check that it did
// not get lost in the move.
func TestScanLibraryEvictsAnalysesForDeletedTracks(t *testing.T) {
	root := t.TempDir()
	present := wavFixture(t, root, "here.mp3")

	cache := library.OpenCache(filepath.Join(t.TempDir(), "analysis"))
	if err := cache.Put(present, library.Analysis{Path: present, BPM: 128, HasGrid: true}); err != nil {
		t.Fatal(err)
	}
	gone := filepath.Join(root, "deleted.mp3")
	if err := cache.Put(gone, library.Analysis{Path: gone, BPM: 90, HasGrid: true}); err != nil {
		t.Fatal(err)
	}

	ix := library.NewIndex()
	scanLibrary(context.Background(), ix, root, cache)

	if _, ok := cache.Get(gone); ok {
		t.Error("an analysis for a deleted track survived EvictMissing")
	}
	if _, ok := cache.Get(present); !ok {
		t.Error("an analysis for a track still on disk was evicted")
	}
}

// TestScanLibraryStopsWithTheApplication checks the goroutine is tied to the
// app context rather than to context.Background.
//
// Derived from Background it would outlive the UI: on quit, a goroutine would
// carry on stat-ing a user's music folder with nobody left to read the result.
func TestScanLibraryStopsWithTheApplication(t *testing.T) {
	root := t.TempDir()
	for _, n := range []string{"a.mp3", "b.mp3", "c.mp3"} {
		wavFixture(t, root, n)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already shut down, as it would be at exit

	ix := library.NewIndex()
	done := make(chan struct{})
	go func() {
		defer close(done)
		scanLibrary(ctx, ix, root, library.OpenCache(t.TempDir()))
	}()

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("scanLibrary ignored a cancelled context")
	}
	if ix.Len() != 0 {
		t.Errorf("indexed %d tracks after cancellation", ix.Len())
	}
}

// TestStartupDoesNotBlockOnTheLibraryScan is the regression test for the
// freeze.
//
// The scan used to run inline before the first frame, bounded by a 30s cap that
// a real collection reliably exceeded. On this machine that is 40GB and ~7,200
// tracks, so every launch sat on a blank terminal for the full 30 seconds and
// then printed "library scan: context deadline exceeded" -- the failure was the
// normal case, not the exception. The acceptance suite inherited it and died on
// the 10-minute test timeout.
//
// A timing threshold is the wrong way to test this: a library of a few hundred
// files walks in well under a second, so an "is it fast enough" assertion either
// does not reproduce the bug or is flaky across machines. Instead the walk is
// made to hang outright. A FIFO named like a track is stat-able, so it passes
// the extension check and gets indexed; opening it for the duration probe then
// waits for a writer that never comes. The scan can therefore never finish, and
// the only remaining question is the one that matters: does startup wait for it?
func TestStartupDoesNotBlockOnTheLibraryScan(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a binary")
	}
	bin := buildBin(t)

	root := t.TempDir()
	fifo := filepath.Join(root, "blocker.mp3")
	if err := syscall.Mkfifo(fifo, 0o644); err != nil {
		t.Skipf("no FIFOs here: %v", err)
	}

	shot := filepath.Join(t.TempDir(), "frame.txt")
	// Generous: the point is a walk that never completes, not a slow disk.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin,
		"-a", "../../internal/testdata/fixtures/sine440.wav",
		"-no-audio", "-no-midi",
		"-library", root,
		"-screenshot", shot,
	)
	cmd.Env = hermeticEnv(t)

	out, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatalf("startup waited on the library scan: the binary was still running after 30s with an unwalkable library\n%s", out)
	}
	if err != nil {
		t.Fatalf("run: %v\n%s", err, out)
	}
	if bytes.Contains(out, []byte("library scan")) {
		t.Errorf("startup attempted a library scan:\n%s", out)
	}
	if _, err := os.Stat(shot); err != nil {
		t.Errorf("no frame was written: %v", err)
	}
}

// TestAcceptanceRunIgnoresTheUsersConfig is the hermeticity check.
//
// runShot points XDG_CONFIG_HOME and XDG_CACHE_HOME at temporary directories.
// Without that, these tests resolve the developer's own library_root and
// default_view, so the suite's behaviour depends on whose machine it runs on:
// it was green on CI and on any machine without a config file, and unusable on
// the desks of people who actually use the app.
func TestAcceptanceRunIgnoresTheUsersConfig(t *testing.T) {
	env := hermeticEnv(t)
	joined := strings.Join(env, "\n")
	for _, k := range []string{"XDG_CONFIG_HOME=", "XDG_CACHE_HOME="} {
		if !strings.Contains(joined, k) {
			t.Errorf("hermeticEnv does not set %s", k)
		}
	}
	// And the override must actually win over an inherited value.
	cmd := exec.Command("sh", "-c", "echo $XDG_CONFIG_HOME")
	cmd.Env = env
	got, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(got)) == "" {
		t.Error("XDG_CONFIG_HOME is empty under hermeticEnv")
	}
}
