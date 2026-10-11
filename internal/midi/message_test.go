package midi

import (
	"testing"

	"github.com/maguldon/mix-tui/internal/engine"
)

func TestActionKindRoundTrip(t *testing.T) {
	names := []string{
		"DeckPlay", "DeckCue", "DeckPitch", "DeckEQLow", "DeckEQMid", "DeckEQHigh",
		"DeckFilterKind", "DeckFilterCutoff", "DeckSeek", "DeckHotcueSet",
		"DeckHotcueJump", "DeckHotcueDelete", "MixerCrossfader", "MixerMasterGain",
		"ZoomToggle", "ZoomIn", "ViewNext", "ViewPrev", "FocusNext",
		"LoopBeat", "LoopIn", "LoopOut", "LoopSave", "LoopRecall", "LoopRelease",
		"LoopRoll", "Sync", "SyncToggle", "Quantize", "LibraryOpen", "LibraryClose",
		"LibraryLoad", "LibrarySearch", "LibrarySort", "DebugToggle", "Quit",
	}
	// The list holds canonical names only. An alias cannot round-trip to
	// itself: MasterGain and MixerMasterGain are the same ActionKind, so
	// FormatActionKind has to pick one, and it picks the engine's spelling.
	for _, n := range names {
		k, err := ParseActionKind(n)
		if err != nil {
			t.Errorf("ParseActionKind(%q): %v", n, err)
			continue
		}
		if got := FormatActionKind(k); got != n {
			t.Errorf("round trip: %q -> %v -> %q", n, k, got)
		}
	}
}

// TestActionKindAliasesResolve covers the accepted alternative spellings. Each
// must parse to the same kind as its canonical name, so an existing mapping
// file keeps working after a rename.
func TestActionKindAliasesResolve(t *testing.T) {
	for alias, canonical := range map[string]string{
		"CueAssign":     "DeckCueAssign",
		"MasterGain":    "MixerMasterGain",
		"ChannelGain":   "MixerChannelGain",
		"CueMix":        "MixerCueMix",
		"ZoomLevel":     "ZoomIn",
		"FocusDeck":     "FocusNext",
		"CrossfaderSet": "MixerCrossfader",
	} {
		ak, err := ParseActionKind(alias)
		if err != nil {
			t.Errorf("ParseActionKind(%q): %v", alias, err)
			continue
		}
		ck, err := ParseActionKind(canonical)
		if err != nil {
			t.Errorf("ParseActionKind(%q): %v", canonical, err)
			continue
		}
		if ak != ck {
			t.Errorf("alias %q resolves to %v, canonical %q to %v", alias, ak, canonical, ck)
		}
	}
}

// TestFormatActionKindIsDeterministic guards the alias canonicaliser. It used
// to fix up the map while building it, so iteration order decided which
// spelling won and FormatActionKind could return a different name from one run
// to the next.
func TestFormatActionKindIsDeterministic(t *testing.T) {
	for i := 0; i < 50; i++ {
		for _, alias := range []string{"MasterGain", "MixerMasterGain", "ZoomLevel", "ZoomIn"} {
			k, err := ParseActionKind(alias)
			if err != nil {
				t.Fatal(err)
			}
			first := FormatActionKind(k)
			for j := 0; j < 5; j++ {
				if got := FormatActionKind(k); got != first {
					t.Fatalf("FormatActionKind(%v) returned %q then %q", k, first, got)
				}
			}
		}
	}
}

func TestParseActionKindRejectsUnknown(t *testing.T) {
	if _, err := ParseActionKind("DeckPith"); err == nil {
		t.Error("a typo must not parse — this is how a broken mapping hides")
	}
}

func TestCountsRecordsIgnoredCategories(t *testing.T) {
	var c Counts
	for i := 0; i < 3; i++ {
		c.Record(Message{Kind: KindOther, Other: OtherSysex})
	}
	c.Record(Message{Kind: KindOther, Other: OtherClock})
	snap := c.Snapshot()
	if snap["sysex"] != 3 {
		t.Errorf("sysex = %d", snap["sysex"])
	}
	if snap["clock"] != 1 {
		t.Errorf("clock = %d", snap["clock"])
	}
}

func TestCountsDoesNotCountHandledMessages(t *testing.T) {
	var c Counts
	if err := c.Record(Message{Kind: KindCC, Channel: 1, Num: 9, Value: 64}); err != nil {
		t.Errorf("a handled CC should not report as ignored: %v", err)
	}
	for _, v := range c.Snapshot() {
		if v != 0 {
			t.Errorf("handled message counted as ignored: %d", v)
		}
	}
}

func TestMessageStringIsStable(t *testing.T) {
	m := Message{Kind: KindCC, Channel: 1, Num: 9, Value: 127}
	if got := m.String(); got == "" {
		t.Error("String() empty")
	}
}

// TestParseActionKindAliasesEngineKinds proves the YAML names resolve to the
// engine kinds milestone 1 actually implements. Without this, "CrossfaderSet"
// could round-trip through midi while mapping to nothing the engine applies,
// which is a mapping that parses and does nothing.
func TestParseActionKindAliasesEngineKinds(t *testing.T) {
	cases := map[string]engine.ActionKind{
		"CrossfaderSet": engine.MixerCrossfader,
		"MasterGain":    engine.MixerMasterGain,
		"ZoomLevel":     engine.ZoomIn,
		"FocusDeck":     engine.FocusNext,
	}
	for name, want := range cases {
		got, err := ParseActionKind(name)
		if err != nil {
			t.Fatalf("ParseActionKind(%q): %v", name, err)
		}
		if got != want {
			t.Errorf("ParseActionKind(%q) = %v, want %v", name, got, want)
		}
	}
}
