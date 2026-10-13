package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestExpandHome covers the one expansion the library root needs. The obvious
// thing to write in a config file is ~/Music, and handing that to the scanner
// verbatim reports a root that does exist as unreachable.
func TestExpandHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	tests := []struct{ in, want string }{
		{"", ""},
		{"~/Music", filepath.Join(home, "Music")},
		{"/absolute/path", "/absolute/path"},
		{"relative/path", "relative/path"},
		{"~", home},
		// Another user's home is not ours to resolve. Leaving it alone means it
		// fails loudly as an unreachable root rather than quietly pointing at
		// this user's directory.
		{"~someoneelse/Music", "~someoneelse/Music"},
		{"~notauser", "~notauser"},
	}
	for _, tc := range tests {
		if got := ExpandHome(tc.in); got != tc.want {
			t.Errorf("ExpandHome(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestExpandHomeWithoutHomeFailsClosed keeps a broken HOME from producing a
// path rooted at "/": ExpandHome("~/Music") with no home directory would
// otherwise return "/Music", which is a real directory on most systems and a
// silent scan of the wrong thing.
func TestExpandHomeWithoutHomeFailsClosed(t *testing.T) {
	t.Setenv("HOME", "")
	if got := ExpandHome("~/Music"); got != "~/Music" {
		t.Errorf("ExpandHome with no home = %q, want the input unchanged", got)
	}
}

// TestResolveLibraryRootPrefersFlag keeps an explicit -library winning: a path
// typed at the prompt is a request for this run, and a config file the user may
// not remember setting should not override it.
func TestResolveLibraryRootPrefersFlag(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	if got := ResolveLibraryRoot("/from/flag", "/from/config"); got != "/from/flag" {
		t.Errorf("flag should win: got %q", got)
	}
	// Whitespace-only flag is not a request, so the config still applies.
	if got := ResolveLibraryRoot("  ", "/from/config"); got != "/from/config" {
		t.Errorf("blank flag should fall through to config: got %q", got)
	}
	if got := ResolveLibraryRoot("", ""); got != "" {
		t.Errorf("no root anywhere should stay empty, got %q", got)
	}
}

// TestResolveLibraryRootReadsConfig is the test for the bug in issue #7:
// ui.library_root parsed, validated, and was then ignored.
func TestResolveLibraryRootReadsConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")

	if err := os.WriteFile(path, []byte("ui:\n  library_root: ~/Music\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.UI.LibraryRoot != "~/Music" {
		t.Fatalf("LibraryRoot = %q, want the value from the file", cfg.UI.LibraryRoot)
	}
	got := ResolveLibraryRoot("", cfg.UI.LibraryRoot)
	if want := filepath.Join(home, "Music"); got != want {
		t.Errorf("resolved root = %q, want %q", got, want)
	}
}

// TestSaveLibraryRootCreatesFile covers the fresh-install case, which is the one
// that matters most: a user with no config file is exactly the user opening the
// settings screen, and a save that refused to create the file would deny them
// the only fix available.
func TestSaveLibraryRootCreatesFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mix-tui", "config.yaml")

	if err := SaveLibraryRoot(path, "/home/duke/Music"); err != nil {
		t.Fatalf("SaveLibraryRoot: %v", err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load after save: %v", err)
	}
	if cfg.UI.LibraryRoot != "/home/duke/Music" {
		t.Errorf("round trip = %q, want /home/duke/Music", cfg.UI.LibraryRoot)
	}
}

// TestSaveLibraryRootPreservesTheFile is the important one. Saving by
// re-marshalling the decoded struct would dump every default into the user's
// file, losing comments, keybindings, and field order -- and because unknown
// fields are errors, the user would then be editing a file the program invented.
func TestSaveLibraryRootPreservesTheFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	original := `# my mix-tui config
ui:
  # where my music lives
  library_root: /old/path
  default_view: C
keys:
  space: {action: DeckPlay}
  q: {action: Quantize}
audio:
  sample_rate: 48000
`
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := SaveLibraryRoot(path, "/new/path"); err != nil {
		t.Fatalf("SaveLibraryRoot: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	s := string(got)
	for _, want := range []string{
		"# my mix-tui config",
		"# where my music lives",
		"library_root: /new/path",
		"default_view: C",
		"space: {action: DeckPlay}",
		"q: {action: Quantize}",
		"sample_rate: 48000",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("saved config lost %q:\n%s", want, s)
		}
	}
	if strings.Contains(s, "/old/path") {
		t.Errorf("saved config kept the old root:\n%s", s)
	}

	// And it must still load, strictly.
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("saved config no longer loads: %v\n%s", err, s)
	}
	if cfg.UI.LibraryRoot != "/new/path" || cfg.UI.DefaultView != "C" || cfg.Audio.SampleRate != 48000 {
		t.Errorf("saved config lost settings: %+v", cfg)
	}
}

// TestSaveLibraryRootAddsMissingSections covers a config that exists but has no
// ui block at all.
func TestSaveLibraryRootAddsMissingSections(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("keys:\n  space: {action: DeckPlay}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := SaveLibraryRoot(path, "/music"); err != nil {
		t.Fatalf("SaveLibraryRoot: %v", err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.UI.LibraryRoot != "/music" {
		t.Errorf("LibraryRoot = %q, want /music", cfg.UI.LibraryRoot)
	}
	if _, ok := cfg.Keys["space"]; !ok {
		t.Error("the existing keybindings were dropped")
	}
}

// TestSaveLibraryRootRefusesToClobber is the honesty test. A `ui: 3` in a config
// file means the user wrote something else; overwriting it with a mapping to
// make a save succeed would destroy their input silently.
func TestSaveLibraryRootRefusesToClobber(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("ui: 3\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := SaveLibraryRoot(path, "/music"); err == nil {
		t.Error("SaveLibraryRoot overwrote a non-mapping ui: it must refuse")
	}
	got, _ := os.ReadFile(path)
	if string(got) != "ui: 3\n" {
		t.Errorf("file was modified despite the error: %q", got)
	}
}

// TestSaveLibraryRootRejectsBadInput keeps the two obvious mistakes reported
// rather than performed: writing nowhere, and clearing the library by accident.
func TestSaveLibraryRootRejectsBadInput(t *testing.T) {
	dir := t.TempDir()
	if err := SaveLibraryRoot("", "/music"); err == nil {
		t.Error("SaveLibraryRoot with no path must fail")
	}
	if err := SaveLibraryRoot(filepath.Join(dir, "c.yaml"), ""); err == nil {
		t.Error("SaveLibraryRoot with an empty root must fail")
	}
}

// TestSaveLibraryRootRejectsUnparseable keeps a broken file from being
// overwritten with a valid one, which would discard whatever the user was
// trying to write.
func TestSaveLibraryRootRejectsUnparseable(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	bad := "ui:\n  library_root: [unclosed\n"
	if err := os.WriteFile(path, []byte(bad), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := SaveLibraryRoot(path, "/music"); err == nil {
		t.Error("SaveLibraryRoot overwrote an unparseable config")
	}
	if got, _ := os.ReadFile(path); string(got) != bad {
		t.Errorf("file was modified despite the error: %q", got)
	}
}

// TestSaveLibraryRootOnEmptyFile keeps "an empty file means all defaults" -- a
// state Load accepts -- writable.
func TestSaveLibraryRootOnEmptyFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := SaveLibraryRoot(path, "/music"); err != nil {
		t.Fatalf("SaveLibraryRoot on an empty file: %v", err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.UI.LibraryRoot != "/music" {
		t.Errorf("LibraryRoot = %q, want /music", cfg.UI.LibraryRoot)
	}
}

// TestDescribeLinesIncludesHardcodedKeys covers the help screen's blind spot:
// the navigation keys are handled before any binding lookup, so nothing else
// records them, and they were missing from the listing. "l" and "tab" were
// already in that position.
func TestDescribeLinesIncludesHardcodedKeys(t *testing.T) {
	r, err := NewKeyResolver(Defaults())
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Join(r.DescribeLines(), "\n")
	for _, want := range []string{"l  quick-load overlay", "tab  move deck focus", "S  settings: library root"} {
		if !strings.Contains(lines, want) {
			t.Errorf("help listing is missing %q:\n%s", want, lines)
		}
	}
}

// TestDescribeLinesPrefersBindings keeps a rebindable key from being described
// by the hardcoded row. If a user moves "l" onto a real action, the hardcoded
// description would be the one that is wrong.
func TestDescribeLinesPrefersBindings(t *testing.T) {
	cfg := Defaults()
	cfg.Keys = map[string]KeyBinding{"l": {Action: "MasterGain", Value: "inc"}}
	r, err := NewKeyResolver(cfg)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Join(r.DescribeLines(), "\n")
	if strings.Contains(lines, "l  quick-load overlay") {
		t.Errorf("a rebound key took the hardcoded description:\n%s", lines)
	}
	if !strings.Contains(lines, "l  MixerMasterGain") {
		t.Errorf("the rebound key is missing:\n%s", lines)
	}
}
