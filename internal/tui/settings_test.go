package tui

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maguldon/mix-tui/internal/engine"
	"github.com/maguldon/mix-tui/internal/tui/library"
	"github.com/maguldon/mix-tui/internal/tui/views"
)

// settingsModel returns a model with the settings view wired to recording
// stubs, so a test can assert on what a keystroke asked the binary to do
// without touching a filesystem.
func settingsModel(t *testing.T, root string) (*Model, *strings.Builder, *string) {
	t.Helper()
	m, _ := testModel(t)
	m.cfg.UI.LibraryRoot = root
	m.SetLibrarySources("/home/duke/.config/mix-tui/config.yaml", "")

	saved := new(strings.Builder)
	saveErr := new(string)
	m.SetConfigSaver(func(root string) error {
		if *saveErr != "" {
			return errors.New(*saveErr)
		}
		saved.WriteString(root)
		return nil
	})
	m.SetLibraryScanner(func(root string) (*library.Index, error) {
		ix := library.NewIndex()
		ix.Add(library.Track{Path: filepath.Join(root, "one.mp3"), Title: "One"})
		ix.Add(library.Track{Path: filepath.Join(root, "two.mp3"), Title: "Two"})
		return ix, nil
	})
	return m, saved, saveErr
}

// typeString sends each rune of s as its own key press, the way a terminal
// delivers a typed word.
func typeString(m *Model, s string) {
	for _, r := range s {
		m.handleKey(string(r))
	}
}

// TestSettingsOpensAndSeedsTheField covers the entry point and the two things
// that make it usable: S is reachable from the mix view, and the field starts
// holding the value in effect rather than blank. A user arriving to edit an
// existing root should see it, not retype it.
func TestSettingsOpensAndSeedsTheField(t *testing.T) {
	m, _, _ := settingsModel(t, "/home/duke/Music/DJ Music")
	m.handleKey("S")

	if m.st.Mode != ModeSettings {
		t.Fatalf("S did not open settings (mode = %v)", m.st.Mode)
	}
	if got := m.st.SettingInput; got != "/home/duke/Music/DJ Music" {
		t.Errorf("field seeded with %q, want the configured root", got)
	}
}

// TestSettingsKeyIsNotAShadowOfADeckControl guards the choice of key. Every
// lowercase letter is either a deck control or bound by DefaultKeys; shadowing
// one to reach a settings screen would take a control away rather than add a way
// in. In particular "s" is sync, and it must still work from the mix view.
func TestSettingsKeyIsNotAShadowOfADeckControl(t *testing.T) {
	m, f := testModel(t)
	m.handleKey("s")

	if m.st.Mode == ModeSettings {
		t.Error("lowercase s opened settings; it is sync toggle")
	}
	found := false
	for _, a := range f.applied {
		if a.Kind == engine.SyncToggle {
			found = true
		}
	}
	if !found {
		t.Error("s did not reach the sync binding")
	}
}

// TestSettingsIgnoresTheKeyOutsideTheMixView keeps S from being a trapdoor in
// the browser: it is a mode key, and the browser's own keys run first.
func TestSettingsIgnoresTheKeyOutsideTheMixView(t *testing.T) {
	m, _, _ := settingsModel(t, "/music")
	m.st.Mode = ModeLibrary
	m.handleKey("S")
	if m.st.Mode != ModeLibrary {
		t.Errorf("S escaped the browser into settings (mode = %v)", m.st.Mode)
	}
	if !strings.Contains(m.st.Query, "S") {
		t.Errorf("S was not typed into the filter, query = %q", m.st.Query)
	}
}

// TestSettingsAcceptsSpacesInPaths is the reason the handler reads the raw key.
// Directories called "DJ Music" or "My Tracks" are ordinary, and a field that
// dropped the space would produce a path that does not exist -- with no way for
// the user to see why.
func TestSettingsAcceptsSpacesInPaths(t *testing.T) {
	m, _, _ := settingsModel(t, "")
	m.handleKey("S")
	m.st.SettingInput = ""
	typeString(m, "/home/duke/Music/DJ Music")

	if got := m.st.SettingInput; got != "/home/duke/Music/DJ Music" {
		t.Errorf("typed path = %q, want it intact including the space", got)
	}
}

