package tui

import (
	"testing"

	"github.com/maguldon/mix-tui/internal/config"
	"github.com/maguldon/mix-tui/internal/engine"
)

// TestBrowserTypesSpaceInsteadOfPlaying is the regression test for the space
// bar in the browser.
//
// handleKey normalised " " to "space" via bindingName before dispatching to
// the browser, and "space" is five runes, so handleBrowserKey's printable-rune
// test rejected it and the key fell through to the configured binding --
// DeckPlay. Typing "hit the drop" into the filter therefore produced the query
// "hitthedrop" and started the focused deck twice.
//
// Two things broke at once: the user cannot type a space, and the app's
// most-used binding fires an audio action from inside a text field.
func TestBrowserTypesSpaceInsteadOfPlaying(t *testing.T) {
	for _, mode := range []Mode{ModeLibrary, ModeOverlay} {
		m, f := testModel(t)
		m.st.Mode = mode

		// Bubbletea delivers one KeyMsg per rune, including the spaces.
		for _, r := range "hit the drop" {
			m.handleKey(string(r))
		}

		if got, want := m.st.Query, "hit the drop"; got != want {
			t.Errorf("mode %v: query = %q, want %q (spaces are being swallowed)", mode, got, want)
		}
		if len(f.applied) != 0 {
			t.Errorf("mode %v: typing applied %d action(s) %v; the browser must apply none",
				mode, len(f.applied), f.applied)
		}
	}
}

// TestBrowserTypingNeverAppliesAnAction covers every printable character, not
// just the space. A digit, a symbol or a letter that also happens to be bound
// must still reach the query rather than the engine.
func TestBrowserTypingNeverAppliesAnAction(t *testing.T) {
	for _, key := range []string{" ", "a", "z", "0", "9", "-", "/", "?", "`", "l", "s", "v"} {
		m, f := testModel(t)
		m.st.Mode = ModeLibrary

		m.handleKey(key)

		if len(f.applied) != 0 {
			t.Errorf("key %q in the browser applied %v; it must be typed", key, f.applied)
		}
	}
}

// TestBrowserNavigationStillWorks guards the other direction: passing the raw
// key must not break the named navigation keys, which are matched by name and
// are not single runes.
//
// The arrows move the filter's caret and delete nothing, which is the whole
// point: LEFT used to delete the last character of a query the full browser
// never drew, so the user lost something they could not see and had not asked
// to lose.
func TestBrowserNavigationStillWorks(t *testing.T) {
	m, _ := testModel(t)
	m.st.Mode = ModeLibrary
	m.setQuery("abc")

	press(t, m, "left")
	if got, want := m.st.QueryCursor, 2; got != want {
		t.Errorf("left: caret = %d, want %d", got, want)
	}
	if got, want := m.st.Query, "abc"; got != want {
		t.Errorf("left deleted: query = %q, want %q", got, want)
	}

	press(t, m, "left")
	press(t, m, "right")
	if got, want := m.st.QueryCursor, 2; got != want {
		t.Errorf("after left,left,right: caret = %d, want %d", got, want)
	}

	typeText(t, m, "z")
	if got, want := m.st.Query, "abzc"; got != want {
		t.Errorf("typing at the caret: query = %q, want %q", got, want)
	}

	press(t, m, "backspace")
	if got, want := m.st.Query, "abc"; got != want {
		t.Errorf("backspace: query = %q, want %q", got, want)
	}
}

// TestBrowserSortStillToggles guards the one single-rune browser key that is a
// command rather than a character: 's' sorts. It is a mode-relative binding,
// so it must not become a literal 's' in the query.
func TestBrowserSortStillToggles(t *testing.T) {
	m, _ := testModel(t)
	m.st.Mode = ModeLibrary

	m.handleKey("s")

	if !m.st.ShowSort {
		t.Error("'s' must still open the sort menu in the browser")
	}
	if m.st.Query != "" {
		t.Errorf("'s' opened the sort menu but also typed a %q", m.st.Query)
	}
}

// TestMixViewStillBindsSpace pins the other half: outside a browser, space must
// still play the focused deck. The fix must not steal the binding globally.
func TestMixViewStillBindsSpace(t *testing.T) {
	m, f := testModel(t)
	m.st.Mode = ModeMix

	m.handleKey(" ")

	if m.st.Query != "" {
		t.Errorf("space in the mix view must not type; query = %q", m.st.Query)
	}
	if len(f.applied) != 1 || f.applied[0].Kind != engine.DeckPlayPause {
		t.Errorf("space in the mix view applied %v, want one DeckPlayPause", f.applied)
	}
}

var _ = config.Defaults
