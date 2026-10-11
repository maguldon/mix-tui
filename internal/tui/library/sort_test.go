package library

import (
	"testing"
	"time"
)

// TestParseCamelot pins the accepted format, including the range check the doc
// claims but the code did not perform: "100A" parsed as a valid key, so a
// corrupt entry sorted after every real one instead of being undetected.
func TestParseCamelot(t *testing.T) {
	valid := map[string]struct {
		num    int
		letter byte
	}{
		"1A": {1, 'A'}, "1B": {1, 'B'},
		"8A": {8, 'A'}, "9A": {9, 'A'}, "10A": {10, 'A'}, "10B": {10, 'B'},
		"11A": {11, 'A'}, "12B": {12, 'B'},
	}
	for in, want := range valid {
		num, letter, ok := parseCamelot(in)
		if !ok {
			t.Errorf("parseCamelot(%q) rejected a valid key", in)
			continue
		}
		if num != want.num || letter != want.letter {
			t.Errorf("parseCamelot(%q) = (%d, %q), want (%d, %q)",
				in, num, letter, want.num, want.letter)
		}
	}

	invalid := []string{
		"", "A", "1", "0A", "13A", "100A", "1C", "1a", "-1A", "1AA", " A",
	}
	for _, in := range invalid {
		if _, _, ok := parseCamelot(in); ok {
			t.Errorf("parseCamelot(%q) accepted an invalid key", in)
		}
	}
}

// TestCamelLessOrdersAroundTheWheel is the property the wheel sort exists for.
// Lexicographic order puts "10A" before "9A", which is exactly backwards, so a
// key-sorted crate comes out in the wrong order.
func TestCamelLessOrdersAroundTheWheel(t *testing.T) {
	ordered := []string{"1A", "1B", "2A", "2B", "8A", "8B", "9A", "9B", "10A", "10B", "11A", "12B"}

	for i := 0; i < len(ordered)-1; i++ {
		a, b := ordered[i], ordered[i+1]
		if !camelLess(a, b) {
			t.Errorf("camelLess(%q, %q) = false; want true", a, b)
		}
		if camelLess(b, a) {
			t.Errorf("camelLess(%q, %q) = true; want false", b, a)
		}
	}

	// Every key must sort before every undetected one.
	for _, k := range ordered {
		if !camelLess(k, "") {
			t.Errorf("camelLess(%q, undetected) = false; a detected key must come first", k)
		}
		if camelLess("", k) {
			t.Errorf("camelLess(undetected, %q) = true", k)
		}
	}
}

// TestSortByKeyUsesTheWheel checks the sort end to end, since camelLess itself
// being correct does not prove Sort wires it up.
func TestSortByKeyUsesTheWheel(t *testing.T) {
	tr := []Track{
		{Title: "ten", KeyCamelot: "10A"},
		{Title: "nine", KeyCamelot: "9B"},
		{Title: "one", KeyCamelot: "1A"},
		{Title: "none", KeyCamelot: ""},
		{Title: "twelve", KeyCamelot: "12A"},
	}

	got := Sort(tr, SortByKey, false)
	want := []string{"one", "nine", "ten", "twelve", "none"}
	for i, title := range want {
		if got[i].Title != title {
			t.Fatalf("ascending order = %v, want %v", titles(got), want)
		}
	}

	got = Sort(tr, SortByKey, true)
	want = []string{"twelve", "ten", "nine", "one", "none"}
	for i, title := range want {
		if got[i].Title != title {
			t.Fatalf("descending order = %v, want %v", titles(got), want)
		}
	}
}

// TestSortAddedHonoursDirection pins the SortAdded fix. The comparison was
// inverted, so "ascending" put the newest track first and the toggle inverted
// the meaning rather than flipping a defined baseline.
func TestSortAddedHonoursDirection(t *testing.T) {
	old := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	mid := time.Date(2021, 6, 1, 0, 0, 0, 0, time.UTC)
	newest := time.Date(2023, 3, 3, 0, 0, 0, 0, time.UTC)
	tr := []Track{
		{Title: "newest", Added: newest},
		{Title: "oldest", Added: old},
		{Title: "middle", Added: mid},
	}

	asc := Sort(tr, SortAdded, false)
	if asc[0].Title != "oldest" || asc[1].Title != "middle" || asc[2].Title != "newest" {
		t.Errorf("ascending = %v, want oldest, middle, newest", titles(asc))
	}

	desc := Sort(tr, SortAdded, true)
	if desc[0].Title != "newest" || desc[1].Title != "middle" || desc[2].Title != "oldest" {
		t.Errorf("descending = %v, want newest, middle, oldest", titles(desc))
	}
}

// TestSortKeepsUndetectedLast guards the stableUndetected call: reversing the
// slice for a descending sort must not drag undetected entries to the front,
// or every unknown-BPM track leads the list.
func TestSortKeepsUndetectedLast(t *testing.T) {
	tr := []Track{
		{Title: "a", BPM: 0},
		{Title: "b", BPM: 140},
		{Title: "c", BPM: 0},
		{Title: "d", BPM: 120},
		{Title: "e", BPM: 0},
	}

	for _, desc := range []bool{false, true} {
		got := Sort(tr, SortBPM, desc)
		// Undetected entries must all come after every detected one, in both
		// directions. The detected entries among themselves follow the sort
		// direction.
		firstUndetected := -1
		lastDetected := -1
		for i, tk := range got {
			if tk.BPM == 0 {
				if firstUndetected < 0 {
					firstUndetected = i
				}
			} else {
				if firstUndetected >= 0 {
					t.Errorf("desc=%v: detected %q at %d appears after an undetected at %d in %v",
						desc, tk.Title, i, firstUndetected, titles(got))
				}
				lastDetected = i
			}
		}
		if firstUndetected >= 0 && firstUndetected < lastDetected {
			t.Errorf("desc=%v: undetected entries are not all last in %v", desc, titles(got))
		}
	}
}

func titles(tr []Track) []string {
	out := make([]string, len(tr))
	for i, t := range tr {
		out[i] = t.Title
	}
	return out
}
