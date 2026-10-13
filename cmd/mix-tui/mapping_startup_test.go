package main

import (
	"errors"
	"strings"
	"testing"
)

// TestMappingStartupLinesNamesTheFileAndLine covers the startup half of the
// stderr fix.
//
// Only a count was printed, so a user with a broken mapping was told that
// something was wrong and not what. Spec 8 asks for the file and the line, and
// those are the only part that makes a typo findable.
func TestMappingStartupLinesNamesTheFileAndLine(t *testing.T) {
	lines := mappingStartupLines(0, []error{
		errors.New("xone-k2.yaml:12: unknown action DeckPich"),
		errors.New("k2.yaml:40: device \"Xone\" not found"),
	})
	got := strings.Join(lines, "\n")
	for _, want := range []string{"xone-k2.yaml:12", "DeckPich", "k2.yaml:40"} {
		if !strings.Contains(got, want) {
			t.Errorf("startup report is missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "mapping error(s)") {
		t.Errorf("the report is still only a count:\n%s", got)
	}
}

// TestMappingStartupLinesIsQuietWhenClean keeps a working setup silent.
func TestMappingStartupLinesIsQuietWhenClean(t *testing.T) {
	if got := mappingStartupLines(0, nil); got != nil {
		t.Errorf("a clean startup reported %v", got)
	}
	if got := mappingStartupLines(2, nil); len(got) != 1 || !strings.Contains(got[0], "2 mapping") {
		t.Errorf("a successful load reported %v", got)
	}
}
