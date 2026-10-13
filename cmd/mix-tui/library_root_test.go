package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/maguldon/mix-tui/internal/config"
)

// TestConfigPathExistsBeforeTheFile does covers the fresh-install case that
// discoverConfig cannot serve.
//
// discoverConfig returns "" for a config that does not exist, which is right for
// loading and useless for saving: a user with no config file is exactly the user
// opening the settings screen, and a "" path would make the one action that can
// fix their setup the one action unavailable.
func TestConfigPathExistsBeforeTheFile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	want := filepath.Join(dir, "mix-tui", "config.yaml")
	if got := configPath(""); got != want {
		t.Errorf("configPath = %q, want %q even though no file exists", got, want)
	}
	if _, err := os.Stat(want); !os.IsNotExist(err) {
		t.Fatalf("test setup: expected no config file to exist yet")
	}
	if got := discoverConfig(""); got != "" {
		t.Errorf("discoverConfig = %q, want empty for a missing file", got)
	}
}

// TestConfigPathPrefersFlag keeps an explicit -config winning for saves too,
// so the settings screen writes where the application was told to read.
func TestConfigPathPrefersFlag(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if got := configPath("/tmp/explicit.yaml"); got != "/tmp/explicit.yaml" {
		t.Errorf("configPath = %q, want the explicit path", got)
	}
}

// TestStartupRootComesFromConfigWhenNoFlag is the regression test for issue #7:
// the scan consulted the -library flag alone, so ui.library_root was parsed,
// validated, and discarded. Launching without the flag must now still find the
// music.
func TestStartupRootComesFromConfigWhenNoFlag(t *testing.T) {
	dir := t.TempDir()
	music := t.TempDir()
	for _, name := range []string{"one.mp3", "two.wav"} {
		if err := os.WriteFile(filepath.Join(music, name), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cfgPath := filepath.Join(dir, "config.yaml")
	body := "ui:\n  library_root: " + music + "\n"
	if err := os.WriteFile(cfgPath, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := config.Load(discoverConfig(cfgPath))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	args, err := parseArgs([]string{"-no-audio", "-no-midi"})
	if err != nil {
		t.Fatal(err)
	}

	root := config.ResolveLibraryRoot(args.libraryRoot, cfg.UI.LibraryRoot)
	if root != music {
		t.Fatalf("resolved root = %q, want the configured %q", root, music)
	}
}

// TestStartupRootFlagWins keeps -library authoritative for a single run: a path
// typed at the prompt is an explicit request, and a config file the user may not
// remember setting should not override it.
func TestStartupRootFlagWins(t *testing.T) {
	args, err := parseArgs([]string{"-library", "/from/flag", "-no-audio"})
	if err != nil {
		t.Fatal(err)
	}
	if got := config.ResolveLibraryRoot(args.libraryRoot, "/from/config"); got != "/from/flag" {
		t.Errorf("resolved root = %q, want /from/flag", got)
	}
}

// TestConfigRoundTripsThroughTheSettingsSaver is the end-to-end shape of the
// feature: the settings screen writes the root, and the next launch reads it
// back without a flag.
func TestConfigRoundTripsThroughTheSettingsSaver(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	p := configPath("")

	// A user with no config at all.
	if got := discoverConfig(""); got != "" {
		t.Fatalf("expected no config to exist, got %q", got)
	}
	if err := config.SaveLibraryRoot(p, "/home/duke/Music"); err != nil {
		t.Fatalf("SaveLibraryRoot: %v", err)
	}
	cfg, err := config.Load(discoverConfig(""))
	if err != nil {
		t.Fatalf("Load after save: %v", err)
	}
	if got := config.ResolveLibraryRoot("", cfg.UI.LibraryRoot); got != "/home/duke/Music" {
		t.Errorf("next launch would scan %q, want /home/duke/Music", got)
	}
}
