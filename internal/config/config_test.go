package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maguldon/mix-tui/internal/engine"
)

func writeCfg(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadMissingFileYieldsDefaults(t *testing.T) {
	cfg, err := Load(filepath.Join(t.TempDir(), "nope.yaml"))
	if err != nil {
		t.Fatalf("missing config must not be an error: %v", err)
	}
	if cfg.Audio.RingFrames == 0 {
		t.Error("defaults not applied")
	}
}

func TestLoadRejectsUnknownField(t *testing.T) {
	p := writeCfg(t, "audio:\n  ring_frames: 44100\n  samle_rate: 48000\n")
	_, err := Load(p)
	if err == nil {
		t.Fatal("unknown field must error")
	}
	if !strings.Contains(err.Error(), "samle_rate") {
		t.Errorf("error should name the bad field: %v", err)
	}
}

func TestLoadErrorCarriesPathAndLine(t *testing.T) {
	p := writeCfg(t, "audio:\n  ring_frames: 44100\n  bogus: 1\n")
	_, err := Load(p)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), p) {
		t.Errorf("error must name the file: %v", err)
	}
	if !strings.Contains(err.Error(), "line") {
		t.Errorf("error must carry a line: %v", err)
	}
}

func TestLoadParsesValues(t *testing.T) {
	p := writeCfg(t, "audio:\n  ring_frames: 44100\n  target_latency_frames: 8820\nui:\n  default_view: D\n")
	cfg, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Audio.RingFrames != 44100 {
		t.Errorf("ringFrames = %d", cfg.Audio.RingFrames)
	}
	if cfg.UI.DefaultView != "D" {
		t.Errorf("view = %q", cfg.UI.DefaultView)
	}
}

func TestLoadRejectsInvalidView(t *testing.T) {
	p := writeCfg(t, "ui:\n  default_view: Z\n")
	if _, err := Load(p); err == nil {
		t.Fatal("invalid view must error")
	}
}

func TestDefaultsAreSane(t *testing.T) {
	d := Defaults()
	if d.UI.DefaultView != "D" {
		t.Errorf("spec says D is the default: %q", d.UI.DefaultView)
	}
	if d.Audio.RingFrames < 4410 {
		t.Errorf("ringFrames %d is too small", d.Audio.RingFrames)
	}
	if d.Audio.TargetLatencyFrames < 4410 {
		t.Errorf("latency target %d too small", d.Audio.TargetLatencyFrames)
	}
}

func TestKeyResolverMapsToAction(t *testing.T) {
	p := writeCfg(t, "keys:\n  space: {action: DeckPlay, target: 0}\n")
	cfg, _ := Load(p)
	r, err := NewKeyResolver(cfg)
	if err != nil {
		t.Fatal(err)
	}
	a, ok := r.Resolve("space")
	if !ok {
		t.Fatal("space not bound")
	}
	if a.Kind != engine.DeckPlay {
		t.Errorf("kind = %v", a.Kind)
	}
	if a.Target != 0 {
		t.Errorf("target = %d", a.Target)
	}
}

func TestKeyResolverRejectsUnknownAction(t *testing.T) {
	p := writeCfg(t, "keys:\n  z: {action: NotAnAction}\n")
	cfg, _ := Load(p)
	if _, err := NewKeyResolver(cfg); err == nil {
		t.Fatal("unknown action in keybinding must error")
	}
}

func TestKeyResolverAllowsTwoKeysSameAction(t *testing.T) {
	// Two different keys bound to the same action is legitimate (a shortcut and
	// its duplicate), not a config bug. A Go map cannot hold a duplicate key
	// name anyway, so "the same key twice" is unrepresentable; what must be
	// rejected is an action that does not compile.
	p := writeCfg(t, "keys:\n  space: {action: DeckPlay, target: 0}\n  p: {action: DeckPlay, target: 0}\n")
	cfg, _ := Load(p)
	r, err := NewKeyResolver(cfg)
	if err != nil {
		t.Fatalf("two keys bound to one action must be allowed: %v", err)
	}
	if _, ok := r.Resolve("p"); !ok {
		t.Error("second key not bound")
	}
}

