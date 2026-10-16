package midi

import (
	"os"
	"testing"

	"github.com/maguldon/mix-tui/internal/engine"
)

// The Xone:K2 fixture was reconciled against real hardware on 2026-10-15 by
// capturing a connected unit with cmd/midi/midicap and cross-checking every
// number against the MIDI Implementation Send table in the Xone:K2 User Guide
// (AP8509, pp.14-16).
//
// The tests below pin the facts that reconciliation established. They exist
// because the file previously encoded a plausible-looking generic mixer:
// channel 1 and channel 4, a crossfader, and CC numbers borrowed from a
// two-deck layout. Nothing in the parser can tell that fiction from truth --
// it parses either -- so without a test that names the hardware facts, the
// next edit can quietly reintroduce them.

const xoneK2Path = "../testdata/mappings/xone-k2.yaml"

// k2Channel is the channel a mapping file must name for a K2 in its default
// state. User Guide p.11: "By default the MIDI Channel number is set to 15" --
// but MIDI channel numbers are 1-based in documentation and 0-based in the
// status byte, and a mapping file is compared against the status byte. The
// resolver looks a message up under Message.Channel, filled from
// `status & 0x0F`, so MIDI channel 15 is 14 here.
//
// Writing 15 is the mistake this constant exists to prevent: the file parses,
// compiles, hot-reloads, and matches nothing.
const k2Channel = 14

// k2PortName is what gomidi reports for the connected unit, and therefore the
// string Table.Matches is given. It carries ALSA card and device numbers that
// change between reconnects, which is why the fixture matches on a substring.
const k2PortName = "XONE:K2:XONE:K2 MIDI 1 24:0"

func loadK2(t *testing.T) *DeviceMapping {
	t.Helper()
	data, err := os.ReadFile(xoneK2Path)
	if err != nil {
		t.Fatalf("%s: %v", xoneK2Path, err)
	}
	m, err := ParseMapping(xoneK2Path, data)
	if err != nil {
		t.Fatalf("%s: %v", xoneK2Path, err)
	}
	return m
}

// TestK2DeviceNameMatchesRealPort guards a bug that made the whole file inert
// regardless of its CC numbers: the device was declared "Xone K2", which is
// not a substring of the port name the K2 actually enumerates as, so
// Table.Matches rejected every message. A mapping that never matches looks
// exactly like a controller that is not plugged in.
func TestK2DeviceNameMatchesRealPort(t *testing.T) {
	m := loadK2(t)
	tab, err := m.Compile()
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	if !tab.Matches(k2PortName) {
		t.Errorf("device %q does not match the port name %q; every binding would be dead",
			m.Device, k2PortName)
	}
}

// TestK2EverythingIsOnChannel15 pins the reconciliation's headline finding. The
// fixture used to spread bindings across channels 1 and 4, neither of which
// the K2 sends on.
func TestK2EverythingIsOnChannel15(t *testing.T) {
	m := loadK2(t)
	for _, c := range m.Controls {
		if c.Chan != k2Channel {
			t.Errorf("control %+v is on channel %d, but a K2 in its default state sends on %d only",
				c, c.Chan, k2Channel)
		}
	}
	for name, src := range m.Modifiers {
		if src.Chan != k2Channel {
			t.Errorf("modifier %q is on channel %d, but a K2 in its default state sends on %d only",
				name, src.Chan, k2Channel)
		}
	}
}

// k2CCs and k2Notes are the numbers a K2 in its default state (LATCHING LAYERS
// OFF) actually sends. CC0-3 encoders, CC4-7 pots, CC8-11 and CC12-15 knobs,
// CC16-19 faders, CC20-21 setup encoders. Notes 24-39 are the sixteen-button
// matrix, 40-51 the row switches, 52-55 the encoder push switches, 12 LAYER
// and 3 EXIT SETUP.
func k2Sends(kind MsgKind, num uint8) bool {
	switch kind {
	case KindCC:
		return num <= 21
	case KindNote:
		switch {
		case num == 3, num == 12:
			return true
		case num >= 24 && num <= 55:
			return true
		}
		return false
	default:
		return false
	}
}

// TestK2BindsOnlyNumbersTheSurfaceSends catches the specific failure this
// fixture suffered from: numbers that are real CC numbers on the K2 but belong
// to a latching layer rather than to the control being described. CC49, CC22
// and CC30 all exist on the surface; they are the layer-1 forms of CC27, CC0
// and CC8, so binding them silently drives a different control, or nothing.
func TestK2BindsOnlyNumbersTheSurfaceSends(t *testing.T) {
	m := loadK2(t)
	for _, c := range m.Controls {
		if !k2Sends(c.Kinds, c.Num) {
			t.Errorf("control %s ch%d n%d (%s) is not a number a K2 sends in its default layer",
				kindWord(c.Kinds), c.Chan, c.Num, FormatActionKind(c.Action))
		}
	}
	for name, src := range m.Modifiers {
		if !k2Sends(src.Kind, src.Num) {
			t.Errorf("modifier %q binds %s n%d, which the K2 does not send in its default layer",
				name, kindWord(src.Kind), src.Num)
		}
	}
}

// TestK2ModifierIsTheLayerButton pins the modifier to the button that was
// observed on the wire. The file used to declare a CC 49 "shift", which is not
// a button at all -- it is the second latching layer of a row-2 pot, and with
// layers off nothing ever sends it.
func TestK2ModifierIsTheLayerButton(t *testing.T) {
	m := loadK2(t)
	src, ok := m.Modifiers["layer"]
	if !ok {
		t.Fatalf("no modifier named %q; the file must declare the LAYER button", "layer")
	}
	if src.Kind != KindNote || src.Num != 12 {
		t.Errorf("modifier %q binds %s n%d, want note 12 (the LAYER button)",
			"layer", kindWord(src.Kind), src.Num)
	}
	if src.ModMode != ModMomentary {
		t.Errorf("modifier %q has mode %v, want momentary", "layer", src.ModMode)
	}
}

// TestK2DeclaresNoCrossfaderFader records that the surface has no crossfader.
// Its four faders are CC16-19, one per column, and no crossfader appears
// anywhere in its MIDI table. The previous fixture bound "chan 4 num 9" as a
// crossfader, which was a number the K2 never sends on any channel.
func TestK2DeclaresNoCrossfaderFader(t *testing.T) {
	m := loadK2(t)
	for _, c := range m.Controls {
		if c.Action == engine.MixerCrossfader && c.Kinds == KindCC && c.Num == 9 {
			t.Error("cc 9 is a row-4 knob, not a crossfader; the K2 has no crossfader fader")
		}
	}
}

func kindWord(k MsgKind) string {
	if k == KindNote {
		return "note"
	}
	return "cc"
}
