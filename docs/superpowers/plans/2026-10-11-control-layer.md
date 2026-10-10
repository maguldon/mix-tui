# mix-tui Control Layer Implementation Plan (Milestone 3)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Turn raw MIDI messages and YAML config into `engine.Action` values — device mappings, modifiers, encoders, keybindings — with no audio device and no terminal involved, so the whole input path is testable on a machine with no controller plugged in.

**Architecture:** Every piece of input logic is a **pure function from an input event to zero or more Actions**, sitting above a thin hardware layer. Nothing that decides anything imports `gitlab.com/gomidi`. That is what makes this plan's tests runnable in CI: the resolver, the value modes, the modifier state machine, and the encoder accumulator are all exercisable from tables of literal messages, with no device, no daemon, and no display.

**Tech Stack:** Go 1.27, `gitlab.com/gomidi/midi/v2` v2.3.24 + `drivers/rtmididrv` (cgo, ALSA), `gopkg.in/yaml.v3` v3.0.1, `github.com/dukedelaet/mix-tui/internal/engine` from milestone 1

**Spec:** `docs/superpowers/specs/2026-10-10-mix-tui-design.md` — read §5 (the whole action layer), §7 (MIDI scope), §8, §10 alongside this plan.
**Prior plans:** `2026-10-10-audio-core.md` (Action/Snapshot), `2026-10-11-device-output.md` (context for this milestone).

## Scope

This plan adds **control**, not presentation: MIDI in, config in, keys out as Actions. It
adds no terminal rendering (plan 4). The deliverable is that a mapping file plus a
synthetic MIDI message produces the correct Actions — provable without hardware.

Two things land here because they are input, not output:

- **Keybindings** (spec §5.1) — a key table is the same shape as a MIDI table, and sharing
  the resolver avoids two binding systems that drift.
- **Config loading** (spec §10) — mappings, keybindings, and stream targets.

## Global Constraints

- **`internal/midi` must build and its tests must pass with no MIDI device present.**
  Every test in this plan runs on a laptop with nothing plugged in. If a test needs
  hardware, it is behind `//go:build hwtest` and skipped by default.
- **No logic below `internal/midi`'s device layer may import `gitlab.com/gomidi`.** The
  resolver, value modes, modifier state machine, and encoder accumulator operate on an
  internal `Message` type. This is the constraint that makes the plan testable; enforce
  it with:
  `go list -deps ./internal/midi/... | grep gomidi`
  → must print nothing for the resolver package.
- **Mappings hot-reload atomically.** A parse failure keeps the last valid table and logs
  with file and line. A typo must never kill a live performance (spec §8).
- **Unknown YAML fields are errors, not warnings.** The stated purpose of these files is
  user-editable; a silently ignored `mods:` typo produces a controller that mysteriously
  does not work. `yaml.Decoder.KnownFields(true)` everywhere.
- **Every `Action.Value` is clamped through `dsp.ClampNormalized`** before use, whatever
  the source. A MIDI knob sending an out-of-range value must not drive a deck past its
  designed limits.
- **Ignored MIDI categories are counted, not dropped silently** (spec §7): sysex, program
  change, pitch bend, aftertouch, and clock/transport each get a counter.
- **No new dependency beyond `gomidi` and `yaml.v3`.** File watching uses polling on
  mtime+size, not `fsnotify` — the config directory is tiny and polled every 2s, so a
  watcher dependency buys nothing.

## Review Focus

Five ways this layer fails in ways that are invisible to tests but obvious to a DJ
holding a controller.

1. **A jog wheel jumps wildly on every detent.** Relative encoders report ±1/±2 with a
   null point at 64. Treating those as absolute positions means one detent slams a fader
   full-range. → Task 4, Step 1.
2. **`SHIFT+hotcue-1` works but bare `hotcue-1` stops working** — or vice versa, once a
   chord is mapped. The most-specific-wins fallback must try every subset, not just the
   held set then empty. → Task 3, Step 1.
3. **A note-off for a modifier clears a modifier that is already engaged by a latching
   toggle.** Modifier state must follow its declared mode, not the wire. → Task 4.
4. **Editing a mapping file mid-set stops MIDI entirely.** The swap must be atomic and a
   parse failure must keep the previous table. → Task 5, Step 1.
5. **The same physical button behaves differently after a reconnect.** Controllers rename
   themselves; matching by exact name means the mapping silently stops applying. → Task 6.

---

## File Structure

```
internal/midi/
  message.go     internal Message type — no gomidi types cross this line
  resolver.go    (mods, msg) -> Binding, most-specific-wins
  modifiers.go   modifier set + press/release state machine
  encoder.go     absolute / relative / null-point accumulation
  mapping.go     mapping file schema -> compiled table
  hotreload.go   mtime polling + atomic table swap
  device_linux.go   gomidi device layer (the ONLY file importing gomidi)
  *_test.go
internal/config/
  config.go      config.yaml, keybindings, stream targets, defaults
  load.go        strict YAML loading with line-numbered errors
  *_test.go
```

`message.go`, `resolver.go`, `modifiers.go`, `encoder.go`, and `mapping.go` are pure Go
with no hardware dependency. `device_linux.go` is a thin adapter.

---

### Task 1: Internal Message type and the full ActionKind set

**Files:**
- Create: `internal/midi/message.go`
- Create: `internal/midi/message_test.go`

**Interfaces:**
- Consumes: `engine.Action`, `engine.ActionKind` from milestone 1.
- Produces:
  ```go
  package midi

  type MsgKind int
  const (
      KindCC MsgKind = iota
      KindNote
      KindOther   // everything we ignore but still count
  )

  type OtherCategory int
  const (
      OtherSysex OtherCategory = iota
      OtherProgramChange
      OtherPitchBend
      OtherAftertouch
      OtherClock
      OtherTransport
      NumOtherCategories
  )

  // Message is the hardware-independent input event. No gomidi type crosses
  // into any package that makes decisions.
  type Message struct {
      Kind    MsgKind
      Channel uint8   // 0-15
      Num     uint8   // CC number or note number
      Value   uint8   // 0-127
      Velocity uint8  // notes only
      Other   OtherCategory
  }

  func (m Message) String() string

  // Counts accumulates ignored-message tallies for the debug overlay.
  type Counts struct { ignored [NumOtherCategories]uint64; unmapped uint64 }
  func (c *Counts) Record(m Message) error   // returns a non-nil error if the message was ignored
  func (c Counts) Snapshot() map[string]uint64

  // ActionKindFor maps a config string to an ActionKind, covering the complete
  // set the spec names. This is the single source of truth for names in YAML.
  func ParseActionKind(s string) (engine.ActionKind, error)
  func FormatActionKind(k engine.ActionKind) string
  ```

**ActionKind must be completed here.** Milestone 1 defined a subset. Add the rest of
spec §5.5 and the controls named across the spec: `DeckEQLow`, `DeckEQMid`, `DeckEQHigh`,
`DeckFilterKind`, `DeckFilterCutoff`, `DeckCueToggle`, `CrossfaderSet`, `MasterGain`,
`ZoomToggle`, `ZoomLevel`, `ViewNext`, `ViewPrev`, `FocusDeck`, `LoopBeat`, `LoopIn`,
`LoopOut`, `LoopSave`, `LoopRecall`, `LoopRelease`, `LoopRoll`, `Sync`, `SyncToggle`,
`Quantize`, `LibraryOpen`, `LibraryClose`, `LibraryLoad`, `LibrarySearch`, `LibrarySort`,
`DebugToggle`, `Quit`.

Every one needs a `String()` that matches its YAML spelling exactly. **The round trip
`FormatActionKind(ParseActionKind(s)) == s` is a test**, because a mismatch here produces
a config file that parses but writes the wrong file on save.

- [ ] **Step 1: Write the failing tests**

Create `internal/midi/message_test.go`:

