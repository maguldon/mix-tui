package tui

import "testing"

// The editing primitives, directly.
//
// The point of a textfield.go is that these are the only places a caret moves,
// and they are all measured in runes. A byte-indexed version passes every test
// written in ASCII and corrupts a Japanese track title.
func TestMoveCaretStopsAtBothEnds(t *testing.T) {
	cases := []struct {
		delta int
		want  int
	}{
		{delta: -1, want: 3},
		{delta: -3, want: 1},
		{delta: -4, want: 0},
		{delta: -99, want: 0},
		{delta: 0, want: 4},
		{delta: 1, want: 4},
		{delta: 99, want: 4},
	}
	for _, c := range cases {
		if got := moveCaret("beat", 4, c.delta); got != c.want {
			t.Errorf("moveCaret(%q, 4, %d) = %d, want %d", "beat", c.delta, got, c.want)
		}
	}
}

func TestDeleteBeforeRemovesTheRuneInFrontOfTheCaret(t *testing.T) {
	s, at := deleteBefore("beat", 4)
	if s != "bea" || at != 3 {
		t.Errorf("deleteBefore(%q, 4) = (%q, %d); want (%q, 3)", "beat", s, at, "bea")
	}
	s, at = deleteBefore("beat", 2)
	if s != "bat" || at != 1 {
		t.Errorf("deleteBefore(%q, 2) = (%q, %d); want (%q, 1)", "beat", s, at, "bat")
	}
}

// TestDeleteBeforeAtTheStartIsANoOp. Backspacing off the front of the field must
// do nothing, not delete the last character instead: the settings field is
// seeded with the configured library root, and a fallback there would let the
// user destroy a path they never typed, one character at a time, and a path
// that no longer exists when they noticed.
func TestDeleteBeforeAtTheStartIsANoOp(t *testing.T) {
	for _, text := range []string{"", "beat", "日"} {
		s, at := deleteBefore(text, 0)
		if s != text || at != 0 {
			t.Errorf("deleteBefore(%q, 0) = %q, %d; want it unchanged", text, s, at)
		}
	}
	// And a caret left past the end by some path that missed the setter.
	s, at := deleteBefore("beat", 400)
	if s != "bea" || at != 3 {
		t.Errorf("deleteBefore(%q, 400) = %q, %d; want the caret clamped first", "beat", s, at)
	}
}

func TestInsertRuneInsertsAtTheCaret(t *testing.T) {
	s, at := insertRune("beat", 2, 'z')
	if s != "bezat" {
		t.Errorf("insertRune = %q, want %q", s, "bezat")
	}
	if at != 3 {
		t.Errorf("caret = %d, want 3 -- it follows what it just inserted", at)
	}
	if s, _ := insertRune("beat", 99, 'z'); s != "beatz" {
		t.Errorf("inserting past the end gave %q, want %q", s, "beatz")
	}
	if s, _ := insertRune("beat", -99, 'z'); s != "zbeat" {
		t.Errorf("inserting before the start gave %q, want %q", s, "zbeat")
	}
}

// TestEditingIsRuneIndexed is the reason this file exists. Backspacing a byte
// out of a multi-byte character leaves invalid UTF-8, which the renderer draws
// as U+FFFD at one column per byte, so a filter matching a Japanese title broke
// the moment the user corrected a typo in it.
func TestEditingIsRuneIndexed(t *testing.T) {
	field := "日本語"
	if len([]rune(field)) == len(field) {
		t.Fatal("the fixture is not multi-byte, so the test proves nothing")
	}
	for want := len([]rune(field)) - 1; want >= 0; want-- {
		var s string
		s, _ = deleteBefore(field, len([]rune(field)))
		field = s
		if got := len([]rune(field)); got != want {
			t.Fatalf("after one backspace the field is %q, %d runes; want %d", field, got, want)
		}
	}
	if field != "" {
		t.Fatalf("the field is %q, want empty", field)
	}

	// The caret moves in runes too: three steps back from the end of a
	// two-rune field lands at the start, not two bytes in.
	if got := moveCaret("日本", 2, -2); got != 0 {
		t.Errorf("moveCaret(%q, 2, -2) = %d, want 0", "日本", got)
	}
	s, _ := insertRune("日本", 1, 'x')
	if s != "日x本" {
		t.Errorf("insertRune(%q, 1, 'x') = %q, want %q", "日本", s, "日x本")
	}
}
