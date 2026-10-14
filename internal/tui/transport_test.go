package tui

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"strconv"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/maguldon/mix-tui/internal/config"
)

// transportKey maps a key name to the tea.KeyMsg a terminal can actually
// produce for it. The comment on each entry is the byte sequence a terminal
// sends, because that -- not the constant name -- is what decides whether the
// key exists.
//
// It exists because three bindings in this file were matched against strings no
// terminal emits: "shift+enter" (bubbletea has no Shift field, and reports
// "enter" for the same bytes), "backtab" (it is "shift+tab"), and anything a
// test invents. Each one had a test that passed, because the test called
// handleKey with the string directly and never went near the transport.
//
// The table is EXHAUSTIVE over every non-rune key the application binds, and
// TestMatchedNamesAreEmittable is what keeps it that way: that test parses the
// package's own source and fails on any string a switch matches that no entry
// here can produce. A list nobody checks against the code is a list that goes
// stale, which is how "shift+enter" shipped in the first place.
//
// Only keys a real terminal sends belong here. A name this table cannot produce
// is a name the application cannot receive, and asserting on it proves nothing
// about the running program.
//
// Printable characters are not listed: bubbletea delivers them as
// tea.KeyRunes, and any single printable rune round-trips through
// KeyMsg.String() unchanged. transportMsg covers that case, and
// TestTransportTableRoundTrips proves it rather than assuming it.
var transportKey = map[string]tea.KeyMsg{
	"tab":       {Type: tea.KeyTab},                       // \x09
	"shift+tab": {Type: tea.KeyShiftTab},                  // \x1b[Z
	"enter":     {Type: tea.KeyEnter},                     // \r
	"backspace": {Type: tea.KeyBackspace},                 // \x7f
	"esc":       {Type: tea.KeyEscape},                    // \x1b
	"up":        {Type: tea.KeyUp},                        // \x1b[A
	"down":      {Type: tea.KeyDown},                      // \x1b[B
	"left":      {Type: tea.KeyLeft},                      // \x1b[D
	"right":     {Type: tea.KeyRight},                     // \x1b[C
	"ctrl+c":    {Type: tea.KeyCtrlC},                     // \x03
	"ctrl+u":    {Type: tea.KeyCtrlU},                     // \x15
	" ":         {Type: tea.KeySpace, Runes: []rune{' '}}, // \x20
}

// transportNames is transportKey inverted: the names a terminal can deliver.
//
// Note that it holds "esc", not "escape". bubbletea spells the escape key
// "esc"; the application spells it "escape" and translates in bindingName. Both
// spellings are therefore real, and which one a case string has to be depends
// on which side of that translation it sits.
var transportNames = func() map[string]bool {
	m := make(map[string]bool, len(transportKey))
	for _, km := range transportKey {
		m[km.String()] = true
	}
	return m
}()

// typedRune reports whether a name is one printable character, which is how a
// letter, digit or symbol arrives: tea.KeyRunes with that rune, reported back
// unchanged.
func typedRune(name string) bool {
	r := []rune(name)
	return len(r) == 1 && r[0] >= ' ' && r[0] != 0x7f
}

// emittable reports whether a handler can ever see the name n.
//
// Three ways a name can be reachable, and no fourth:
//
//   - a terminal reports it verbatim ("tab", "up", "ctrl+c");
//   - it is a typed character ("c", "[", "?", "Z");
//   - a terminal reports something else and bindingName translates it
//     ("esc" arrives as "escape", " " arrives as "space").
//
// Anything else is a case that cannot fire. That is the whole point: a name
// matched against bytes no terminal sends is a binding that does nothing, and
// until now nothing in the suite could tell the difference.
func emittable(name string) bool {
	if transportNames[name] || typedRune(name) {
		return true
	}
	for sent := range transportNames {
		if bindingName(sent) == name {
			return true
		}
	}
	return false
}

// transportMsg returns the tea.KeyMsg a terminal sends for a key name.
//
// It accepts either spelling for the translated keys, so a caller holding a
// config binding name ("space") and a caller holding the transport name (" ")
// can both use it. A name it cannot produce is a fatal test failure rather than
// a silent pass: that name is a binding with no implementation.
func transportMsg(t *testing.T, name string) tea.KeyMsg {
	t.Helper()
	if km, ok := transportKey[name]; ok {
		if got := km.String(); got != name {
			t.Fatalf("transportKey[%q] reports %q; the table and the handlers disagree", name, got)
		}
		return km
	}
	for sent, km := range transportKey {
		if bindingName(sent) == name {
			return km
		}
	}
	if typedRune(name) {
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(name)}
	}
	t.Fatalf("no tea.KeyMsg reports %q, so no test can prove this binding reachable", name)
	return tea.KeyMsg{}
}