```go
package midi

import (
    "testing"

    "github.com/dukedelaet/mix-tui/internal/engine"
)

func TestActionKindRoundTrip(t *testing.T) {
    names := []string{
        "DeckPlay", "DeckCue", "DeckPitch", "DeckEQLow", "DeckEQMid", "DeckEQHigh",
        "DeckFilterKind", "DeckFilterCutoff", "DeckSeek", "DeckHotcueSet",
        "DeckHotcueJump", "DeckHotcueDelete", "CrossfaderSet", "MasterGain",
        "ZoomToggle", "ZoomLevel", "ViewNext", "ViewPrev", "FocusDeck",
        "LoopBeat", "LoopIn", "LoopOut", "LoopSave", "LoopRecall", "LoopRelease",
        "LoopRoll", "Sync", "SyncToggle", "Quantize", "LibraryOpen", "LibraryClose",
        "LibraryLoad", "LibrarySearch", "LibrarySort", "DebugToggle", "Quit",
    }
    for _, n := range names {
        k, err := ParseActionKind(n)
        if err != nil { t.Errorf("ParseActionKind(%q): %v", n, err); continue }
        if got := FormatActionKind(k); got != n {
            t.Errorf("round trip: %q -> %v -> %q", n, k, got)
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
    if snap["sysex"] != 3 { t.Errorf("sysex = %d", snap["sysex"]) }
    if snap["clock"] != 1 { t.Errorf("clock = %d", snap["clock"]) }
}

func TestCountsDoesNotCountHandledMessages(t *testing.T) {
    var c Counts
    if err := c.Record(Message{Kind: KindCC, Channel: 1, Num: 9, Value: 64}); err != nil {
        t.Errorf("a handled CC should not report as ignored: %v", err)
    }
    for _, v := range c.Snapshot() {
        if v != 0 { t.Errorf("handled message counted as ignored: %d", v) }
    }
}

func TestMessageStringIsStable(t *testing.T) {
    m := Message{Kind: KindCC, Channel: 1, Num: 9, Value: 127}
    if got := m.String(); got == "" { t.Error("String() empty") }
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/midi/ -v`
Expected: FAIL — `undefined: ParseActionKind`.

- [ ] **Step 3: Extend `engine.ActionKind`**

Add every constant from the list above to `internal/engine/action.go`, and implement
`func (k ActionKind) String() string` over the same table. Keep one table:

```go
var actionNames = map[ActionKind]string{ /* ... */ }
var actionKinds = map[string]ActionKind{}  // built in init() by inverting
```

Inverting one map rather than maintaining two is what makes the round-trip test pass by
construction rather than by discipline.

- [ ] **Step 4: Implement `internal/midi/message.go`**

`Message`, `MsgKind`, `OtherCategory`, `Counts`, and the two parse/format wrappers that
delegate to `engine`'s table.

`Counts.Record` returns a non-nil error when the message was ignored, so a caller can log
it once per category rather than once per message.

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/midi/ -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/engine/ internal/midi/
git commit -m "feat(midi): internal message type and complete ActionKind set"
```

---

### Task 2: Mapping file schema and strict loading

**Files:**
- Create: `internal/midi/mapping.go`
- Create: `internal/midi/mapping_test.go`
- Create: `internal/testdata/mappings/xone-k2.yaml`
- Create: `internal/testdata/mappings/generic-2deck.yaml`

**Interfaces:**
- Consumes: `Message`, `ParseActionKind` from Task 1.
- Produces:
  ```go
  package midi

  type ValueMode int
  const (
      ModeAbsolute ValueMode = iota
      ModeRelative
      ModeMomentary
      ModeToggle
  )

  type ModSource struct {
      Kind      MsgKind
      Chan      uint8
      Num       uint8
      Pressed   uint8   // cc: value meaning down
      Released  uint8   // cc: value meaning up
      ModMode   ModMode // Momentary | Latching | Toggle
  }

  type Control struct {
      Kinds    MsgKind
      Chan     uint8
      Num      uint8
      Mods     []string
      Action   engine.ActionKind
      Target   int
      Slot     int
      Mode     ValueMode
      Momentary bool   // shorthand: Mode = ModeMomentary
  }

  type DeviceMapping struct {
      Device string
      Match  MatchKind   // Substring | Exact
      Modifiers map[string]ModSource
      Controls []Control
      // Raw is retained so a save round-trips without losing unknown fields.
      Raw yaml.Node
  }

  type MatchKind int
  const (
      MatchSubstring MatchKind = iota
      MatchExact
  )

  // ParseMapping parses one mapping file. Errors carry file and line.
  func ParseMapping(path string, data []byte) (*DeviceMapping, error)

  // Compile turns a parsed mapping into a lookup table.
  func (m *DeviceMapping) Compile() (*Table, error)

  // Table is the hot-path structure. It is immutable once built, so swapping it
  // is a single pointer store (see hotreload).
  type Table struct {
      Device string
      Match  MatchKind
      mods   map[string]ModSource
      // byMods is keyed by canonical modifier set, then (kind, chan, num).
      byMods map[string]map[msgKey]Control
  }

  func (t *Table) Lookup(mods ModSet, msg Message) (Control, bool)
  ```

The YAML shape is fixed by spec §5.3 and must match it exactly:

```yaml
# ~/.config/mix-tui/mappings/xone-k2.yaml
device: "Xone K2"
match: substring

modifiers:
  shift:
    kind: cc          # or: note
    chan: 1
    num: 49
    pressed: 127
    released: 0
    mode: momentary   # momentary | latching | toggle

controls:
  - midi: {kind: cc, chan: 1, num: 9}
    action: {kind: DeckPitch, target: 0}
  - midi: {kind: cc, chan: 1, num: 20}
    action: {kind: CueAssign, target: 0}
    momentary: true
  - midi: {kind: note, chan: 1, num: 40}
    mods: [shift]
    action: {kind: DeckHotcueDelete, target: 0}
    slot: 1
```

- [ ] **Step 1: Write the failing tests**

Create `internal/midi/mapping_test.go`:

```go
package midi

import (
    "os"
    "strings"
    "testing"
)

func writeTmp(t *testing.T, body string) string {
    t.Helper()
    p := t.TempDir() + "/m.yaml"
    if err := os.WriteFile(p, []byte(body), 0o644); err != nil { t.Fatal(err) }
    return p
}

const goodMapping = `
device: "Test Controller"
match: substring
modifiers:
  shift:
    kind: cc
    chan: 1
    num: 49
    pressed: 127
    released: 0
    mode: momentary
controls:
  - midi: {kind: cc, chan: 1, num: 9}
    action: {kind: DeckPitch, target: 0}
  - midi: {kind: cc, chan: 1, num: 20}
    action: {kind: CueAssign, target: 0}
    momentary: true
  - midi: {kind: note, chan: 1, num: 40}
    mods: [shift]
    action: {kind: DeckHotcueDelete, target: 0}
    slot: 1
`

func TestParseGoodMapping(t *testing.T) {
    m, err := ParseMapping("x.yaml", []byte(goodMapping))
    if err != nil { t.Fatal(err) }
    if m.Device != "Test Controller" { t.Errorf("device = %q", m.Device) }
    if len(m.Controls) != 3 { t.Fatalf("controls = %d", len(m.Controls)) }
    if _, ok := m.Modifiers["shift"]; !ok { t.Error("shift modifier missing") }
    if m.Controls[2].Slot != 1 { t.Errorf("slot = %d", m.Controls[2].Slot) }
}

func TestParseRejectsUnknownField(t *testing.T) {
    // A typo must fail loudly rather than silently disable the control.
    bad := strings.Replace(goodMapping,
        "    action: {kind: DeckPitch, target: 0}",
        "    actoin: {kind: DeckPitch, target: 0}", 1)
    _, err := ParseMapping("x.yaml", []byte(bad))
    if err == nil { t.Fatal("unknown field must be an error") }
    if !strings.Contains(err.Error(), "x.yaml") {
        t.Errorf("error must name the file: %v", err)
    }
}

func TestParseErrorCarriesLineNumber(t *testing.T) {
    bad := "device: \"X\"\ncontrols:\n  - midi: {kind: cc, chan: 1, num: 9}\n    action: {kind: NotARealAction, target: 0}\n"
    _, err := ParseMapping("x.yaml", []byte(bad))
    if err == nil { t.Fatal("expected error") }
    if !strings.Contains(err.Error(), "line") {
        t.Errorf("error must carry a line number: %v", err)
    }
}

func TestParseRejectsUnknownModifierReference(t *testing.T) {
    bad := strings.Replace(goodMapping, "    mods: [shift]", "    mods: [shfit]", 1)
    _, err := ParseMapping("x.yaml", []byte(bad))
    if err == nil { t.Fatal("a control referencing an undeclared modifier must error") }
}

