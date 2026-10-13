package tui

import "testing"

// TestShowErrorSurfacesOnScreen covers the interface half of the fix for the
// last finding in issue #10.
//
// The mapping watcher runs for the whole session and reported failures to
// stderr, which is the alt-screen frame, so a reload error corrupted the
// rendered interface instead of telling the user anything. The log file kept
// the detail, but a user who broke their mapping mid-set never learned why
// their controller had stopped responding.
//
// The binary now calls ShowError; these assertions pin what it does with it.
func TestShowErrorSurfacesOnScreen(t *testing.T) {
	m, _ := testModel(t)
	m.ShowError("mapping: xone-k2.yaml:12: unknown action DeckPich")

	if m.st.LastErr != "mapping: xone-k2.yaml:12: unknown action DeckPich" {
		t.Fatalf("LastErr = %q", m.st.LastErr)
	}
	frame := m.RenderAt(120, 24)
	for _, want := range []string{"xone-k2.yaml", "DeckPich"} {
		if !contains(frame, want) {
			t.Errorf("the message is not on screen: %q missing", want)
		}
	}
}

// TestShowErrorExpires keeps a stale mapping error from sitting on screen after
// the user fixes the file, which is the other half of why the error line is
// time-limited rather than sticky.
func TestShowErrorExpires(t *testing.T) {
	m, _ := testModel(t)
	m.ShowError("mapping: broken")
	if m.st.LastErr == "" {
		t.Fatal("ShowError did not record anything")
	}
	m.st.LastErrAt = m.st.LastErrAt.Add(-2 * errTTL)
	m.expireErr()
	if m.st.LastErr != "" {
		t.Errorf("a stale mapping error is still showing: %q", m.st.LastErr)
	}
}

func contains(hay, needle string) bool {
	return len(needle) == 0 || len(hay) >= len(needle) &&
		(hay == needle || indexOf(hay, needle) >= 0)
}

func indexOf(hay, needle string) int {
	for i := 0; i+len(needle) <= len(hay); i++ {
		if hay[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}
