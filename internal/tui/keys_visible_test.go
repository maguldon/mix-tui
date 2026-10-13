package tui

import (
	"sort"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/maguldon/mix-tui/internal/analyze"
	"github.com/maguldon/mix-tui/internal/audio"
	"github.com/maguldon/mix-tui/internal/config"
	"github.com/maguldon/mix-tui/internal/dsp"
	"github.com/maguldon/mix-tui/internal/engine"
)

// The user-visible contract for a key: pressing it changes what is on screen.
//
// A binding that reaches the engine but moves nothing in the interface is
// indistinguishable, to the person holding the keyboard, from a dead key. That
// is how master gain, the cue/program blend and sync all passed review: each
// one applied correctly, and each one showed nothing at all.
//
// The rig is rebuilt for every key so presses cannot contaminate each other,
// and the decks are played with a real beatgrid so hotcues, beat loops and
// sync are genuinely available rather than silently gated off.
func TestEveryDefaultKeyChangesTheScreen(t *testing.T) {
	keys, err := config.NewKeyResolver(config.Defaults())
	if err != nil {
		t.Fatal(err)
	}

	// A hotcue jump to an empty slot is expected to change nothing; that is
	// covered by its own error-reporting test.
	skip := map[string]bool{"2": true, "4": true, "6": true, "8": true}
	// UI navigation keys, listed so an omission is deliberate rather than
	// silent.
	uiKeys := []string{"v", "z", "Z", "l", "/", "escape", "tab",
		"?", "`", "ctrl+c", "space"}

	all := uiKeys
	for k := range config.DefaultKeys() {
		all = append(all, k)
	}
	sort.Strings(all)

	var dead []string
	for _, name := range all {
		if skip[name] {
			continue
		}
		eng := engine.New(engine.Config{
			SampleRate: 44100, BlockFrames: engine.BlockFrames, DeckCount: 2,
		})
		pcm := make(dsp.Stereo, 44100*2)
		for i := range pcm {
			pcm[i] = 0.2
		}
		tr := &audio.Track{Path: "sweep.mp3", SampleRate: 44100,
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
		eng.Deck(1).Play()
		for i := 0; i < 60; i++ {
			eng.RenderInto(audio.NewRing(1<<16), audio.NewRing(1<<16))
		}

		m := New(eng, config.Defaults(), keys)
		m.st.Mode = ModeMix
		before := m.RenderAt(120, 40)

		switch name {
		case "space":
			m.Update(tea.KeyMsg{Type: tea.KeySpace})
		case "ctrl+c":
			m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
		default:
			m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(name)})
		}
		for i := 0; i < 4; i++ {
			eng.RenderInto(audio.NewRing(1<<16), audio.NewRing(1<<16))
		}

		if m.RenderAt(120, 40) == before {
			dead = append(dead, name)
		}
	}
	if len(dead) > 0 {
		t.Errorf("%d key(s) do nothing visible: %v", len(dead), dead)
	}
}