func TestKeyResolverToggleValue(t *testing.T) {
	p := writeCfg(t, "keys:\n  c: {action: DeckCue, target: 0, value: toggle}\n")
	cfg, _ := Load(p)
	r, _ := NewKeyResolver(cfg)
	a, _ := r.Resolve("c")
	if a.Value != 1 {
		t.Errorf("toggle press = %v, want 1", a.Value)
	}
}

// TestKeyResolverUserBindingOverridesDefault covers the merge rule: a user file
// rebinding a key that already has a default must override it, not collide with
// it. The defaults are merged first precisely so this is allowed.
func TestKeyResolverUserBindingOverridesDefault(t *testing.T) {
	p := writeCfg(t, "keys:\n  space: {action: DeckCue, target: 1}\n")
	cfg, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	r, err := NewKeyResolver(cfg)
	if err != nil {
		t.Fatalf("user rebinding of a default key must not error: %v", err)
	}
	a, ok := r.Resolve("space")
	if !ok {
		t.Fatal("space not bound")
	}
	if a.Kind != engine.DeckCue || a.Target != 1 {
		t.Errorf("override did not take: got %v target %d", a.Kind, a.Target)
	}
}

// TestKeyResolverHasDefaults covers the shipped keymap existing at all: with an
// empty config, space must still play.
func TestKeyResolverHasDefaults(t *testing.T) {
	r, err := NewKeyResolver(Defaults())
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := r.Resolve("space"); !ok {
		t.Error("default keymap does not bind space")
	}
}

// TestKeyResolverBindingsAreSorted keeps the help view stable between runs; a
// help listing that reorders itself between launches is impossible to scan.
func TestKeyResolverBindingsAreSorted(t *testing.T) {
	r, err := NewKeyResolver(Defaults())
	if err != nil {
		t.Fatal(err)
	}
	bs := r.Bindings()
	if len(bs) == 0 {
		t.Fatal("no bindings")
	}
	for i := 1; i < len(bs); i++ {
		if bs[i-1] > bs[i] {
			t.Fatalf("bindings not sorted: %q before %q", bs[i-1], bs[i])
		}
	}
}

// TestKeyResolverToggleAlternates covers the toggle strategy: successive presses
// alternate 1, 0, 1 rather than always reporting 1, which would leave a cue
// permanently on.
func TestKeyResolverToggleAlternates(t *testing.T) {
	p := writeCfg(t, "keys:\n  c: {action: DeckCue, target: 0, value: toggle}\n")
	cfg, _ := Load(p)
	r, _ := NewKeyResolver(cfg)
	want := []float64{1, 0, 1}
	for i, w := range want {
		a, ok := r.Resolve("c")
		if !ok {
			t.Fatal("c not bound")
		}
		if a.Value != w {
			t.Errorf("press %d = %v, want %v", i+1, a.Value, w)
		}
	}
}

// TestKeyResolverUnknownKeyIsNotBound keeps an unbound key from silently
// producing a zero action.
func TestKeyResolverUnknownKeyIsNotBound(t *testing.T) {
	r, _ := NewKeyResolver(Defaults())
	if _, ok := r.Resolve("ctrl+alt+f13"); ok {
		t.Error("unbound key reported as bound")
	}
}

// TestLoadRejectsNegativeAudioValue covers validation of the numeric fields: a
// negative ring depth is nonsense and would produce a broken buffer.
func TestLoadRejectsNegativeAudioValue(t *testing.T) {
	p := writeCfg(t, "audio:\n  ring_frames: -1\n")
	if _, err := Load(p); err == nil {
		t.Fatal("negative ring_frames must error")
	}
}

// TestKeyResolverLiteralValue covers a numeric value binding.
func TestKeyResolverLiteralValue(t *testing.T) {
	p := writeCfg(t, "keys:\n  x: {action: CrossfaderSet, value: \"-0.5\"}\n")
	cfg, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	r, err := NewKeyResolver(cfg)
	if err != nil {
		t.Fatal(err)
	}
	a, ok := r.Resolve("x")
	if !ok {
		t.Fatal("x not bound")
	}
	if a.Value != -0.5 {
		t.Errorf("value = %v, want -0.5", a.Value)
	}
}