func TestMomentaryShorthandSetsMode(t *testing.T) {
    m, _ := ParseMapping("x.yaml", []byte(goodMapping))
    m2, _ := m.Compile()
    c, ok := m2.Lookup(ModSet{}, Message{Kind: KindCC, Chan: 1, Num: 20, Value: 127})
    if !ok { t.Fatal("cue control not found") }
    if c.Mode != ModeMomentary {
        t.Errorf("momentary: true did not set ModeMomentary, got %v", c.Mode)
    }
}

func TestDeviceMatchingSubstring(t *testing.T) {
    // Review Focus #5: controllers rename themselves across reconnects.
    m, _ := ParseMapping("x.yaml", []byte(goodMapping))
    tab, _ := m.Compile()
    if !tab.Matches("Xone K2 96") { t.Error("substring match failed") }
    if tab.Matches("Unrelated Pad") { t.Error("substring match too loose") }
}

func TestDeviceMatchingExact(t *testing.T) {
    y := strings.Replace(goodMapping, "match: substring", "match: exact", 1)
    m, _ := ParseMapping("x.yaml", []byte(y))
    tab, _ := m.Compile()
    if tab.Matches("Xone K2 96") { t.Error("exact match must not match a longer name") }
    if !tab.Matches("Test Controller") { t.Error("exact match failed on the exact name") }
}

func TestShippedMappingsParse(t *testing.T) {
    for _, p := range []string{
        "../testdata/mappings/xone-k2.yaml",
        "../testdata/mappings/generic-2deck.yaml",
    } {
        data, err := os.ReadFile(p)
        if err != nil { t.Fatalf("%s: %v", p, err) }
        if _, err := ParseMapping(p, data); err != nil {
            t.Errorf("shipped mapping %s does not parse: %v", p, err)
        }
    }
}
```

Add the two fixture files with realistic content. `xone-k2.yaml` should map the K2's
actual CC layout: pitch faders (CC 9, 10), EQ knobs (CC 20, 21, 22 per channel), filter
(CC 30), crossfader (CC 9 ch 4 or whichever the K2 uses — use plausible values and note in
a comment that these are the values to verify against the hardware), hotcue buttons as
notes with shift-deleted, and a `shift` modifier on a CC.

`generic-2deck.yaml` maps a plausible generic two-channel controller using the
`modifiers` block to exercise latching and toggle modes.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/midi/ -run 'Parse|Momentary|Device' -v`
Expected: FAIL — `undefined: ParseMapping`.

- [ ] **Step 3: Implement `internal/midi/mapping.go`**

```go
type yamlMapping struct {
    Device    string                `yaml:"device"`
    Match     string                `yaml:"match"`
    Modifiers map[string]yamlMod    `yaml:"modifiers"`
    Controls  []yamlControl         `yaml:"controls"`
    Raw       yaml.Node             `yaml:",inline"`
}
type yamlControl struct {
    MIDI      yamlMIDI       `yaml:"midi"`
    Mods      []string       `yaml:"mods"`
    Action    yamlAction     `yaml:"action"`
    Slot      int            `yaml:"slot"`
    Mode      string         `yaml:"mode"`
    Momentary bool           `yaml:"momentary"`
}
type yamlAction struct {
    Kind   string `yaml:"kind"`
    Target int    `yaml:"target"`
}
```

Decode with `yaml.NewDecoder(bytes.NewReader(data)); dec.KnownFields(true)`. On any
error, wrap as `fmt.Errorf("%s: %w", path, err)` — `yaml.v3`'s errors already contain
`line N:`, which satisfies the line-number requirement without extra work.

Validate after decode: every name in `Mods` must exist in `Modifiers`; every
`Action.Kind` must parse; `Momentary: true` overrides `Mode`. **Reject duplicate
`(mods, kind, chan, num)` bindings at compile time** — the second one would be dead code
and is almost always a mistake.

`Compile` canonicalises each modifier set into a sorted, comma-joined key
(`"shift"`, `"ctrl,shift"`, `""`) and builds `map[string]map[msgKey]Control`.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/midi/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/midi/ internal/testdata/
git commit -m "feat(midi): strict mapping file schema with device matching"
```

---

### Task 3: Most-specific-wins resolution

**Files:**
- Create: `internal/midi/resolver.go`
- Create: `internal/midi/resolver_test.go`

**Interfaces:**
- Consumes: `Table`, `ModSet` from Task 2, `Message` from Task 1.
- Produces:
  ```go
  package midi

  // ModSet is a set of held modifier names.
  type ModSet map[string]bool
  func (m ModSet) With(name string) ModSet    // returns a COPY
  func (m ModSet) Without(name string) ModSet // returns a COPY
  func (m ModSet) Empty() bool
  func (m ModSet) Canonical() string          // sorted, comma-joined

  // Resolve finds the binding for a message given the currently held modifiers,
  // trying modifier sets from most specific to least (spec §5.3).
  func (t *Table) Resolve(mods ModSet, msg Message) (Control, bool)
  ```

- [ ] **Step 1: Write the failing tests**

Create `internal/midi/resolver_test.go`:

```go
package midi

import "testing"

func tableFrom(t *testing.T, y string) *Table {
    t.Helper()
    m, err := ParseMapping("x.yaml", []byte(y))
    if err != nil { t.Fatal(err) }
    tab, err := m.Compile()
    if err != nil { t.Fatal(err) }
    return tab
}

const chordMapping = `
device: "T"
controls:
  - midi: {kind: cc, chan: 1, num: 40}
    action: {kind: DeckHotcueSet, target: 0}
    slot: 1
  - midi: {kind: cc, chan: 1, num: 40}
    mods: [shift]
    action: {kind: DeckHotcueDelete, target: 0}
    slot: 1
  - midi: {kind: cc, chan: 1, num: 40}
    mods: [shift, ctrl]
    action: {kind: DeckHotcueJump, target: 0}
    slot: 1
`

func TestResolveNoModifiers(t *testing.T) {
    tab := tableFrom(t, chordMapping)
    c, ok := tab.Resolve(ModSet{}, Message{Kind: KindCC, Chan: 1, Num: 40})
    if !ok { t.Fatal("unmodified not bound") }
    if c.Action != engine_DeckHotcueSet { t.Errorf("got %v", c.Action) }
}

func TestResolveShiftOverridesBase(t *testing.T) {
    // Review Focus #2: one physical button, three behaviours
    tab := tableFrom(t, chordMapping)
    c, _ := tab.Resolve(ModSet{"shift": true}, Message{Kind: KindCC, Chan: 1, Num: 40})
    if c.Action != engine_DeckHotcueDelete {
        t.Errorf("shift gave %v, want DeckHotcueDelete", c.Action)
    }
}

func TestResolveShiftCtrlBeatsShift(t *testing.T) {
    tab := tableFrom(t, chordMapping)
    c, _ := tab.Resolve(ModSet{"shift": true, "ctrl": true},
                       Message{Kind: KindCC, Chan: 1, Num: 40})
    if c.Action != engine_DeckHotcueJump {
        t.Errorf("shift+ctrl gave %v, want DeckHotcueJump", c.Action)
    }
}

func TestResolveCtrlAloneFallsBackToBase(t *testing.T) {
    // ctrl is held but not bound: must fall through shift to empty, not fail
    tab := tableFrom(t, chordMapping)
    c, ok := tab.Resolve(ModSet{"ctrl": true}, Message{Kind: KindCC, Chan: 1, Num: 40})
    if !ok { t.Fatal("ctrl-only should fall back to the base binding") }
    if c.Action != engine_DeckHotcueSet {
        t.Errorf("ctrl-only gave %v, want the base binding", c.Action)
    }
}

func TestResolveUnboundModifierSetFallsBack(t *testing.T) {
    tab := tableFrom(t, chordMapping)
    _, ok := tab.Resolve(ModSet{"alt": true}, Message{Kind: KindCC, Chan: 1, Num: 40})
    if !ok { t.Fatal("unknown modifiers must still reach the base binding") }
}

func TestResolveUnmappedMessageReportsFalse(t *testing.T) {
    tab := tableFrom(t, chordMapping)
    if _, ok := tab.Resolve(ModSet{}, Message{Kind: KindCC, Chan: 1, Num: 99}); ok {
        t.Error("an unbound CC must not resolve")
    }
}

