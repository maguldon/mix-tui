package views

import (
	"strings"
	"testing"
)

// stripANSI removes styling so assertions read against plain text.
func stripANSI(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] != 0x1b {
			b.WriteByte(s[i])
			continue
		}
		for i < len(s) && s[i] != 'm' {
			i++
		}
	}
	return b.String()
}

func renderSettingsText(st SettingsState, w, h int) string {
	return stripANSI(RenderSettings(w, h, st))
}

// TestRenderSettingsShowsWhatIsSet covers the screen's reason for existing: the
// user can see what the library is set to without opening a text editor.
func TestRenderSettingsShowsWhatIsSet(t *testing.T) {
	out := renderSettingsText(SettingsState{
		Root:       "/home/duke/Music/DJ Music",
		ConfigPath: "/home/duke/.config/mix-tui/config.yaml",
		Tracks:     6522,
		Input:      "/home/duke/Music/DJ Music",
	}, 100, 24)

	for _, want := range []string{
		"SETTINGS",
		"library root",
		"/home/duke/Music/DJ Music",
		"tracks",
		"6522",
		"config",
		"/home/duke/.config/mix-tui/config.yaml",
		"scan and save",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("settings screen is missing %q:\n%s", want, out)
		}
	}
}

// TestRenderSettingsSaysWhenUnset keeps an absent root from rendering as blank
// space. "(not set)" is the difference between "this is your value" and
// "something failed to draw".
func TestRenderSettingsSaysWhenUnset(t *testing.T) {
	out := renderSettingsText(SettingsState{}, 80, 24)
	if !strings.Contains(out, "not set") {
		t.Errorf("an unset root does not say so:\n%s", out)
	}
}

// TestRenderSettingsShowsTheFlagOverride is the honesty test. The -library flag
// wins over the config, so a user who edits the config and gets the same tracks
// back needs to be told why on the screen.
func TestRenderSettingsShowsTheFlagOverride(t *testing.T) {
	out := renderSettingsText(SettingsState{
		Root:     "/from/config",
		FlagRoot: "/from/flag",
	}, 100, 24)
	if !strings.Contains(out, "-library /from/flag overrides") {
		t.Errorf("the override is not shown:\n%s", out)
	}
}

// TestRenderSettingsQuietWithoutAFlag keeps the warning from becoming noise: most
// users never pass -library, and a permanent warning would be ignored.
func TestRenderSettingsQuietWithoutAFlag(t *testing.T) {
	out := renderSettingsText(SettingsState{Root: "/music"}, 100, 24)
	if strings.Contains(out, "overrides") {
		t.Errorf("an override warning was shown with no flag:\n%s", out)
	}
}

// TestRenderSettingsSaysItIsScanning keeps a rescan legible. Scanning a real
// collection takes seconds; without a line saying so, the interface looks
// identical whether the keypress worked or not.
func TestRenderSettingsSaysItIsScanning(t *testing.T) {
	out := renderSettingsText(SettingsState{Busy: true}, 100, 24)
	if !strings.Contains(out, "scanning") {
		t.Errorf("no scanning line while busy:\n%s", out)
	}
}

// TestRenderSettingsRendersAtAnyWidth keeps this screen usable when the library
// browser cannot be. The browser refuses below 120 columns; a user whose
// terminal is narrow reaches the settings screen precisely because the browser
// is telling them it will not draw, so a minimum width here would close the only
// door left.
func TestRenderSettingsRendersAtAnyWidth(t *testing.T) {
	for _, w := range []int{40, 60, 80, 120, 200} {
		out := renderSettingsText(SettingsState{
			Root:       "/a/very/long/path/that/exceeds/the/width/of/this/terminal/for/sure",
			ConfigPath: "/cfg.yaml",
		}, w, 20)
		if !strings.Contains(out, "SETTINGS") {
			t.Errorf("at %d columns the screen did not render:\n%s", w, out)
		}
		for _, line := range strings.Split(out, "\n") {
			if got := len([]rune(line)); got != w {
				t.Errorf("at %d columns a line is %d runes wide", w, got)
			}
		}
	}
}

// TestRenderSettingsIsAFrame keeps the output an exact rectangle. A ragged frame
// makes a full-screen app repaint noise.
func TestRenderSettingsIsAFrame(t *testing.T) {
	out := RenderSettings(90, 18, SettingsState{Root: "/music", Input: "/music"})
	lines := strings.Split(out, "\n")
	if len(lines) != 18 {
		t.Errorf("frame has %d lines, want 18", len(lines))
	}
}