// press sends a key the way the application receives one: as a tea.KeyMsg
// through Update.
//
// Update has a pointer receiver and mutates in place, so the caller reads state
// off m immediately rather than through the returned interface value.
func press(t *testing.T, m *Model, name string) {
	t.Helper()
	m.Update(transportMsg(t, name))
}

// typeText sends each rune of s as its own key press, the way a typed word
// arrives. A terminal delivers one KeyRunes per character, and several bugs
// here came from assuming otherwise.
func typeText(t *testing.T, m *Model, s string) {
	t.Helper()
	for _, r := range s {
		m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
}

// TestTransportTableRoundTrips guards the table against itself: every entry
// must report the name it is filed under. If bubbletea renames a key -- which
// is what would happen if it started supporting a "shift" modifier -- this
// fails instead of quietly turning an entry into a key the handlers never match.
func TestTransportTableRoundTrips(t *testing.T) {
	for name, km := range transportKey {
		if got := km.String(); got != name {
			t.Errorf("transportKey[%q].String() = %q; the table is lying about the transport", name, got)
		}
	}
}

// TestMatchedNamesAreEmittable is the guard that makes the table exhaustive.
//
// It parses this package's own source and takes every string a switch matches
// on -- not a hand-written list of the keys someone remembered. A binding added
// tomorrow lands in this set without anyone having to update a table, so a
// future "shift+enter" or "backtab" fails here, in review, rather than shipping
// with a test that passes because it invented the string.
func TestMatchedNamesAreEmittable(t *testing.T) {
	for name, pos := range matchedNames(t) {
		if emittable(name) {
			continue
		}
		t.Errorf("%s: a switch matches %q, which no terminal can send. "+
			"Add a transportKey entry for the name bubbletea really reports, or "+
			"delete the case: as it stands the binding is unreachable.",
			pos, name)
	}
}

// TestEveryDefaultKeyIsEmittable covers the shipped keymap, which lives in
// another package and so is not in the source the parse above sees. A default
// binding on a name no terminal reports is the same dead key, one layer down.
func TestEveryDefaultKeyIsEmittable(t *testing.T) {
	for name := range config.DefaultKeys() {
		if !emittable(name) {
			t.Errorf("config.DefaultKeys() binds %q, which no terminal can send", name)
		}
	}
}

// TestEmittableIsNotVacuouslyTrue keeps the check above honest. Without this,
// a bug in emittable that returned true for everything would turn the guard
// into a rubber stamp.
func TestEmittableIsNotVacuouslyTrue(t *testing.T) {
	unreachable := []string{
		"shift+enter", // bubbletea has no Shift field; an ordinary terminal sends
		// ENTER's bytes for Shift+Enter, so the handler sees "enter"
		"backtab",     // the sequence is \x1b[Z, which bubbletea reports as shift+tab
		"return",      // the key is CR, which bubbletea reports as enter
		"shift+space", // there is no shift modifier to spell it with
		"super+c",     // and no way to reach one from a terminal this program owns
	}
	for _, name := range unreachable {
		if emittable(name) {
			t.Errorf("emittable(%q) is true; if bubbletea now reports it, the "+
				"handler and its comment should be revisited", name)
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

// matchedNames parses every non-test file in this package and returns every
// string a switch matches on, keyed by the first position it was written at.
//
// Reading the source rather than a list is what makes the check exhaustive. A
// list is only as current as whoever last edited it; the source is the set.
func matchedNames(t *testing.T) map[string]token.Position {
	t.Helper()
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", func(fi fs.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Fatalf("parsing the tui package: %v", err)
	}
	out := map[string]token.Position{}
	for _, pkg := range pkgs {
		for _, f := range pkg.Files {
			ast.Inspect(f, func(n ast.Node) bool {
				cc, ok := n.(*ast.CaseClause)
				if !ok || cc.List == nil {
					return true
				}
				for _, e := range cc.List {
					lit, ok := e.(*ast.BasicLit)
					if !ok || lit.Kind != token.STRING {
						continue
					}
					name, err := strconv.Unquote(lit.Value)
					if err != nil {
						continue
					}
					if _, seen := out[name]; !seen {
						out[name] = fset.Position(lit.Pos())
					}
				}
				return true
			})
		}
	}
	if len(out) == 0 {
		t.Fatal("no case strings found; the parse is looking in the wrong place")
	}
	return out
}