func TestResolveIsIndependentOfHeldModOrder(t *testing.T) {
    tab := tableFrom(t, chordMapping)
    a, _ := tab.Resolve(ModSet{"ctrl": true, "shift": true}, Message{Kind: KindCC, Chan: 1, Num: 40})
    b, _ := tab.Resolve(ModSet{"shift": true, "ctrl": true}, Message{Kind: KindCC, Chan: 1, Num: 40})
    if a.Action != b.Action { t.Error("resolution depends on map iteration order") }
}

func TestModSetCopiesAreIndependent(t *testing.T) {
    base := ModSet{"shift": true}
    other := base.With("ctrl")
    if base["ctrl"] { t.Error("With mutated the receiver") }
    if !other["shift"] || !other["ctrl"] { t.Error("With did not copy") }
}
```

Use the real `engine.DeckHotcueSet` etc. constants in the assertions — the `engine_X`
placeholders above are illustrative; write the real names.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/midi/ -run Resolve -v`
Expected: FAIL — `undefined: Resolve`, `undefined: ModSet`.

- [ ] **Step 3: Implement `internal/midi/resolver.go`**

```go
// Resolve tries every subset of the held modifiers from most specific to least,
// then the empty set. Subsets are enumerated in descending size order with a
// deterministic tie-break (canonical sorted string), so resolution never
// depends on map iteration order.
func (t *Table) Resolve(mods ModSet, msg Message) (Control, bool) {
    names := mods.Names()            // sorted slice
    for size := len(names); size >= 0; size-- {
        for _, subset := range combinations(names, size) {
            key := canonical(subset)
            byMsg, ok := t.byMods[key]
            if !ok { continue }
            if c, ok := byMsg[msgKeyOf(msg)]; ok {
                return c, true
            }
        }
    }
    return Control{}, false
}
```

For a handful of modifiers, enumerating subsets is trivially cheap. Cap the held-modifier
count at, say, 8 in the config loader — beyond that the `2^n` enumeration is not worth it
and no real controller has that many.

`ModSet.With`/`Without` **must return copies**, because the resolver holds the set across
the hot path and an accidental mutation would change resolution mid-performance.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/midi/ -v -race`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/midi/
git commit -m "feat(midi): most-specific-wins modifier resolution with fallback"
```

---

### Task 4: Value modes, modifiers, and encoder accumulation

**Files:**
- Create: `internal/midi/encoder.go`
- Create: `internal/midi/modifiers.go`
- Create: `internal/midi/encoder_test.go`
- Create: `internal/midi/modifiers_test.go`

**Interfaces:**
- Consumes: `Message`, `Control`, `ModSet` from Tasks 1–3.
- Produces:
  ```go
  package midi

  // Handler turns one message plus held state into Actions. It is the top of
  // the pure input path and the only thing the device layer calls.
  type Handler struct {
      table    atomic.Pointer[Table]   // hot-swappable, see hotreload
      mods     ModSet
      latched  map[string]bool
      toggled  map[string]bool
      encoder  map[msgKey]float64      // accumulated position per control
      onAction func(engine.Action)
      counts   Counts
  }
  func NewHandler(table *Table, onAction func(engine.Action)) *Handler

  // Handle processes one message and emits zero or more Actions.
  func (h *Handler) Handle(m Message) error
  // SetTable swaps the mapping atomically (hot reload).
  func (h *Handler) SetTable(t *Table)
  // ActiveMods returns a copy for display.
  func (h *Handler) ActiveMods() ModSet
  func (h *Handler) Counts() map[string]uint64

  // RelativeNull is the encoder null point from spec §5.2.
  const RelativeNull = 64
  // Absolute maps a 0..127 CC to -1..1.
  func Absolute(v uint8) float64
  // RelativeDelta converts a relative-encoder value to an increment.
  func RelativeDelta(v uint8) float64
  ```

- [ ] **Step 1: Write the failing tests**

Create `internal/midi/encoder_test.go`:

```go
package midi

import (
    "math"
    "testing"

    "github.com/dukedelaet/mix-tui/internal/engine"
)

func TestAbsoluteMapsFullRange(t *testing.T) {
    if math.Abs(Absolute(0)+1) > 1e-9 { t.Errorf("Absolute(0) = %v", Absolute(0)) }
    if math.Abs(Absolute(127)-1) > 1e-9 { t.Errorf("Absolute(127) = %v", Absolute(127)) }
    if math.Abs(Absolute(64)) > 0.01 { t.Errorf("Absolute(64) = %v, want ~0", Absolute(64)) }
}

func TestRelativeDeltaAroundNull(t *testing.T) {
    // Review Focus #1: one detent must move the value a little, not to the rail
    if d := RelativeDelta(RelativeNull); d != 0 {
        t.Errorf("null point must be 0, got %v", d)
    }
    if d := RelativeDelta(RelativeNull + 2); math.Abs(d) > 0.05 {
        t.Errorf("single detent %v is too large", d)
    }
    if RelativeDelta(RelativeNull-2)*RelativeDelta(RelativeNull+2) >= 0 {
        t.Error("up and down detents must have opposite signs")
    }
}

func TestRelativeEncoderAccumulatesNotJumps(t *testing.T) {
    // Review Focus #1: 10 detents up from centre must reach a modest value,
    // not +1.0. This is the jog-wheel bug.
    h := NewHandler(encoderMapping(t), func(a engine.Action) { last = a })
    for i := 0; i < 10; i++ {
        h.Handle(Message{Kind: KindCC, Chan: 1, Num: 9, Value: RelativeNull + 2})
    }
    if last.Value <= 0.05 || last.Value >= 0.5 {
        t.Errorf("10 detents gave Value = %v, want a small increment", last.Value)
    }
}

func TestRelativeEncoderReachesFullScaleEventually(t *testing.T) {
    h := NewHandler(encoderMapping(t), func(a engine.Action) { last = a })
    for i := 0; i < 100; i++ {
        h.Handle(Message{Kind: KindCC, Chan: 1, Num: 9, Value: RelativeNull + 2})
    }
    if last.Value < 0.99 { t.Errorf("100 detents gave %v, want ~1.0 (clamped)", last.Value) }
}

func TestAbsoluteControlIgnoresNullPoint(t *testing.T) {
    h := NewHandler(absoluteMapping(t), func(a engine.Action) { last = a })
    h.Handle(Message{Kind: KindCC, Chan: 1, Num: 9, Value: 127})
    if math.Abs(last.Value-1) > 1e-6 {
        t.Errorf("absolute 127 gave %v, want 1", last.Value)
    }
}

func TestValueIsClamped(t *testing.T) {
    h := NewHandler(encoderMapping(t), func(a engine.Action) { last = a })
    for i := 0; i < 500; i++ {
        h.Handle(Message{Kind: KindCC, Chan: 1, Num: 9, Value: RelativeNull + 2})
    }
    if last.Value > 1.0 { t.Errorf("Value escaped range: %v", last.Value) }
}
```

Create `internal/midi/modifiers_test.go`:

```go
package midi

import "testing"

func modifierMapping(t *testing.T) *Table {
    return tableFrom(t, `
device: "T"
modifiers:
  shift:
    kind: cc
    chan: 1
    num: 49
    pressed: 127
    released: 0
    mode: momentary
  lock:
    kind: note
    chan: 1
    num: 50
    mode: toggle
controls:
  - midi: {kind: cc, chan: 1, num: 40}
    action: {kind: DeckHotcueSet, target: 0}
    slot: 1
  - midi: {kind: cc, chan: 1, num: 40}
    mods: [shift]
    action: {kind: DeckHotcueDelete, target: 0}
    slot: 1
