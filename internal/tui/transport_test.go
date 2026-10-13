package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// transportKey maps a key name to the tea.KeyMsg a terminal can actually
// produce for it.
//
// It exists because three bindings in this file were matched against strings no
// terminal emits: "shift+enter" (bubbletea has no Shift field, and reports
// "enter" for the same bytes), "backtab" (it is "shift+tab"), and anything a
// test invents. Each one had a test that passed, because the test called
// handleKey with the string directly and never went near the transport.
//
// Only keys a real terminal sends belong here. A name this table cannot produce
// is a name the application cannot receive, and asserting on it proves nothing
// about the running program.
var transportKey = map[string]tea.KeyMsg{
	"tab":       {Type: tea.KeyTab},
	"shift+tab": {Type: tea.KeyShiftTab},
	"enter":     {Type: tea.KeyEnter},
	"backspace": {Type: tea.KeyBackspace},
	"esc":       {Type: tea.KeyEscape},
	"up":        {Type: tea.KeyUp},
	"down":      {Type: tea.KeyDown},
	"left":      {Type: tea.KeyLeft},
	"right":     {Type: tea.KeyRight},
	"ctrl+c":    {Type: tea.KeyCtrlC},
	"ctrl+u":    {Type: tea.KeyCtrlU},
}

// press sends a key the way the application receives one: as a tea.KeyMsg
// through Update.
func press(t *testing.T, m *Model, name string) {
	t.Helper()
	km, ok := transportKey[name]
	if !ok {
		// A rune, which is how a letter actually arrives.
		m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(name)})
		return
	}
	m.Update(km)
}

// typeText sends each rune of s as its own key press, the way a typed word
// arrives.
func typeText(t *testing.T, m *Model, s string) {
	t.Helper()
	for _, r := range s {
		m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
}

// TestTransportNamesAreReal guards the table above against itself, and records
// why: a key the transport cannot produce is a key with no implementation, no
// matter how convincing its test is.
func TestTransportNamesAreReal(t *testing.T) {
	// Spot-check the three that were wrong, against bubbletea's own spelling.
	for name, want := range map[string]string{
		"tab":       "tab",
		"shift+tab": "shift+tab",
		"enter":     "enter",
	} {
		if got := transportKey[name].String(); got != want {
			t.Errorf("transportKey[%q].String() = %q, want %q", name, got, want)
		}
	}

	// Names the handlers match on that the transport cannot produce. Each entry
	// is a binding that silently does nothing.
	produced := map[string]bool{}
	for _, km := range transportKey {
		produced[km.String()] = true
	}
	for _, impossible := range []string{"shift+enter", "backtab"} {
		if produced[impossible] {
			t.Errorf("%q is in the transport table; if bubbletea can now send it, "+
				"the handler and its comment should be revisited", impossible)
		}
	}
}

// TestShiftEnterIsNotARealKey is the direct test for the first dead binding.
// bubbletea reports "enter" for the bytes Shift+Enter sends on an ordinary
// terminal, so a case matching "shift+enter" cannot fire.
func TestShiftEnterIsNotARealKey(t *testing.T) {
	km, ok := transportKey["enter"]
	if !ok {
		t.Fatal("no transport key for enter")
	}
	if got := km.String(); got != "enter" {
		t.Fatalf("enter reports as %q", got)
	}
	// There is no Shift field on tea.KeyMsg to make this anything else.
	if got := (tea.KeyMsg{Type: tea.KeyEnter}).String(); got == "shift+enter" {
		t.Fatal("bubbletea now reports shift+enter; the removed binding should come back")
	}
}
