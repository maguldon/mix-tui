package main

import (
	"errors"
	"strings"
	"testing"
)

// TestMIDIStartupDistinguishesNoInputFromFailure is the regression test for
// issue #10.
//
// midi.OpenListener returns success when the ALSA driver is healthy but zero
// ports are attached -- the port loop does not execute. The caller gated its
// reporting on err != nil, so the most common state of all -- a working driver
// with no controller plugged in -- told the user nothing.
func TestMIDIStartupDistinguishesNoInputFromFailure(t *testing.T) {
	cases := []struct {
		name  string
		err   error
		ports int
		want  midiStartupState
	}{
		{"driver failed", errors.New("cannot open ALSA"), 0, midiFailed},
		{"healthy driver, nothing plugged in", nil, 0, midiNoInputs},
		{"healthy driver, one controller", nil, 1, midiOK},
		{"several controllers", nil, 4, midiOK},
	}
	for _, tc := range cases {
		if got := midiStartup(tc.err, tc.ports); got != tc.want {
			t.Errorf("%s: state = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// TestOpenedPortsToleratesNilListener keeps the error path from panicking when
// OpenListener returns no listener.
func TestOpenedPortsToleratesNilListener(t *testing.T) {
	if got := openedPorts(nil); got != 0 {
		t.Errorf("openedPorts(nil) = %d, want 0", got)
	}
}

// TestNoInputReportNamesTheMappingDirectory checks the message the user
// actually gets. "No MIDI" alone leaves them unable to tell a missing device
// from a missing mapping, which is the distinction spec 8 asks for.
func TestNoInputReportNamesTheMappingDirectory(t *testing.T) {
	lines := strings.Join(midiReportLines(midiNoInputs, nil, "/cfg"), "\n")
	for _, want := range []string{"no MIDI inputs are attached", "mappings are read from", "/cfg/mappings"} {
		if !strings.Contains(lines, want) {
			t.Errorf("the no-inputs report never mentions %q:\n%s", want, lines)
		}
	}
}

// TestFailedReportCarriesTheCause keeps the driver error, which is the only
// actionable part when the driver itself is the problem.
func TestFailedReportCarriesTheCause(t *testing.T) {
	lines := strings.Join(midiReportLines(midiFailed, errors.New("alsa busy"), "/cfg"), "\n")
	if !strings.Contains(lines, "alsa busy") {
		t.Errorf("the failure report dropped the cause:\n%s", lines)
	}
}

// TestOKStateSaysNothing keeps a working setup quiet.
func TestOKStateSaysNothing(t *testing.T) {
	if got := midiReportLines(midiOK, nil, "/cfg"); got != nil {
		t.Errorf("a working MIDI setup produced %v, want no report", got)
	}
}
