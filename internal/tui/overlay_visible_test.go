package tui

import (
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/maguldon/mix-tui/internal/analyze"
	"github.com/maguldon/mix-tui/internal/audio"
	"github.com/maguldon/mix-tui/internal/config"
	"github.com/maguldon/mix-tui/internal/dsp"
	"github.com/maguldon/mix-tui/internal/engine"
	"github.com/maguldon/mix-tui/internal/tui/library"
)

// rig builds a model with a real engine, two loaded decks and a small library,
// which is the state the overlay is meant to be used from.
func overlayRig(t *testing.T) (*Model, *engine.Engine, *library.Index) {
	t.Helper()
	keys, err := config.NewKeyResolver(config.Defaults())
	if err != nil {
		t.Fatal(err)
	}
	eng := engine.New(engine.Config{
		SampleRate: 44100, BlockFrames: engine.BlockFrames, DeckCount: 2,
	})
	pcm := make(dsp.Stereo, 44100*2)
	for i := range pcm {
		pcm[i] = 0.2
	}
	tr := &audio.Track{Path: "deck.mp3", SampleRate: 44100,
		Channels: 2, PCM: pcm, Duration: 4 * time.Second}
	beats := make([]time.Duration, 512)
	for i := range beats {
		beats[i] = time.Duration(i) * time.Second / 2
	}
	grid := &analyze.BeatGrid{BPM: 120, Confidence: 0.9, Beats: beats,
		Downbeats: []int{0, 4, 8, 12}}
	eng.Deck(0).LoadNow(tr, grid, nil)
	eng.Deck(1).LoadNow(tr, grid, nil)
	eng.Deck(0).Play()
	for i := 0; i < 60; i++ {
		eng.RenderInto(audio.NewRing(1<<16), audio.NewRing(1<<16))
	}

	ix := library.NewIndex()
	for i, name := range []string{"alpha", "beta", "gamma", "delta"} {
		ix.Add(library.Track{
			Path: "/m/" + name + ".mp3", Title: name,
			Artist: "artist", Duration: time.Duration(60*(i+1)) * time.Second,
		})
	}
	m := New(eng, config.Defaults(), keys)
	m.SetLibrary(ix)
	// A real loader, so ENTER exercises the path the application uses.
	m.SetLoader(func(deck int, path string) error {
		eng.Deck(deck).LoadWith(&audio.Track{
			Path: path, SampleRate: 44100, Channels: 2,
			PCM: pcm, Duration: 2 * time.Second,
		}, grid, nil)
		return nil
	})
	return m, eng, ix
}

// The overlay is only useful if it can be opened, searched, navigated and
// loaded from. Each of those is checked by whether the SCREEN changes, which is
// the same question the keyboard sweep asks.
func TestOverlayIsUsableEndToEnd(t *testing.T) {
	steps := []struct {
		name string
		key  tea.KeyMsg
		what string
	}{
		{"open", tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("l")}, "pressing l opens the overlay"},
		{"type", tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")}, "typing filters the list"},
		{"backspace", tea.KeyMsg{Type: tea.KeyBackspace}, "backspace edits the query"},
		{"down", tea.KeyMsg{Type: tea.KeyDown}, "down moves the selection"},
		{"up", tea.KeyMsg{Type: tea.KeyUp}, "up moves the selection"},
		{"load", tea.KeyMsg{Type: tea.KeyEnter}, "enter loads the track"},
	}
	m, eng, _ := overlayRig(t)

	for _, s := range steps {
		before := m.RenderAt(120, 40)
		m.Update(s.key)
		for i := 0; i < 2; i++ {
			for i := 0; i < 2; i++ {
				_ = eng.RenderInto(audio.NewRing(1<<16), audio.NewRing(1<<16))
			}
		}
		if m.RenderAt(120, 40) == before {
			t.Errorf("%s: the screen did not change", s.what)
		}
	}
}

// TestOverlayTabMovesDeckFocus covers the overlay's TAB.
//
// handleBrowserKey cycled LibPane for every browser mode, but views.OverlayRows
// references no pane: the overlay draws only the track list. So TAB moved a
// cursor through panes that were never rendered and nothing on screen changed --
// pressing it did nothing a user could perceive.
func TestOverlayTabMovesDeckFocus(t *testing.T) {
	m, _, _ := overlayRig(t)
	m.st.Focus = 0
	press(t, m, "l")
	if m.st.Mode != ModeOverlay {
		t.Fatalf("l did not open the overlay (mode %v)", m.st.Mode)
	}

	press(t, m, "tab")

	if m.st.Focus != 1 {
		t.Errorf("tab left deck focus at %d, want 1", m.st.Focus)
	}
	if m.st.Mode != ModeOverlay {
		t.Errorf("tab closed the overlay (mode %v); it should still be open", m.st.Mode)
	}
}

// TestOverlayTabThenEnterLoadsToTheOtherDeck is the whole point of the fix:
// with SHIFT+ENTER unreachable, this is how the other deck gets a track.
func TestOverlayTabThenEnterLoadsToTheOtherDeck(t *testing.T) {
	m, _, _ := overlayRig(t)
	m.st.Focus = 0
	press(t, m, "l")
	press(t, m, "tab")
	press(t, m, "enter")

	if m.st.Mode != ModeMix {
		t.Fatalf("enter did not load and return to the decks (mode %v)", m.st.Mode)
	}
	if m.st.Focus != 1 {
		t.Errorf("focus = %d, want 1; the load should have gone to the other deck", m.st.Focus)
	}
}

// TestLibraryTabStillCyclesPanes guards the other half. The full browser really
// does render crates and tracks, so TAB must keep meaning pane focus there --
// otherwise the same key means two different things one keystroke apart.
func TestLibraryTabStillCyclesPanes(t *testing.T) {
	m, _ := libraryModel(t)
	deckBefore := m.st.Focus
	paneBefore := m.st.LibPane

	press(t, m, "tab")

	if m.st.Focus != deckBefore {
		t.Errorf("tab moved deck focus from %d to %d in the browser", deckBefore, m.st.Focus)
	}
	if m.st.LibPane == paneBefore {
		t.Errorf("tab did not cycle the pane (stayed at %v)", paneBefore)
	}
}
