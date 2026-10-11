package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"

	"github.com/maguldon/mix-tui/internal/engine"
)

func emptyDeck() engine.DeckState { return engine.DeckState{Index: 0, Loaded: false} }

func TestDeckPanelShowsTrackMetadata(t *testing.T) {
	p := renderDeckPanel(40, snapTwoDecks().Decks[0], ViewState{}, true, 12)
	out := strings.Join(RenderPanel(p, 14), "\n")
	for _, want := range []string{"Track A", "128.00", "A"} {
		if !strings.Contains(out, want) {
			t.Errorf("deck panel missing %q:\n%s", want, out)
		}
	}
}

func TestDeckPanelShowsBPMWithoutGridAsUnknown(t *testing.T) {
	d := snapTwoDecks().Decks[0]
	d.BPM = 0
	d.HasBeatGrid = false
	out := strings.Join(RenderPanel(renderDeckPanel(40, d, ViewState{}, false, 12), 14), "\n")
	if strings.Contains(out, "0.00") {
		t.Error("a deck with no beatgrid must not show 0.00 BPM")
	}
	if !strings.Contains(out, "--") {
		t.Errorf("expected a placeholder, got:\n%s", out)
	}
}

func TestDeckPanelShowsKeyWithConfidence(t *testing.T) {
	d := snapTwoDecks().Decks[0]
	d.KeyCamelot, d.KeyConfidence = "8A", 0.91
	out := strings.Join(RenderPanel(renderDeckPanel(44, d, ViewState{}, false, 12), 14), "\n")
	if !strings.Contains(out, "8A") {
		t.Errorf("key missing:\n%s", out)
	}

	d.KeyCamelot, d.KeyConfidence = "9A", 0.30 // below threshold
	out = strings.Join(RenderPanel(renderDeckPanel(44, d, ViewState{}, false, 12), 14), "\n")
	if strings.Contains(out, "9A") {
		t.Errorf("a below-threshold key must render as undetected:\n%s", out)
	}
}

func TestDeckPanelShowsLoopState(t *testing.T) {
	d := snapTwoDecks().Decks[0]
	d.LoopActive, d.LoopBeatCount = true, 4
	out := strings.Join(RenderPanel(renderDeckPanel(44, d, ViewState{}, true, 12), 14), "\n")
	if !strings.Contains(out, "LOOP 4") {
		t.Errorf("loop size not shown:\n%s", out)
	}
}

func TestEmptyDeckShowsArtNotBlank(t *testing.T) {
	out := strings.Join(RenderPanel(renderDeckPanel(40, emptyDeck(), ViewState{}, false, 12), 14), "\n")
	if !strings.Contains(out, "L") {
		t.Errorf("expected a load hint:\n%s", out)
	}
}

func TestMixerPanelShowsBothFaders(t *testing.T) {
	s := snapTwoDecks()
	s.Crossfader = 0.3
	out := strings.Join(RenderPanel(renderMixerPanel(24, s, ViewState{}), 12), "\n")
	if !strings.Contains(out, "A") || !strings.Contains(out, "B") {
		t.Errorf("both channel labels required:\n%s", out)
	}
	if !strings.Contains(out, "MASTER") {
		t.Errorf("master missing:\n%s", out)
	}
}

func TestPanelWidthIsExactAtManySizes(t *testing.T) {
	for _, w := range []int{24, 28, 32, 40, 48, 52} {
		p := renderDeckPanel(w, snapTwoDecks().Decks[0], ViewState{}, true, 12)
		for i, l := range RenderPanel(p, 14) {
			if lipgloss.Width(l) != w {
				t.Errorf("w=%d line %d is %d wide", w, i, lipgloss.Width(l))
			}
		}
	}
}

// TestDeckPanelDropsLeastImportantFieldsFirst pins the narrow-panel rule: when
// the BPM/key/pitch/time row does not fit, whole fields are dropped in priority
// order rather than truncating mid-field into nonsense like "128.00 BP".
func TestDeckPanelDropsLeastImportantFieldsFirst(t *testing.T) {
	d := snapTwoDecks().Decks[0]
	d.KeyCamelot, d.KeyConfidence = "8A", 0.9
	wide := strings.Join(RenderPanel(renderDeckPanel(52, d, ViewState{}, true, 12), 14), "\n")
	narrow := strings.Join(RenderPanel(renderDeckPanel(24, d, ViewState{}, true, 12), 14), "\n")

	if !strings.Contains(wide, "128.00") {
		t.Fatalf("wide panel should show BPM:\n%s", wide)
	}
	if strings.Contains(narrow, "128.00 BP") {
		t.Errorf("narrow panel truncated a field mid-text:\n%s", narrow)
	}
	for _, line := range strings.Split(narrow, "\n") {
		if strings.Contains(line, "128.00 B") {
			t.Errorf("narrow panel truncated a field mid-text: %q", line)
		}
	}
}

// TestDeckPanelNeverPanicsAcrossSizes guards the row-construction path.
func TestDeckPanelNeverPanicsAcrossSizes(t *testing.T) {
	for _, w := range []int{0, 1, 2, 5, 10, 20, 30, 60, 120} {
		for _, h := range []int{0, 1, 2, 5, 14, 40} {
			p := renderDeckPanel(w, snapTwoDecks().Decks[0], ViewState{}, true, 12)
			_ = RenderPanel(p, h)
			p2 := renderDeckPanel(w, emptyDeck(), ViewState{}, false, 12)
			_ = RenderPanel(p2, h)
		}
	}
}

// TestMixerPanelWidthIsExact checks the mixer panel holds its width, since it
// sits between two deck panels and any overflow misaligns the whole layout.
func TestMixerPanelWidthIsExact(t *testing.T) {
	for _, w := range []int{14, 18, 24, 30} {
		for i, l := range RenderPanel(renderMixerPanel(w, snapTwoDecks(), ViewState{}), 12) {
			if lipgloss.Width(l) != w {
				t.Errorf("mixer w=%d line %d is %d", w, i, lipgloss.Width(l))
			}
		}
	}
}

// TestRenderArtNeverExceedsBounds checks embedded art is clipped to the space
// it is given rather than pushing a panel wider.
func TestRenderArtNeverExceedsBounds(t *testing.T) {
	for _, name := range []string{"splash", "empty", "accent", "nonexistent"} {
		for _, w := range []int{0, 1, 5, 20, 40, 80} {
			for _, h := range []int{0, 1, 3, 10} {
				for _, line := range RenderArt(w, h, name) {
					if lipgloss.Width(line) > w {
						t.Errorf("art %q at %dx%d line width %d", name, w, h, lipgloss.Width(line))
					}
				}
			}
		}
	}
}
