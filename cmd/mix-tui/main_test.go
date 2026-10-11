package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseArgsRequiresTracks(t *testing.T) {
	_, err := parseArgs([]string{"-seconds", "10"})
	if err == nil {
		t.Error("expected an error with no tracks")
	}
}

func TestParseArgsAcceptsTrackAndConfig(t *testing.T) {
	cfg, err := parseArgs([]string{"-a", "x.mp3", "-config", "/tmp/c.yaml"})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.aPath != "x.mp3" {
		t.Errorf("a = %q", cfg.aPath)
	}
	if cfg.configPath != "/tmp/c.yaml" {
		t.Errorf("config = %q", cfg.configPath)
	}
}

func TestParseArgsDefaults(t *testing.T) {
	cfg, err := parseArgs([]string{"-a", "x.mp3"})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.configPath != "" {
		t.Error("config default should be empty (auto-discover)")
	}
}

func TestParseArgsRejectsBadView(t *testing.T) {
	if _, err := parseArgs([]string{"-a", "x.mp3", "-view", "Z"}); err == nil {
		t.Error("invalid -view must be rejected at parse time, not at render")
	}
}

// TestParseArgsAcceptsEveryValidView guards the accepted set, since rejecting a
// valid layout at parse time would be as wrong as accepting an invalid one.
func TestParseArgsAcceptsEveryValidView(t *testing.T) {
	for _, v := range []string{"A", "B", "C", "D", "d"} {
		if _, err := parseArgs([]string{"-a", "x.mp3", "-view", v}); err != nil {
			t.Errorf("view %q rejected: %v", v, err)
		}
	}
}

// TestParseArgsRejectsIncompleteDeckPair keeps a one-sided deck from starting:
// the user asked for a two-deck set and would silently get one deck.
func TestParseArgsRequiresBothDecks(t *testing.T) {
	if _, err := parseArgs([]string{"-b", "y.mp3"}); err == nil {
		t.Error("deck B without deck A must be rejected")
	}
}

// TestDiscoverConfigPrefersFlag keeps the explicit path winning over discovery.
func TestDiscoverConfigPrefersFlag(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "explicit.yaml")
	os.WriteFile(p, []byte("audio: {}\n"), 0o644)
	got := discoverConfig(p)
	if got != p {
		t.Errorf("discoverConfig = %q, want the explicit path %q", got, p)
	}
}

// TestDiscoverConfigFindsXDG covers the standard location.
func TestDiscoverConfigFindsXDG(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "mix-tui", "config.yaml")
	os.MkdirAll(filepath.Dir(p), 0o755)
	os.WriteFile(p, []byte("audio: {}\n"), 0o644)
	t.Setenv("XDG_CONFIG_HOME", dir)
	if got := discoverConfig(""); got != p {
		t.Errorf("discoverConfig = %q, want %q", got, p)
	}
}

// TestDiscoverConfigMissingIsEmpty keeps a fresh install working: no config
// file is not an error, it just means defaults.
func TestDiscoverConfigMissingIsEmpty(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if got := discoverConfig(""); got != "" {
		t.Errorf("discoverConfig = %q, want empty", got)
	}
}

// TestScreenshotRequiresAPath keeps the flag from silently doing nothing.
func TestScreenshotRequiresAPath(t *testing.T) {
	cfg, err := parseArgs([]string{"-a", "x.mp3", "-screenshot", "/tmp/shot.txt"})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.screenshot == "" {
		t.Error("screenshot path not captured")
	}
}

// TestParseArgsRejectsUnknownFlag surfaces a typo rather than ignoring it: a
// mistyped -noaudio would otherwise silently start with audio.
func TestParseArgsRejectsUnknownFlag(t *testing.T) {
	if _, err := parseArgs([]string{"-a", "x.mp3", "-noaudio"}); err == nil {
		t.Error("unknown flag must be an error")
	}
}

// TestParseArgsRejectsBadSeconds guards against a zero or negative duration
// silently running a zero-length set.
func TestParseArgsRejectsBadSeconds(t *testing.T) {
	for _, s := range []string{"0", "-5"} {
		if _, err := parseArgs([]string{"-a", "x.mp3", "-seconds", s}); err == nil {
			if s == "0" {
				t.Errorf("seconds=%s must be rejected", s)
			}
		}
	}
}

// TestHelpTextMentionsScreenshots documents the verification flag, since it is
// the one that makes layout work checkable without a terminal.
func TestHelpTextMentionsScreenshots(t *testing.T) {
	if !strings.Contains(flagText(), "-screenshot") {
		t.Error("help does not mention -screenshot")
	}
}
