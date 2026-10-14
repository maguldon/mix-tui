package library

import (
	"os"
	"path/filepath"
	"testing"
)

// writeSineFixture writes a real encoded WAV to path.
//
// The scanner's fixture is real encoded audio rather than something this test
// assembles: a hand-built WAV would test the builder, and the thing that broke
// here was the scanner never asking for a duration at all.
func writeSineFixture(t *testing.T, path string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "testdata", "fixtures", "sine440.wav"))
	if err != nil {
		t.Skipf("fixture missing: run MIXTUI_GEN=1 go test -tags mixtuigen ./internal/testdata/")
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestTheScanRecordsTrackDuration covers the library's TIME column, which was
// permanently --:--.
//
// Track.Duration existed and was rendered in the track list and the detail pane,
// and nothing in the application ever assigned it. audio.Probe read the duration
// out of the header and was called only from its own tests. A DJ choosing between
// two mixes could not tell the six-minute one from the seventy-six-minute one.
func TestTheScanRecordsTrackDuration(t *testing.T) {
	dir := t.TempDir()
	writeSineFixture(t, filepath.Join(dir, "A Track.wav"))

	ix := NewIndex()
	ix.Scan(t.Context(), []string{dir}, nil, nil)

	trs := ix.Tracks()
	if len(trs) != 1 {
		t.Fatalf("scan found %d tracks, want 1", len(trs))
	}
	if trs[0].Duration <= 0 {
		t.Errorf("scanned track duration = %v, want the length from the file header", trs[0].Duration)
	}
}

// TestAFileWhoseHeaderCannotBeReadStillAppears: a probe failure must drop the
// duration, never the track. A DJ would rather see a file with --:-- than not
// see it at all, and the scanner already indexed these files before probing was
// added.
func TestAFileWhoseHeaderCannotBeReadStillAppears(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "Broken.wav"), []byte("not audio at all"), 0o644); err != nil {
		t.Fatal(err)
	}

	ix := NewIndex()
	ix.Scan(t.Context(), []string{dir}, nil, nil)

	trs := ix.Tracks()
	if len(trs) != 1 {
		t.Fatalf("scan found %d tracks, want 1: an unprobeable file was dropped", len(trs))
	}
	if trs[0].Duration != 0 {
		t.Errorf("an unreadable header gave duration %v, want 0", trs[0].Duration)
	}
}
