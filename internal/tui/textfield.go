package tui

// Editing for the two text fields in this interface: the library filter and the
// settings path.
//
// Everything here is measured in runes, not bytes. Deleting a byte from a
// multi-byte query leaves invalid UTF-8, which renders as U+FFFD at one column
// per byte, and that is what backspace used to do to a track titled in any
// script that is not Latin.
//
// The caret is an index into the field rather than a byte offset, so it cannot
// land inside a character, and every entry point clamps it. A caret is the one
// piece of state a text field adds that can disagree with the text it points
// into, so the rule here is that it is never set by arithmetic done at the call
// site: it moves through these functions or it does not move.

// clampCaret puts a caret back inside its field.
func clampCaret(s string, at int) int {
	n := len([]rune(s))
	if at < 0 {
		return 0
	}
	if at > n {
		return n
	}
	return at
}

// moveCaret steps a caret by delta runes and stops at either end.
func moveCaret(s string, at, delta int) int {
	return clampCaret(s, at+delta)
}

// insertRune puts r at the caret and leaves the caret after it.
func insertRune(s string, at int, r rune) (string, int) {
	rs := []rune(s)
	at = clampCaret(s, at)
	out := make([]rune, 0, len(rs)+1)
	out = append(out, rs[:at]...)
	out = append(out, r)
	out = append(out, rs[at:]...)
	return string(out), at + 1
}

// deleteBefore removes the rune before the caret.
//
// At the start of the field there is nothing before it, and the field is left
// alone. Backspacing off the front is not "delete the last character instead":
// in the settings screen the field is seeded with the configured library root,
// and backspacing through a path the user did not type would destroy it one
// character at a time.
func deleteBefore(s string, at int) (string, int) {
	rs := []rune(s)
	at = clampCaret(s, at)
	if at == 0 {
		return s, 0
	}
	// The three-index slice gives append a destination that cannot overlap rs,
	// so the copy cannot scribble over the rune it is about to read.
	return string(append(rs[:at-1:at-1], rs[at:]...)), at - 1
}

// deleteAfter removes the rune under the caret, leaving the caret where it is.
//
// It is the other half of backspace. Without it a caret at the start of a field
// could not remove anything at all -- the only way to delete the first character
// of a query was to step right and backspace -- and a field could only ever be
// trimmed from the end.
//
// At the end of the field there is nothing under the caret and the field is left
// alone, which is the mirror of deleteBefore's rule at the start: a forward
// delete that ran off the end would have to destroy the last character instead,
// and in the settings screen that means eating a path the user never typed.
// caretToStart and caretToEnd put the caret at either end of the field.
//
// Without them a caret can only be walked one step at a time, so fixing a typo
// near the end of a long path took as many presses as the field is wide. These
// are new bindings rather than a repair, which is why #14 left them out, but
// every text field in this app holds a path and paths are long.
func caretToStart() int {
	return 0
}

// caretToEnd counts runes, matching the caret's unit everywhere else. Bytes
// would put the caret inside a multi-byte character for any path with an
// accented or non-Latin character in it.
func caretToEnd(s string) int {
	return len([]rune(s))
}

func deleteAfter(s string, at int) (string, int) {
	rs := []rune(s)
	at = clampCaret(s, at)
	if at >= len(rs) {
		return s, at
	}
	// The three-index slice gives append a destination that cannot overlap rs,
	// so the copy cannot scribble over the rune it is about to read.
	return string(append(rs[:at:at], rs[at+1:]...)), at
}