`)
}

func TestMomentaryModifierPressAndRelease(t *testing.T) {
    h := NewHandler(modifierMapping(t), func(a engine.Action) { last = a })
    h.Handle(Message{Kind: KindCC, Chan: 1, Num: 49, Value: 127})   // shift down
    if !h.ActiveMods()["shift"] { t.Fatal("shift not held after press") }
    h.Handle(Message{Kind: KindCC, Chan: 1, Num: 40, Value: 127})
    if last.Action != engine.DeckHotcueDelete { t.Errorf("got %v", last.Action) }
    h.Handle(Message{Kind: KindCC, Chan: 1, Num: 49, Value: 0})     // shift up
    if h.ActiveMods()["shift"] { t.Error("shift still held after release") }
    h.Handle(Message{Kind: KindCC, Chan: 1, Num: 40, Value: 127})
    if last.Action != engine.DeckHotcueSet { t.Errorf("after release got %v", last.Action) }
}

func TestNoteModifierUsesNoteOnOff(t *testing.T) {
    h := NewHandler(modifierMapping(t), func(a engine.Action) { last = a })
    h.Handle(Message{Kind: KindNote, Chan: 1, Num: 50, Velocity: 100})  // lock on
    h.Handle(Message{Kind: KindCC, Chan: 1, Num: 40, Value: 127})
    _ = last  // 'lock' is not bound to the action, so it falls back to base
    if last.Action != engine.DeckHotcueSet {
        t.Errorf("an unbound modifier must fall back, got %v", last.Action)
    }
    h.Handle(Message{Kind: KindNote, Chan: 1, Num: 50, Velocity: 0})    // lock off
}

func TestToggleModifierTogglesOnEachPress(t *testing.T) {
    // Review Focus #3: note-off must NOT clear a toggled modifier
    h := NewHandler(modifierMapping(t), func(a engine.Action) { last = a })
    h.Handle(Message{Kind: KindNote, Chan: 1, Num: 50, Velocity: 100})
    if !h.ActiveMods()["lock"] { t.Fatal("toggle did not engage") }
    h.Handle(Message{Kind: KindNote, Chan: 1, Num: 50, Velocity: 0})   // note-off
    if !h.ActiveMods()["lock"] {
        t.Error("note-off cleared a toggled modifier — must only toggle on press")
    }
    h.Handle(Message{Kind: KindNote, Chan: 1, Num: 50, Velocity: 100}) // press again
    if h.ActiveMods()["lock"] { t.Error("second press did not disengage") }
}

func TestMomentaryActionFiresOnRelease(t *testing.T) {
    m, _ := ParseMapping("x.yaml", []byte(`
device: "T"
controls:
  - midi: {kind: cc, chan: 1, num: 20}
    action: {kind: DeckCue, target: 0}
    momentary: true
`))
    tab, _ := m.Compile()
    h := NewHandler(tab, func(a engine.Action) { last = a })
    h.Handle(Message{Kind: KindCC, Chan: 1, Num: 20, Value: 127})
    if last.Value != 1 { t.Errorf("press emitted %v, want 1", last.Value) }
    h.Handle(Message{Kind: KindCC, Chan: 1, Num: 20, Value: 0})
    if last.Value != 0 { t.Errorf("release emitted %v, want 0", last.Value) }
}
```

Add `var last engine.Action` as a package-level test variable.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/midi/ -run 'Absolute|Relative|MomentaryModifier|Toggle|Modifier' -v`
Expected: FAIL — `undefined: Handler`, `undefined: Absolute`.

- [ ] **Step 3: Implement `internal/midi/encoder.go`**

```go
const RelativeNull = 64

// Absolute maps a 0..127 CC to -1..1, with 64 at centre.
func Absolute(v uint8) float64 { return float64(v)/127.0*2.0 - 1.0 }

// RelativeDelta converts an encoder value to an increment. The null point
// yields exactly zero, and one detent is a small step so a jog wheel does not
// slam a fader to the rail.
func RelativeDelta(v uint8) float64 {
    d := float64(int(v) - RelativeNull)
    return d / 127.0 * 0.2
}
```

The `0.2` scale means the full ±63 detent range saturates the control. State that as a
comment — it is a design decision, not a magic number.

- [ ] **Step 4: Implement `internal/midi/modifiers.go`**

```go
// press applies a modifier source message to the held set according to its
// declared mode. Momentary follows the wire; toggle flips on note-on only.
func (h *Handler) applyModifier(name string, src ModSource, m Message) {
    switch src.ModMode {
    case ModMomentary:
        if m.Kind == KindNote {
            if m.Velocity > 0 { h.mods = h.mods.With(name) } else { h.mods = h.mods.Without(name) }
        } else {
            if m.Value == src.Pressed { h.mods = h.mods.With(name) } else { h.mods = h.mods.Without(name) }
        }
    case ModToggle:
        // Review Focus #3: note-OFF must not disengage a toggled modifier
        if m.Kind == KindNote && m.Velocity == 0 { return }
        if h.toggled[name] { h.mods = h.mods.Without(name); h.toggled[name] = false
        } else { h.mods = h.mods.With(name); h.toggled[name] = true }
    case ModLatching:
        // the controller toggles; we track our own parity so resolution is stable
        if h.latched[name] { h.mods = h.mods.Without(name); h.latched[name] = false
        } else { h.mods = h.mods.With(name); h.latched[name] = true }
    }
}
```

- [ ] **Step 5: Implement `Handler`**

`Handle` in order: count ignored categories and return early if the message was not a CC
or Note → check whether the message matches any modifier source and, if so, apply it and
return (a modifier message does not also trigger a control) → `Resolve` → emit per mode:

- `ModeAbsolute` → `Value = Absolute(msg.Value)`
- `ModeRelative` → accumulate `h.encoder[key] += RelativeDelta(msg.Value)`, clamp, emit
- `ModeMomentary` → emit `Value = 1` on press and `Value = 0` on release
- `ModeToggle` → flip an internal per-control bool, emit `Value = 1`/`0`

Every emitted `Value` passes through `dsp.ClampNormalized`. Every emitted `Action` gets
`Target` and `Slot` from the resolved `Control`.

- [ ] **Step 6: Run tests to verify they pass**

Run: `go test ./internal/midi/ -v -race`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/midi/
git commit -m "feat(midi): value modes, modifier state machine, encoder accumulation"
```

---

### Task 5: Hot reload

**Files:**
- Create: `internal/midi/hotreload.go`
- Create: `internal/midi/hotreload_test.go`

**Interfaces:**
- Consumes: `ParseMapping`, `Table` from Task 2; `Handler.SetTable` from Task 4.
- Produces:
  ```go
  package midi

  // Watcher polls a directory of mapping files and swaps the handler's table
  // when a file's mtime or size changes. Polling, not fsnotify: the config
  // directory is tiny and a 2s interval is imperceptible.
  type Watcher struct { /* ... */ }
  func NewWatcher(dir string, h *Handler, logf func(string, ...any)) *Watcher
  // ReloadOnce performs one scan. Exported so it is testable without a ticker.
  func (w *Watcher) ReloadOnce() (changed int, errs []error)
  func (w *Watcher) Run(ctx context.Context)     // polls until cancelled

  // LoadDeviceTables parses every mapping in a directory into name->Table.
  func LoadDeviceTables(dir string) (map[string]*Table, []error)
  ```

- [ ] **Step 1: Write the failing tests**

Create `internal/midi/hotreload_test.go`:

```go
package midi

import (
    "context"
    "os"
    "path/filepath"
    "testing"
    "time"
)

func writeMapping(t *testing.T, dir, name, body string) string {
    t.Helper()
    p := filepath.Join(dir, name)
    if err := os.WriteFile(p, []byte(body), 0o644); err != nil { t.Fatal(err) }
    return p
}

func TestReloadOncePicksUpChanges(t *testing.T) {
    dir := t.TempDir()
    writeMapping(t, dir, "a.yaml", `
device: "A"
controls:
  - midi: {kind: cc, chan: 1, num: 9}
    action: {kind: DeckPitch, target: 0}
`)
    h := NewHandler(nil, func(eng engine.Action) {})
    w := NewWatcher(dir, h, t.Logf)
    n, errs := w.ReloadOnce()
    if n != 1 { t.Fatalf("changed = %d, want 1", n) }
    if len(errs) != 0 { t.Fatalf("errs = %v", errs) }
    if h.Table() == nil { t.Fatal("handler table not set") }
}

func TestReloadKeepsLastValidTableOnParseError(t *testing.T) {
    // Review Focus #4: a typo mid-set must not stop MIDI.
    dir := t.TempDir()
    writeMapping(t, dir, "a.yaml", `
device: "A"
controls:
  - midi: {kind: cc, chan: 1, num: 9}
    action: {kind: DeckPitch, target: 0}