// TestSettingsBackspaceRemovesOneRune covers multi-byte input: deleting a byte
// instead of a rune leaves invalid UTF-8, which renders as U+FFFD.
func TestSettingsBackspaceRemovesOneRune(t *testing.T) {
	m, _, _ := settingsModel(t, "/Musik/über")
	m.handleKey("S")
	m.handleKey("backspace")
	if got := m.st.SettingInput; got != "/Musik/übe" {
		t.Errorf("after backspace = %q, want /Musik/übe", got)
	}
}

// TestSettingsAppliesRootAndRescans is the whole feature: type a path, press
// enter, the config is written and the library is re-scanned.
func TestSettingsAppliesRootAndRescans(t *testing.T) {
	m, saved, _ := settingsModel(t, "/old")
	m.SetLibrary(nil)
	m.handleKey("S")
	m.st.SettingInput = ""
	typeString(m, "/new/music")

	cmd := m.handleKey("enter")

	if saved.String() != "/new/music" {
		t.Errorf("saved %q, want /new/music", saved.String())
	}
	if m.cfg.UI.LibraryRoot != "/new/music" {
		t.Errorf("config in memory = %q, want /new/music", m.cfg.UI.LibraryRoot)
	}
	if !m.st.ScanBusy {
		t.Error("the scan was not marked busy, so the view would look frozen")
	}
	if cmd == nil {
		t.Fatal("no command returned: the rescan would never run")
	}

	// Running the command must not block the UI goroutine, but it must return
	// the new index. Feed it back the way bubbletea would.
	msg, ok := cmd().(scanDoneMsg)
	if !ok {
		t.Fatalf("command returned %T, want scanDoneMsg", msg)
	}
	if msg.root != "/new/music" || msg.err != nil {
		t.Fatalf("scanDoneMsg = %+v", msg)
	}

	m2, _ := m.Update(msg)
	got := m2.(*Model)
	if got.st.ScanBusy {
		t.Error("still busy after the scan returned")
	}
	if len(got.lib.Tracks()) != 2 {
		t.Errorf("library holds %d tracks, want 2 from the rescan", len(got.lib.Tracks()))
	}
	if !strings.Contains(got.st.SettingNote, "2 tracks") {
		t.Errorf("note = %q, want it to report the count", got.st.SettingNote)
	}
}

// TestSettingsExpandsTilde covers the value users actually type. Without it the
// scan reports a root that exists as unreachable.
func TestSettingsExpandsTilde(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	m, saved, _ := settingsModel(t, "")
	m.handleKey("S")
	m.st.SettingInput = ""
	typeString(m, "~/Music")
	m.handleKey("enter")

	want := filepath.Join(home, "Music")
	if saved.String() != want {
		t.Errorf("saved %q, want the expanded %q", saved.String(), want)
	}
}

// TestSettingsRefusesAnEmptyRoot keeps backspacing a directory away from
// silently emptying the library. Clearing the library is a real request, so it
// needs somewhere to be recorded.
func TestSettingsRefusesAnEmptyRoot(t *testing.T) {
	m, saved, _ := settingsModel(t, "/music")
	m.handleKey("S")
	m.st.SettingInput = ""
	cmd := m.handleKey("enter")

	if cmd != nil {
		t.Error("an empty root started a rescan")
	}
	if saved.String() != "" {
		t.Errorf("saved %q, want nothing", saved.String())
	}
	if !strings.Contains(m.st.LastErr, "type a directory") {
		t.Errorf("error = %q, want it to say what to do", m.st.LastErr)
	}
}

