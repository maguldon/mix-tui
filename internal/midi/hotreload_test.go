package midi

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/maguldon/mix-tui/internal/engine"
)

func writeMapping(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestReloadOncePicksUpChanges(t *testing.T) {
	dir := t.TempDir()
	writeMapping(t, dir, "a.yaml", `
device: "A"
controls:
  - midi: {kind: cc, chan: 1, num: 9}
    action: {kind: DeckPitch, target: 0}
`)
	h := NewHandler(nil, func(engine.Action) {})
	w := NewWatcher(dir, h, t.Logf)
	n, errs := w.ReloadOnce()
	if n != 1 {
		t.Fatalf("changed = %d, want 1", n)
	}
	if len(errs) != 0 {
		t.Fatalf("errs = %v", errs)
	}
	if h.Table() == nil {
		t.Fatal("handler table not set")
	}
}

func TestReloadKeepsLastValidTableOnParseError(t *testing.T) {
	// Review Focus #4: a typo mid-set must not stop MIDI.
	dir := t.TempDir()
	writeMapping(t, dir, "a.yaml", `
device: "A"
controls:
  - midi: {kind: cc, chan: 1, num: 9}
    action: {kind: DeckPitch, target: 0}
`)
	h := NewHandler(nil, func(engine.Action) {})
	w := NewWatcher(dir, h, t.Logf)
	w.ReloadOnce()
	before := h.Table()

	writeMapping(t, dir, "a.yaml", "device: \"A\"\ncontrols:\n  - actoin: {kind: DeckPitch}\n")
	n, errs := w.ReloadOnce()
	if len(errs) == 0 {
		t.Fatal("expected a parse error")
	}
	if n != 0 {
		t.Errorf("changed = %d, want 0 on parse failure", n)
	}
	if h.Table() != before {
		t.Error("handler table was replaced by a failed parse")
	}
}

func TestReloadIsANoopWhenNothingChanged(t *testing.T) {
	dir := t.TempDir()
	writeMapping(t, dir, "a.yaml", "device: \"A\"\ncontrols: []\n")
	h := NewHandler(nil, func(engine.Action) {})
	w := NewWatcher(dir, h, t.Logf)
	w.ReloadOnce()
	n, errs := w.ReloadOnce()
	if n != 0 {
		t.Errorf("second scan changed %d files, want 0", n)
	}
	if len(errs) != 0 {
		t.Errorf("errs on unchanged scan: %v", errs)
	}
}

func TestWatcherRunsAndStops(t *testing.T) {
	dir := t.TempDir()
	h := NewHandler(nil, func(engine.Action) {})
	w := NewWatcher(dir, h, t.Logf)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { w.Run(ctx); close(done) }()
	writeMapping(t, dir, "a.yaml", "device: \"A\"\ncontrols: []\n")
	time.Sleep(2500 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Watcher.Run did not stop on cancel")
	}
}

func TestLoadDeviceTablesIgnoresNonYAML(t *testing.T) {
	dir := t.TempDir()
	writeMapping(t, dir, "a.yaml", "device: \"A\"\ncontrols: []\n")
	writeMapping(t, dir, "notes.txt", "ignore me")
	tables, errs := LoadDeviceTables(dir)
	if len(tables) != 1 {
		t.Errorf("loaded %d tables, want 1", len(tables))
	}
	if len(errs) != 0 {
		t.Errorf("errs = %v", errs)
	}
}

// TestReloadRetriesABrokenFile covers the plan's rule that a failed file is
// retried: the stamp is recorded only on success, so fixing the file must make
// the very next scan pick it up. If a broken file were marked seen, the fix
// would be silently ignored until the next edit.
func TestReloadRetriesABrokenFile(t *testing.T) {
	dir := t.TempDir()
	writeMapping(t, dir, "a.yaml", "device: \"A\"\ncontrols:\n  - actoin: {kind: DeckPitch}\n") // broken
	h := NewHandler(nil, func(engine.Action) {})
	w := NewWatcher(dir, h, t.Logf)
	if _, errs := w.ReloadOnce(); len(errs) == 0 {
		t.Fatal("expected the broken file to error")
	}
	// Fix it; the next scan must reload even though the file was seen before.
	writeMapping(t, dir, "a.yaml", "device: \"A\"\ncontrols:\n  - midi: {kind: cc, chan: 1, num: 9}\n    action: {kind: DeckPitch, target: 0}\n")
	n, errs := w.ReloadOnce()
	if len(errs) != 0 {
		t.Fatalf("fixed file still errors: %v", errs)
	}
	if n != 1 {
		t.Errorf("changed = %d after fixing, want 1", n)
	}
	if h.Table() == nil {
		t.Error("fixed file was not installed")
	}
}

// TestOneBadFileDoesNotBlockOthers ensures a single broken mapping does not
// stop the rest from loading — the difference between one dead button and a
// dead controller.
func TestOneBadFileDoesNotBlockOthers(t *testing.T) {
	dir := t.TempDir()
	writeMapping(t, dir, "good.yaml", "device: \"Good\"\ncontrols:\n  - midi: {kind: cc, chan: 1, num: 9}\n    action: {kind: DeckPitch, target: 0}\n")
	writeMapping(t, dir, "bad.yaml", "device: \"Bad\"\ncontrols:\n  - actoin: {kind: DeckPitch}\n")
	tables, errs := LoadDeviceTables(dir)
	if len(errs) != 1 {
		t.Errorf("errs = %v, want exactly one", errs)
	}
	if _, ok := tables["Good"]; !ok {
		t.Error("good mapping was blocked by the bad one")
	}
}

// TestEachDeviceGetsItsOwnMapping covers spec 5.1: mappings are matched by
// device name. The application installed whichever mapping file compiled last,
// so a K2 could be served the generic table -- and Table.Matches, which exists
// precisely to select by name, had no caller outside its own test.
func TestEachDeviceGetsItsOwnMapping(t *testing.T) {
	dir := t.TempDir()
	writeMapping(t, dir, "k2.yaml", `
device: "xone-k2"
controls:
  - midi: {kind: cc, chan: 1, num: 9}
    action: {kind: DeckPitch, target: 0}
`)
	writeMapping(t, dir, "generic.yaml", `
device: "generic-2deck"
controls:
  - midi: {kind: cc, chan: 1, num: 11}
    action: {kind: DeckCue, target: 0}
`)

	h := NewHandler(nil, func(engine.Action) {})
	w := NewWatcher(dir, h, func(string, ...any) {})
	if _, errs := w.ReloadOnce(); len(errs) > 0 {
		t.Fatalf("reload: %v", errs)
	}

	k2 := h.tableForDevice("xone-k2")
	gen := h.tableForDevice("generic-2deck")
	if k2 == nil || gen == nil {
		t.Fatalf("device lookup failed: k2=%v generic=%v", k2, gen)
	}
	if k2 == gen {
		t.Fatal("both devices resolved to the same table")
	}
}

// TestUnmatchedDeviceFallsBack. A device with no mapping of its own must still
// work through the installed table rather than going dead.
func TestUnmatchedDeviceFallsBack(t *testing.T) {
	dir := t.TempDir()
	writeMapping(t, dir, "k2.yaml", `
device: "xone-k2"
controls:
  - midi: {kind: cc, chan: 1, num: 9}
    action: {kind: DeckPitch, target: 0}
`)

	h := NewHandler(nil, func(engine.Action) {})
	w := NewWatcher(dir, h, func(string, ...any) {})
	if _, errs := w.ReloadOnce(); len(errs) > 0 {
		t.Fatalf("reload: %v", errs)
	}

	if h.Table() == nil {
		t.Fatal("no table installed; an unmapped device would be dead")
	}
	if h.tableForDevice("some-other-controller") != nil {
		t.Error("an unmatched device name should not select a table by name")
	}
}

// TestHotReloadReplacesDeviceTables. After deleting one file the whole set must
// be reinstalled, or the removed mapping stays live.
func TestHotReloadReplacesDeviceTables(t *testing.T) {
	dir := t.TempDir()
	writeMapping(t, dir, "k2.yaml", `
device: "xone-k2"
controls:
  - midi: {kind: cc, chan: 1, num: 9}
    action: {kind: DeckPitch, target: 0}
`)
	writeMapping(t, dir, "generic.yaml", `
device: "generic-2deck"
controls:
  - midi: {kind: cc, chan: 1, num: 11}
    action: {kind: DeckCue, target: 0}
`)

	h := NewHandler(nil, func(engine.Action) {})
	w := NewWatcher(dir, h, func(string, ...any) {})
	if _, errs := w.ReloadOnce(); len(errs) > 0 {
		t.Fatalf("reload: %v", errs)
	}

	if err := os.Remove(filepath.Join(dir, "generic.yaml")); err != nil {
		t.Fatal(err)
	}
	// Touch the survivor so a rescan is triggered by its stamp.
	survivor := filepath.Join(dir, "k2.yaml")
	future := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(survivor, future, future); err != nil {
		t.Fatal(err)
	}

	w.ReloadOnce()
	if h.tableForDevice("generic-2deck") != nil {
		t.Error("a deleted mapping is still installed")
	}
	if h.tableForDevice("xone-k2") == nil {
		t.Error("the surviving mapping was dropped")
	}
}