`)
    h := NewHandler(nil, func(eng engine.Action) {})
    w := NewWatcher(dir, h, t.Logf)
    w.ReloadOnce()
    before := h.Table()

    writeMapping(t, dir, "a.yaml", "device: \"A\"\ncontrols:\n  - actoin: {kind: DeckPitch}\n")
    n, errs := w.ReloadOnce()
    if len(errs) == 0 { t.Fatal("expected a parse error") }
    if n != 0 { t.Errorf("changed = %d, want 0 on parse failure", n) }
    if h.Table() != before { t.Error("handler table was replaced by a failed parse") }
}

func TestReloadIsANoopWhenNothingChanged(t *testing.T) {
    dir := t.TempDir()
    writeMapping(t, dir, "a.yaml", "device: \"A\"\ncontrols: []\n")
    h := NewHandler(nil, func(eng engine.Action) {})
    w := NewWatcher(dir, h, t.Logf)
    w.ReloadOnce()
    n, errs := w.ReloadOnce()
    if n != 0 { t.Errorf("second scan changed %d files, want 0", n) }
    if len(errs) != 0 { t.Errorf("errs on unchanged scan: %v", errs) }
}

func TestWatcherRunsAndStops(t *testing.T) {
    dir := t.TempDir()
    h := NewHandler(nil, func(eng engine.Action) {})
    w := NewWatcher(dir, h, t.Logf)
    ctx, cancel := context.WithCancel(context.Background())
    done := make(chan struct{})
    go func() { w.Run(ctx); close(done) }()
    writeMapping(t, dir, "a.yaml", "device: \"A\"\ncontrols: []\n")
    time.Sleep(2500 * time.Millisecond)
    cancel()
    select {
    case <-done:
    case <-time.After(2 * time.Second):
        t.Fatal("Watcher.Run did not stop on cancel")
    }
}

func TestLoadDeviceTablesIgnoresNonYAML(t *testing.T) {
    dir := t.TempDir()
    writeMapping(t, dir, "a.yaml", "device: \"A\"\ncontrols: []\n")
    writeMapping(t, dir, "notes.txt", "ignore me")
    tables, errs := LoadDeviceTables(dir)
    if len(tables) != 1 { t.Errorf("loaded %d tables, want 1", len(tables)) }
    if len(errs) != 0 { t.Errorf("errs = %v", errs) }
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/midi/ -run Reload -v`
Expected: FAIL — `undefined: NewWatcher`.

- [ ] **Step 3: Add `Table()` to `Handler`**

```go
func (h *Handler) Table() *Table { return h.table.Load() }
```

- [ ] **Step 4: Implement `internal/midi/hotreload.go`**

```go
type Watcher struct {
    dir   string
    h     *Handler
    logf  func(string, ...any)
    seen  map[string]fileStamp   // path -> {mtime, size}
    mu    sync.Mutex
}
```

`ReloadOnce` scans `*.yaml`/`*.yml`, stats each, compares to `seen`, and for changed or
new files parses and compiles. **Only call `h.SetTable` when the parse and compile both
succeed.** Record the stamp either way? No — record only on success, so a file that stays
broken is retried and re-reported rather than silently skipped. Record errors and continue
scanning: one bad file must not block the others.

`Run` calls `ReloadOnce` on a 2s ticker until `ctx.Done()`.

`LoadDeviceTables` returns every table keyed by device name, plus a slice of errors for
the ones that failed.

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/midi/ -v -race`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/midi/
git commit -m "feat(midi): polling hot reload that keeps the last valid table"
```

---

### Task 6: The device layer

**Files:**
- Create: `internal/midi/device_linux.go`
- Create: `internal/midi/device_test.go`

**Interfaces:**
- Consumes: `Handler.Handle` from Task 4, `LoadDeviceTables` from Task 5.
- Produces:
  ```go
  package midi

  type Device struct { /* ... */ }
  // OpenDevices lists connected MIDI input ports.
  func OpenDevices() ([]DeviceInfo, error)
  type DeviceInfo struct { Name string; Index int }

  // Listener receives every incoming message already converted to Message.
  type Listener struct { /* ... */ }
  func OpenListener(h *Handler) (*Listener, error)
  func (l *Listener) Devices() []DeviceInfo
  // Close stops reading. Idempotent.
  func (l *Listener) Close() error
  ```

**This is the only file that imports `gitlab.com/gomidi`.** Everything above it works on
`Message`.

- [ ] **Step 1: Write the failing tests**

Create `internal/midi/device_test.go`:

```go
package midi

import "testing"

// These run with no hardware. They assert the pure conversion between wire
// messages and our internal Message, which is the part that can be wrong.

func TestWireConversionForCC(t *testing.T) {
    m := fromWireCC(3, 20, 127)
    if m.Kind != KindCC { t.Errorf("kind = %v", m.Kind) }
    if m.Channel != 3 { t.Errorf("chan = %d", m.Channel) }
    if m.Num != 20 { t.Errorf("num = %d", m.Num) }
    if m.Value != 127 { t.Errorf("value = %d", m.Value) }
}

func TestWireConversionClampsChannel(t *testing.T) {
    if m := fromWireCC(200, 20, 64); m.Channel > 15 {
        t.Errorf("channel %d out of range", m.Channel)
    }
}

func TestWireConversionNoteOffIsZeroVelocity(t *testing.T) {
    m := fromWireNote(1, 40, 0)
    if m.Kind != KindNote { t.Error("kind") }
    if m.Velocity != 0 { t.Errorf("note-off velocity = %d", m.Velocity) }
}

func TestFromWireClassifiesClockAsOther(t *testing.T) {
    m := fromWireRaw(0xF8)
    if m.Kind != KindOther { t.Errorf("clock should be Other, got %v", m.Kind) }
    if m.Other != OtherClock { t.Errorf("category = %v", m.Other) }
}

func TestFromWireClassifiesSysex(t *testing.T) {
    if m := fromWireRaw(0xF0); m.Other != OtherSysex {
        t.Errorf("0xF0 should be sysex, got %v", m.Other)
    }
}

func TestOpenDevicesDoesNotPanicWithoutHardware(t *testing.T) {
    devs, err := OpenDevices()
    if err != nil && len(devs) != 0 {
        t.Errorf("returned %d devices and an error simultaneously", len(devs))
    }
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/midi/ -run 'Wire|OpenDevices' -v`
Expected: FAIL — `undefined: fromWireCC`.

- [ ] **Step 3: Implement `internal/midi/device_linux.go`**

```go
//go:build linux

package midi

// #cgo pkg-config: alsa
import "C"

import (
    "gitlab.com/gomidi/midi/v2"
    _ "gitlab.com/gomidi/midi/v2/drivers/rtmididrv"
)
```

`fromWireCC`, `fromWireNote`, and `fromWireRaw` are **pure functions over raw status
bytes** and take no gomidi types — which is why their tests run with no hardware.

`OpenListener` uses `midi.NewDriver()`, enumerates inputs, and starts a goroutine per
port reading messages, converting each to `Message` and calling `h.Handle`. Guard the
goroutine with a context and ensure `Close` waits for it, so no message is handled after
the handler is torn down.