// TestSettingsReportsASaveFailure keeps a config the program could not write
// from looking like a save. The usual cause is a path the user cannot write to,
// and silently continuing would leave them believing a setting that is not there.
func TestSettingsReportsASaveFailure(t *testing.T) {
	m, saved, saveErr := settingsModel(t, "/music")
	*saveErr = "permission denied"
	m.handleKey("S")
	m.st.SettingInput = ""
	typeString(m, "/new")

	cmd := m.handleKey("enter")

	if cmd != nil {
		t.Error("a failed save still started a rescan")
	}
	if saved.String() != "" {
		t.Error("the failing saver recorded a value")
	}
	if !strings.Contains(m.st.LastErr, "permission denied") {
		t.Errorf("error = %q, want the cause", m.st.LastErr)
	}
	if m.cfg.UI.LibraryRoot != "/music" {
		t.Errorf("in-memory config changed to %q despite the failed save", m.cfg.UI.LibraryRoot)
	}
}

// TestSettingsReportsAnUnreadableRoot keeps an empty result and a failed scan
// distinguishable. An unreadable root has a fix; a folder with no audio in it
// usually does not, and reporting both as an error would send the user looking
// for a problem they do not have.
func TestSettingsReportsAnUnreadableRoot(t *testing.T) {
	m, _, _ := settingsModel(t, "")
	m.SetLibraryScanner(func(root string) (*library.Index, error) {
		return library.NewIndex(), errors.New("no such file or directory")
	})
	m.handleKey("S")
	m.st.SettingInput = ""
	typeString(m, "/gone")
	cmd := m.handleKey("enter")

	m2, _ := m.Update(cmd())
	got := m2.(*Model)
	if !strings.Contains(got.st.LastErr, "no such file") {
		t.Errorf("error = %q, want the scan failure", got.st.LastErr)
	}
	if got.st.ScanBusy {
		t.Error("still busy after a failed scan")
	}
}

// TestSettingsKeepsTheLibraryWhenTheRescanFails is the safety property: a root
// that cannot be read must not leave the user with nothing. The new index is
// empty, so swapping it in unconditionally would empty the browser as a side
// effect of a typo.
func TestSettingsKeepsTheLibraryWhenTheRescanFails(t *testing.T) {
	m, _, _ := settingsModel(t, "/music")
	old := library.NewIndex()
	old.Add(library.Track{Path: "/music/keep.mp3", Title: "Keep"})
	m.SetLibrary(old)
	m.SetLibraryScanner(func(root string) (*library.Index, error) {
		return nil, errors.New("boom")
	})

	m.handleKey("S")
	m.st.SettingInput = ""
	typeString(m, "/gone")
	cmd := m.handleKey("enter")
	m2, _ := m.Update(cmd())
	got := m2.(*Model)

	if got.lib != old {
		t.Errorf("the existing library was replaced by %v", got.lib)
	}
}

// TestSettingsShowsTheFlagOverride explains the most confusing case there is:
// the user edits the config, the scan returns the same tracks, and nothing on
// screen says why. The -library flag wins, so the view has to say so.
func TestSettingsShowsTheFlagOverride(t *testing.T) {
	m, _ := testModel(t)
	m.SetLibrarySources("/cfg.yaml", "/flag/wins")
	m.handleKey("S")

	frame := m.RenderAt(100, 24)
	if !strings.Contains(frame, "-library /flag/wins overrides") {
		t.Errorf("the override is not shown:\n%s", frame)
	}
}

// TestSettingsRendersTheTrackCountAndConfigPath covers the screen's other job:
// being somewhere the user can see what is set without opening a text editor.
func TestSettingsRendersTheTrackCountAndConfigPath(t *testing.T) {
	m, _ := testModel(t)
	ix := library.NewIndex()
	ix.Add(library.Track{Path: "/a.mp3"})
	ix.Add(library.Track{Path: "/b.mp3"})
	m.SetLibrary(ix)
	m.SetLibrarySources("/cfg/config.yaml", "")
	m.cfg.UI.LibraryRoot = "/music"
	m.handleKey("S")

	frame := m.RenderAt(100, 24)
	for _, want := range []string{"/music", "2", "/cfg/config.yaml"} {
		if !strings.Contains(frame, want) {
			t.Errorf("settings frame is missing %q:\n%s", want, frame)
		}
	}
}

