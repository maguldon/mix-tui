package config

import (
	"strings"
	"testing"
)

// Every key the TUI acts on appears in the help.
//
// The README points at the help screen as "the live list", so a key that works
// but is missing from it is a key the user has no way to find. Thirteen were
// missing -- every navigation key and every text-editing key -- because they live
// in the TUI's hardcoded switches and in neither DefaultKeys nor uiKeys, which
// are the only two sources the help is built from.
//
// Listing them here rather than checking a rendered frame keeps the assertion on
// the source of truth: the help is generated from the binding table.
func TestEveryHandledKeyIsInTheHelp(t *testing.T) {
	r, err := NewKeyResolver(&Config{})
	if err != nil {
		t.Fatal(err)
	}
	shown := make(map[string]bool)
	for _, line := range r.DescribeLines() {
		shown[strings.Fields(line)[0]] = true
	}

	// The keys internal/tui/keys.go handles before any config lookup.
	handled := []string{
		"tab", "shift+tab", "q", "ctrl+c", "escape", "?", "`", "/", "l", "S",
		"v", "z", "Z",
		"up", "down", "left", "right",
		"enter", "backspace", "delete", "home", "end", "ctrl+u",
		"ctrl+n", "ctrl+a", "ctrl+d",
	}
	var missing []string
	for _, k := range handled {
		if !shown[k] {
			missing = append(missing, k)
		}
	}
	if len(missing) > 0 {
		t.Errorf("handled but absent from the help: %v", missing)
	}
}
