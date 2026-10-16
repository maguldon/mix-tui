package main

import "testing"

func TestFlagSetParses(t *testing.T) {
	fs, _ := parseFlags([]string{"-a", "a.mp3", "-b", "b.wav", "-play", "0",
		"-play", "1", "-seconds", "10", "-stats"})
	if fs.a != "a.mp3" {
		t.Errorf("a = %q", fs.a)
	}
	if fs.b != "b.wav" {
		t.Errorf("b = %q", fs.b)
	}
	if len(fs.play) != 2 || fs.play[0] != 0 || fs.play[1] != 1 {
		t.Errorf("play = %v, want [0 1]", fs.play)
	}
	if fs.seconds != 10 {
		t.Errorf("seconds = %v", fs.seconds)
	}
	if !fs.stats {
		t.Error("stats not set")
	}
}

func TestFlagRejectsNegativeSeconds(t *testing.T) {
	if err := validate(configFor([]string{"-a", "a.mp3", "-b", "b.mp3", "-seconds", "-5"})); err == nil {
		t.Error("negative -seconds must be rejected")
	}
}

// A single-deck run is legitimate, so -b alone being absent is NOT an error.
// What IS an error is asking to play a deck that has no track, because the
// engine would silently ignore it and the user would hear nothing.
func TestFlagAllowsSingleDeck(t *testing.T) {
	if err := validate(configFor([]string{"-a", "a.mp3", "-seconds", "1"})); err != nil {
		t.Errorf("a single-deck run must be valid: %v", err)
	}
}

func TestFlagRejectsPlayingDeckWithNoTrack(t *testing.T) {
	if err := validate(configFor([]string{"-a", "a.mp3", "-play", "1", "-seconds", "1"})); err == nil {
		t.Error("expected an error when -play 1 has no -b track")
	}
}

// A deck index outside the engine's deck count would be silently ignored at
// run time, which reads as "the track did not play" with no explanation.
func TestFlagRejectsOutOfRangePlay(t *testing.T) {
	cfg := configFor([]string{"-a", "a.mp3", "-b", "b.mp3", "-play", "7", "-seconds", "1"})
	if err := validate(cfg); err == nil {
		t.Error("expected an error for -play 7 on a two-deck engine")
	}
}

// Beat-dependent controls need a beatgrid, and without one they are refused at
// runtime. Saying so up front beats a silent no-op.
func TestFlagRejectsBeatControlWithoutBPM(t *testing.T) {
	cfg := configFor([]string{"-a", "a.mp3", "-b", "b.mp3", "-loop-a", "4", "-seconds", "1"})
	if err := validate(cfg); err == nil {
		t.Error("-loop-a without -bpm-a must be rejected")
	}
	cfg = configFor([]string{"-a", "a.mp3", "-b", "b.mp3", "-sync", "-seconds", "1"})
	if err := validate(cfg); err == nil {
		t.Error("-sync without both -bpm-a and -bpm-b must be rejected")
	}
}

// A crossfader outside [-1,1] is a sign of a bad config file rather than a
// deliberate value, and silently clamping it would hide that.
func TestFlagRejectsOutOfRangeCrossfader(t *testing.T) {
	cfg := configFor([]string{"-a", "a.mp3", "-b", "b.mp3", "-xf", "5", "-seconds", "1"})
	if err := validate(cfg); err == nil {
		t.Error("crossfader beyond [-1,1] must be rejected")
	}
}

func TestFlagRequiresAtLeastOneTrack(t *testing.T) {
	if err := validate(configFor([]string{"-seconds", "1"})); err == nil {
		t.Error("expected an error with no tracks at all")
	}
}

func TestFlagSetDefaults(t *testing.T) {
	fs, _ := parseFlags(nil)
	if fs.seconds != 0 {
		t.Errorf("seconds default = %v; validation owns the default", fs.seconds)
	}
	if fs.stats {
		t.Error("stats should default off")
	}
}

// TestGrantedRateSpellsOutTheUnknown keeps the stats dump honest about the one
// number it cannot know until the daemon says it.
//
// Stats.NegotiatedRate is 0 until PipeWire grants a format, which is exactly the
// window a reader most wants the truth in: whether the device is running at what
// was asked. Printed raw, "rate=0" under a device heading reads as a sound card
// running at 0Hz — a fault that does not exist — rather than as "not known yet".
func TestGrantedRateSpellsOutTheUnknown(t *testing.T) {
	for _, tc := range []struct {
		rate int
		want string
	}{
		{0, "--"},
		{-1, "--"},
		{44100, "44100"},
		{48000, "48000"},
	} {
		if got := grantedRate(tc.rate); got != tc.want {
			t.Errorf("grantedRate(%d) = %q, want %q", tc.rate, got, tc.want)
		}
	}
}
