package views

import "strings"

// caretGlyph is the field caret.
//
// A drawn character rather than reverse video: reverse video means embedding an
// ANSI sequence inside a string that the caller is about to wrap in its own
// style, and lipgloss does not re-apply the outer style after an embedded reset,
// so every character typed after the caret would lose the field's colour. One
// narrow block costs nothing, renders in every font with box drawing, and is
// one column wide -- so the field does not change width as the user types.
const caretGlyph = "▏"

// CaretGlyph exposes the caret to the model's tests, which assert on a rendered
// frame and so cannot see an unexported constant in this package.
func CaretGlyph() string { return caretGlyph }

// caretField renders a text field with its caret at rune index at.
//
// The caret on an empty field, or on one it has been walked off the end of, is
// drawn after the last character rather than replacing it. Overwriting the
// character under the caret would hide a character the user is looking at while
// they decide whether to delete it.
//
// at is clamped rather than trusted: a caret that points past the end would
// either panic or drop characters, and the model clamps it on every edit, so
// this is the backstop for the case where it did not.
func caretField(label, s string, at int) string {
	r := []rune(s)
	if at < 0 {
		at = 0
	}
	if at > len(r) {
		at = len(r)
	}
	var b strings.Builder
	b.WriteString(label)
	for i, c := range r {
		if i == at {
			b.WriteString(caretGlyph)
		}
		b.WriteString(string(c))
	}
	if at >= len(r) {
		b.WriteString(caretGlyph)
	}
	return b.String()
}