`OpenDevices` returns port names; **names are how mappings match**, and controllers rename
themselves, so the config uses substring matching by default (spec §5.1).

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/midi/ -v -race`
Expected: PASS — including with no MIDI hardware present.

- [ ] **Step 5: Verify the package boundary**

Run:
```bash
go list -deps ./internal/midi/ | grep gomidi
```
Expected: prints `gitlab.com/gomidi/...` — this is fine, since it is one package. Verify
the *decision* code is clean instead:

```bash
grep -l gomidi internal/midi/*.go
```
Expected: only `device_linux.go`.

- [ ] **Step 6: Commit**

```bash
git add internal/midi/
git commit -m "feat(midi): gomidi device layer with pure wire-message conversion"
```

---

### Task 7: Config loading and keybindings

**Files:**
- Create: `internal/config/config.go`
- Create: `internal/config/load.go`
- Create: `internal/config/keybindings.go`
- Create: `internal/config/config_test.go`

**Interfaces:**
- Consumes: `engine.ActionKind` from milestone 1/plan 3 Task 1, `midi.Control` semantics.
- Produces:
  ```go
  package config

  type Config struct {
      Audio    AudioConfig    `yaml:"audio"`
      UI       UIConfig       `yaml:"ui"`
      Keys     map[string]KeyBinding `yaml:"keys"`
  }
  type AudioConfig struct {
      SampleRate int    `yaml:"sample_rate"`   // 0 = negotiate
      MainTarget string `yaml:"main_target"`
      CueTarget  string `yaml:"cue_target"`
      RingFrames int    `yaml:"ring_frames"`
      TargetLatencyFrames int `yaml:"target_latency_frames"`
  }
  type UIConfig struct {
      DefaultView string `yaml:"default_view"` // "A" | "B" | "C" | "D"
      LibraryNode string `yaml:"library_root"`
  }
  type KeyBinding struct {
      Action string `yaml:"action"`
      Target int    `yaml:"target"`
      Value  string `yaml:"value"`   // "toggle" | "inc" | "dec" | a number
      Repeat bool   `yaml:"repeat"`
  }

  // Load reads config.yaml strictly. Missing file is NOT an error — it yields
  // defaults, so the app runs with no configuration at all.
  func Load(path string) (*Config, error)
  func Defaults() *Config

  // KeyResolver maps a key name ("q", "ctrl+s", "f1") to an Action. It shares the
  // same Action output as MIDI, so both paths converge (spec §5.1).
  type KeyResolver struct { /* ... */ }
  func NewKeyResolver(cfg *Config) (*KeyResolver, error)
  func (r *KeyResolver) Resolve(key string) (engine.Action, bool)
  func (r *KeyResolver) Bindings() []string   // for a help view
  ```

- [ ] **Step 1: Write the failing tests**

Create `internal/config/config_test.go`:

```go
package config

import (
    "os"
    "path/filepath"
    "strings"
    "testing"

    "github.com/dukedelaet/mix-tui/internal/engine"
)

func writeCfg(t *testing.T, body string) string {
    t.Helper()
    p := filepath.Join(t.TempDir(), "config.yaml")
    if err := os.WriteFile(p, []byte(body), 0o644); err != nil { t.Fatal(err) }
    return p
}

func TestLoadMissingFileYieldsDefaults(t *testing.T) {
    cfg, err := Load(filepath.Join(t.TempDir(), "nope.yaml"))
    if err != nil { t.Fatalf("missing config must not be an error: %v", err) }
    if cfg.Audio.RingFrames == 0 { t.Error("defaults not applied") }
}

func TestLoadRejectsUnknownField(t *testing.T) {
    p := writeCfg(t, "audio:\n  ring_frames: 44100\n  samle_rate: 48000\n")
    _, err := Load(p)
    if err == nil { t.Fatal("unknown field must error") }
    if !strings.Contains(err.Error(), "samle_rate") {
        t.Errorf("error should name the bad field: %v", err)
    }
}

func TestLoadErrorCarriesPathAndLine(t *testing.T) {
    p := writeCfg(t, "audio:\n  ring_frames: 44100\n  bogus: 1\n")
    _, err := Load(p)
    if err == nil { t.Fatal("expected error") }
    if !strings.Contains(err.Error(), p) { t.Errorf("error must name the file: %v", err) }
    if !strings.Contains(err.Error(), "line") { t.Errorf("error must carry a line: %v", err) }
}

func TestLoadParsesValues(t *testing.T) {
    p := writeCfg(t, "audio:\n  ring_frames: 44100\n  target_latency_frames: 8820\nui:\n  default_view: D\n")
    cfg, err := Load(p)
    if err != nil { t.Fatal(err) }
    if cfg.Audio.RingFrames != 44100 { t.Errorf("ringFrames = %d", cfg.Audio.RingFrames) }
    if cfg.UI.DefaultView != "D" { t.Errorf("view = %q", cfg.UI.DefaultView) }
}

func TestLoadRejectsInvalidView(t *testing.T) {
    p := writeCfg(t, "ui:\n  default_view: Z\n")
    if _, err := Load(p); err == nil { t.Fatal("invalid view must error") }
}

func TestDefaultsAreSane(t *testing.T) {
    d := Defaults()
    if d.UI.DefaultView != "D" { t.Errorf("spec says D is the default: %q", d.UI.DefaultView) }
    if d.Audio.RingFrames < 4410 { t.Errorf("ringFrames %d is too small", d.Audio.RingFrames) }
    if d.Audio.TargetLatencyFrames < 4410 { t.Errorf("latency target %d too small", d.Audio.TargetLatencyFrames) }
}

func TestKeyResolverMapsToAction(t *testing.T) {
    p := writeCfg(t, "keys:\n  space: {action: DeckPlay, target: 0}\n")
    cfg, _ := Load(p)
    r, err := NewKeyResolver(cfg)
    if err != nil { t.Fatal(err) }
    a, ok := r.Resolve("space")
    if !ok { t.Fatal("space not bound") }
    if a.Kind != engine.DeckPlay { t.Errorf("kind = %v", a.Kind) }
    if a.Target != 0 { t.Errorf("target = %d", a.Target) }
}

func TestKeyResolverRejectsUnknownAction(t *testing.T) {
    p := writeCfg(t, "keys:\n  z: {action: NotAnAction}\n")
    cfg, _ := Load(p)
    if _, err := NewKeyResolver(cfg); err == nil {
        t.Fatal("unknown action in keybinding must error")
    }
}

func TestKeyResolverRejectsShadowedDefault(t *testing.T) {
    // two bindings for the same key is a config bug worth surfacing
    p := writeCfg(t, "keys:\n  space: {action: DeckPlay, target: 0}\n")
    cfg, _ := Load(p)
    cfg.Keys["q"] = cfg.Keys["space"]
    if _, err := NewKeyResolver(cfg); err == nil {
        t.Fatal("duplicate key bindings must error")
    }
}