// TestSettingsEscapeCloses keeps the universal exit working from the new mode:
// escape must not be swallowed as text, or there is no way out but ctrl+c.
func TestSettingsEscapeCloses(t *testing.T) {
	m, _, _ := settingsModel(t, "/music")
	m.handleKey("S")
	m.handleKey("escape")
	if m.st.Mode != ModeMix {
		t.Errorf("escape did not close settings (mode = %v)", m.st.Mode)
	}
}

// TestSettingsSurvivesAMissingSaverAndScanner keeps a headless or partially
// wired model from panicking on enter. Every other action reports what is
// missing rather than failing silently.
func TestSettingsSurvivesAMissingSaverAndScanner(t *testing.T) {
	m, _ := testModel(t)
	m.cfg.UI.LibraryRoot = ""
	m.handleKey("S")
	typeString(m, "/music")

	cmd := m.handleKey("enter")
	if cmd != nil {
		t.Error("a model with no scanner returned a command")
	}
	if m.cfg.UI.LibraryRoot != "/music" {
		t.Errorf("root = %q, want it applied even with nothing to persist to", m.cfg.UI.LibraryRoot)
	}
	if !strings.Contains(m.st.SettingNote, "unavailable") {
		t.Errorf("note = %q, want it to say rescanning is unavailable", m.st.SettingNote)
	}
}

// TestEnterOnAnEmptyLibraryExplainsItself is the second half of issue #7.
// loadSelectedInto returned silently when there was nothing to load, so ENTER
// was indistinguishable from ENTER not being wired to anything -- and the usual
// reason the list is empty is that no library root is set.
func TestEnterOnAnEmptyLibraryExplainsItself(t *testing.T) {
	m, _ := testModel(t)
	m.SetLibrary(library.NewIndex())
	m.st.Mode = ModeLibrary
	m.st.LibPane = views.PaneTracks
	m.handleKey("enter")

	if !strings.Contains(m.st.LastErr, "press S") {
		t.Errorf("error = %q, want it to point at the settings screen", m.st.LastErr)
	}
}

// TestEnterWithNoMatchingTracksExplainsItself separates "the library is empty"
// from "your filter matched nothing". They look identical on screen and have
// opposite fixes.
func TestEnterWithNoMatchingTracksExplainsItself(t *testing.T) {
	m, _ := testModel(t)
	ix := library.NewIndex()
	ix.Add(library.Track{Path: "/1.mp3", Title: "One"})
	m.SetLibrary(ix)
	m.st.Mode = ModeLibrary
	m.st.LibPane = views.PaneTracks
	m.st.Query = "zzz"
	m.handleKey("enter")

	if !strings.Contains(m.st.LastErr, "zzz") {
		t.Errorf("error = %q, want it to name the filter", m.st.LastErr)
	}
	if !strings.Contains(m.st.LastErr, "backspace") {
		t.Errorf("error = %q, want it to say how to fix it", m.st.LastErr)
	}
}

// TestSettingsSurvivesTheScreenshotPath is the smoke test that the settings view
// renders headless, since that is how layout work is verified.
func TestSettingsRendersHeadless(t *testing.T) {
	m, _ := testModel(t)
	m.cfg.UI.LibraryRoot = "/music"
	m.SetLibrarySources("/cfg.yaml", "")
	m.handleKey("S")

	for _, size := range [][2]int{{120, 40}, {80, 24}, {40, 12}} {
		frame := m.RenderAt(size[0], size[1])
		if len(strings.Split(frame, "\n")) != size[1] {
			t.Errorf("at %dx%d the frame is not %d lines", size[0], size[1], size[1])
		}
		if !strings.Contains(frame, "SETTINGS") {
			t.Errorf("at %dx%d the settings screen did not render:\n%s", size[0], size[1], frame)
		}
	}
}