func TestKeyResolverToggleValue(t *testing.T) {
    p := writeCfg(t, "keys:\n  c: {action: DeckCue, target: 0, value: toggle}\n")
    cfg, _ := Load(p)
    r, _ := NewKeyResolver(cfg)
    a, _ := r.Resolve("c")
    if a.Value != 1 { t.Errorf("toggle press = %v, want 1", a.Value) }
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/config/ -v`
Expected: FAIL — `undefined: Load`.

- [ ] **Step 3: Implement `internal/config/config.go` and `load.go`**

`Defaults()` returns the spec's values: `DefaultView: "D"`, `RingFrames: 8820`,
`TargetLatencyFrames: 8820` (200ms at 44.1k), `SampleRate: 0` (negotiate).

`Load`: `os.ReadFile`; if `os.IsNotExist`, return `Defaults(), nil`. Otherwise decode with
`KnownFields(true)` and validate, wrapping every error as
`fmt.Errorf("%s: %w", path, err)` so file and line survive.

Validate `DefaultView ∈ {A,B,C,D}` and that positive-int fields are positive.

- [ ] **Step 4: Implement `internal/config/keybindings.go`**

`NewKeyResolver` builds `map[string]engine.Action` plus a per-key `Value` strategy.
`Value` accepts `"toggle"` (alternate 1/0 per press, tracked by the caller), `"inc"`/
`"dec"` (used by plan 4's key repeat), or a literal float. Reject duplicate keys after
merging with defaults — the defaults are merged first, so a user binding `space` overrides
it without error, while binding the same key twice in the user's own file is a mistake
worth reporting.

Ship a default keymap covering: play (`space`), cue (`c`), sync (`s`), quantize (`q`),
pitch nudge (`[` / `]`), hotcue set/jump/delete (`1`–`8` plus shift), loop beat
(`l`/`k`), loop in/out (`[`/`]` on the loop layer), loop roll (`r`), zoom toggle (`z`),
view cycle (`v`), library (`/`), quit (`q` on ctrl, i.e. `ctrl+c`).

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/config/ -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/config/
git commit -m "feat(config): strict config loading, defaults, and keybindings"
```

---

### Task 8: End-to-end — synthetic MIDI into a live engine

**Files:**
- Create: `internal/midi/integration_test.go`
- Create: `internal/midi/midimapdump/main.go`

**Interfaces:**
- Consumes: `Handler` from Task 4, `engine.Engine` from milestone 1, `config.KeyResolver`
  from Task 7.
- Produces: `midimapdump`, a CLI that reads a mapping file and prints the bindings it
  defines, sorted — the tool for "why is my controller not doing what I expect".

- [ ] **Step 1: Write the failing integration test**

Create `internal/midi/integration_test.go`:

```go
package midi_test

import (
    "testing"

    "github.com/dukedelaet/mix-tui/internal/audio"
    "github.com/dukedelaet/mix-tui/internal/config"
    "github.com/dukedelaet/mix-tui/internal/dsp"
    "github.com/dukedelaet/mix-tui/internal/engine"
    "github.com/dukedelaet/mix-tui/internal/midi"
)

// TestSyntheticControllerDrivesEngine plays a "performance" with no hardware:
// a table of literal MIDI messages produces the same Actions a real controller
// would, and the engine ends in the expected state.
func TestSyntheticControllerDrivesEngine(t *testing.T) {
    eng := engine.New(engine.Config{SampleRate: dsp.SampleRate, BlockFrames: 512,
                                    RingFrames: 8820, DeckCount: 2})
    track := engine.NewTrackFromPCM(dsp.Sine(440, 60))
    eng.Deck(0).Load(track)
    eng.Deck(1).Load(track)
    eng.Deck(0).AttachBeatGrid(testGrid(128))
    eng.Deck(1).AttachBeatGrid(testGrid(128))

    table, err := midi.ParseMapping("test", []byte(performanceMapping))
    if err != nil { t.Fatal(err) }
    compiled, err := table.Compile()
    if err != nil { t.Fatal(err) }

    h := midi.NewHandler(compiled, eng.Apply)
    step := func(m midi.Message) {
        if err := h.Handle(m); err != nil { t.Fatalf("handle %v: %v", m, err) }
    }

    // load both decks, play A
    step(midi.Message{Kind: midi.KindCC, Channel: 1, Num: 9, Value: 127})   // A play
    step(midi.Message{Kind: midi.KindCC, Channel: 1, Num: 10, Value: 127})  // B play
    // push A's pitch up
    for i := 0; i < 20; i++ {
        step(midi.Message{Kind: midi.KindCC, Channel: 1, Num: 20, Value: midi.RelativeNull + 2})
    }
    // 4-beat loop on A
    step(midi.Message{Kind: midi.KindCC, Channel: 1, Num: 30, Value: 90})
    // shift+hotcue1 sets nothing but deletes
    step(midi.Message{Kind: midi.KindCC, Channel: 1, Num: 49, Value: 127})  // shift down
    step(midi.Message{Kind: midi.KindCC, Channel: 1, Num: 40, Value: 127})  // shift+hc1
    step(midi.Message{Kind: midi.KindCC, Chan: 1, Num: 49, Value: 0})

    s := eng.Snapshot()
    if !s.Decks[0].Playing { t.Error("A should be playing") }
    if !s.Decks[1].Playing { t.Error("B should be playing") }
    if s.Decks[0].Pitch <= 0 { t.Errorf("A pitch = %v, want > 0", s.Decks[0].Pitch) }
    if s.Decks[0].Pitch > 0.5 { t.Errorf("A pitch = %v, want a small nudge", s.Decks[0].Pitch) }
    if !s.Decks[0].LoopActive { t.Error("A loop should be active") }
}

func TestIgnoredMessagesDoNotStopTheHandler(t *testing.T) {
    // A stream of sysex between real notes must not break the mapping.
    var got int
    table, _ := midi.ParseMapping("t", []byte(performanceMapping))
    compiled, _ := table.Compile()
    h := midi.NewHandler(compiled, func(a engine.Action) { got++ })
    for i := 0; i < 100; i++ {
        h.Handle(midi.Message{Kind: midi.KindOther, Other: midi.OtherSysex})
    }
    h.Handle(midi.Message{Kind: midi.KindCC, Channel: 1, Num: 9, Value: 127})
    if got == 0 { t.Error("handler stopped working after sysex") }
    c := h.Counts()
    if c["sysex"] != 100 { t.Errorf("sysex counted %d, want 100", c["sysex"]) }
}

const performanceMapping = `
device: "Synthetic"
controls:
  - midi: {kind: cc, chan: 1, num: 9}
    action: {kind: DeckPlay, target: 0}
  - midi: {kind: cc, chan: 1, num: 10}
    action: {kind: DeckPlay, target: 1}
  - midi: {kind: cc, chan: 1, num: 20}
    mode: relative
    action: {kind: DeckPitch, target: 0}
  - midi: {kind: cc, chan: 1, num: 30}
    mode: absolute
    action: {kind: LoopBeat, target: 0}
  - midi: {kind: cc, chan: 1, num: 40}
    action: {kind: DeckHotcueSet, target: 0}
    slot: 1
  - midi: {kind: cc, chan: 1, num: 40}
    mods: [shift]
    action: {kind: DeckHotcueDelete, target: 0}
    slot: 1
modifiers:
  shift:
    kind: cc
    chan: 1
    num: 49
    pressed: 127
    released: 0
    mode: momentary
`
```

This test requires two things milestone 1 may not have exported:
`engine.NewTrackFromPCM(dsp.Stereo) *audio.Track` and a way to read loop state from
`Snapshot` (`DeckState.LoopActive bool`, plus `LoopBeatCount float64`). **Add them if
absent** — a Snapshot that omits loop state would leave the TUI unable to render a loop,
so it belongs in the Snapshot regardless of tests.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/midi/ -v -run 'Synthetic|Ignored'`
Expected: FAIL on whichever symbol is missing.

- [ ] **Step 3: Implement `cmd/midi/midimapdump/main.go`**

```go
// midimapdump prints every binding a mapping file defines, sorted by
// (channel, kind, number), so a mis-mapped controller is diagnosable without
// attaching one.
//
//	midimapdump ~/.config/mix-tui/mappings/xone-k2.yaml
//	midimapdump -all ~/.config/mix-tui/mappings/
```

Print: `ch  kind  num   mods           action                  slot  mode`.
Flag `-json` for machine-readable output.

- [ ] **Step 4: Run the full suite**

Run: `go test ./... -race -count=1 && go vet ./... && gofmt -l .`
Expected: all pass, `gofmt -l` silent.

- [ ] **Step 5: Verify the shipped K2 mapping is loadable**

Run: `go run ./cmd/midi/midimapdump internal/testdata/mappings/xone-k2.yaml`
Expected: a table of bindings. **Read it against a real Xone K2 later** — the CC numbers
in the fixture are plausible placeholders, and the first hardware session should reconcile
them. Note that in the fixture's comments so it is not mistaken for verified truth.

- [ ] **Step 6: Commit**

```bash
git add -A
git commit -m "feat(midi): synthetic-controller integration test and mapping dump tool"
```

---

## Self-Review

**Spec coverage** for everything milestone 3 owns:

| Spec § | Task |
|---|---|
| §5.1 three producers, one consumer, normalized values | 1, 7, 8 |
| §5.2 absolute vs relative, null point at 64 | 4 |
| §5.3 modifier set, most-specific-wins, config-declared modes | 2, 3, 4 |
| §5.4 hot reload, atomic swap | 5 |
| §5.5 loop actions are ordinary actions | 1 |
| §7 handled vs ignored, counted | 1, 6, 8 |
| §8 malformed YAML keeps the last valid table | 5 |
| §8 no MIDI devices → keyboard only | 7 (the app layer decides; this plan makes it possible) |
| §10 config location and format | 7 |

**Explicitly deferred, and why it is safe:** MIDI output (LED feedback, sysex to the
controller) and MIDI clock/Traktor sync. Both need the ignored categories implemented,
which this plan's `Counts` structure already anticipates.

**Review Focus coverage:** all five are pinned to tasks — encoder deltas (4), chord
fallback (3), toggle modifiers (4), atomic reload (5), device-name matching (2).

**Proportion:** this plan is roughly 2× the spec, consistent with plan 1. Test code
dominates, which is correct — the value here is in the assertions that encode spec rules
(most-specific-wins ordering, null-point encoding, modifier mode semantics), not in the
thin plumbing that applies them.

**One honest risk:** the shipped `xone-k2.yaml` CC numbers are **placeholders that look
plausible**. They are correct for the architecture and wrong for the hardware until
someone sits with a K2 and maps it. The plan flags this in Task 8 Step 5 rather than
letting the file read as verified truth, because a mapping file that looks authoritative
and is wrong is worse than an obviously-empty one.
