# mix-tui

A terminal DJ application: two decks, a mixer, a library browser, MIDI and
keyboard control, playing to a PipeWire device.

Four switchable layouts, braille waveforms, a CDJ-style zoom view with a ΔBEAT
readout, hotcues, beat-synced loops, and a mid-set quick-load overlay.

## Build

Requires Go 1.27, `libpipewire` and ALSA development headers:

```bash
# Debian / Ubuntu
sudo apt install libpipewire-0.3-dev libasound2-dev

go build ./cmd/mix-tui
```

The only cgo in the project is at the hardware boundaries: the PipeWire output
and the MIDI device layer. Everything above them is pure Go and tests without
any device attached.

## Run

```bash
mix-tui -a trackA.mp3 -b trackB.mp3
```

| Flag | Meaning |
|---|---|
| `-a PATH` | Track for deck A. |
| `-b PATH` | Track for deck B. Requires `-a`. |
| `-library DIR` | Library root to scan at startup. |
| `-config PATH` | Config file. Defaults to `$XDG_CONFIG_HOME/mix-tui/config.yaml`. |
| `-view A\|B\|C\|D` | Default layout. |
| `-no-midi` | Disable MIDI input. |
| `-no-audio` | Run the interface without opening a device. |
| `-screenshot PATH` | Render one frame to a file and exit. |
| `-width N` / `-height N` | Render size for `-screenshot`. Defaults to 120×40. |
| `-seed-test-midi` | Replay a scripted performance through the real MIDI path. |
| `-xf N` | Crossfader position at startup, -1..1. |

The engine renders at the sample rate the config asks for (44100 Hz by
default) and relies on PipeWire's graph to resample if the device grants a
different one. The mismatch is reported by `mixplay -stats`, not corrected here.

## Configuration

`$XDG_CONFIG_HOME/mix-tui/config.yaml`. A missing file is not an error: the
application runs on defaults.

```yaml
audio:
  sample_rate: 0            # 0 = the 44100 Hz default
  main_target: ""           # route MAIN to this PipeWire node
  cue_target: ""            # route CUE to this PipeWire node
  ring_frames: 8820         # ~200ms at 44.1kHz
  target_latency_frames: 8820
ui:
  default_view: D
keys:
  space: {action: DeckPlay}
  c: {action: DeckCue, target: 0, value: toggle}
```

The library root comes from the `-library` flag, not the config file.

Unknown fields are **errors**, not warnings. These files are user-editable, and a
silently ignored `mods:` typo produces a controller that mysteriously does
nothing.

### Mappings

Mapping files live in `$XDG_CONFIG_HOME/mix-tui/mappings/*.yaml`. The format is
specified in [spec §5.3](../../docs/superpowers/specs/2026-10-10-mix-tui-design.md):

```yaml
# Must be a substring of the name the port actually reports. A Xone:K2 shows up
# as "XONE:K2:XONE:K2 MIDI 1 24:0", so "Xone K2" matches nothing at all and
# every binding below stays dead with no error.
device: "XONE:K2"
match: substring          # controllers rename themselves across reconnects
modifiers:
  # A note modifier needs no pressed/released: note-on engages, note-off releases.
  # `chan` is the status-byte value: MIDI channels are 1-based in docs and
  # 0-based on the wire, and a mapping file is matched against the wire. A
  # controller documented as channel 15 is written 14 here.
  layer: {kind: note, chan: 14, num: 12}
controls:
  - midi: {kind: cc, chan: 14, num: 16}
    action: {kind: DeckPitch, target: 0}
  - midi: {kind: note, chan: 14, num: 24}
    mods: [layer]
    action: {kind: DeckHotcueDelete, target: 0}
    slot: 1
```

Mappings hot-reload while a device is connected: edit a file mid-set and it is
picked up within two seconds. A parse error keeps the last valid table and logs
the file and line — a typo must never kill a live performance. With `-no-midi`
there is no device, so nothing to reload against.

To see what a mapping file actually defines:

```bash
midimapdump ~/.config/mix-tui/mappings/xone-k2.yaml
```

> **No mapping file is installed for you.** The one in
> `internal/testdata/mappings/xone-k2.yaml` is a parser fixture, but it has now
> been reconciled against a real Xone:K2: every channel and CC/note number in
> it was captured off the hardware. Its header records which parts are fact
> (the numbers, the channel) and which are convention (which knob does what) —
> read that before trusting it.
>
> For your own controller, `midicap` is what produced those numbers. It prints
> every message your surface sends and diffs them against a mapping file, so
> you find out what the hardware does before deciding what you want it to do:
>
> ```bash
> midicap -map ~/.config/mix-tui/mappings/my-controller.yaml
> ```
>
> Type a label and press enter before touching a control to attribute what
> follows; that is how you record a mode-dependent layer. `midimapdump` then
> tells you what a file binds.

## Keys

`v` cycles layout · `tab` moves focus · `z` zoom · `Z` zoom level ·
`/` library · `l` quick-load overlay · `?` help · `` ` `` debug overlay · `esc` back

Press `?` in the application for the full list.

## Verifying without a terminal

```bash
mix-tui -a track.mp3 -b track.mp3 -no-audio -screenshot /tmp/shot.txt
mix-tui -a track.mp3 -no-audio -width 80 -height 24 -screenshot /tmp/shot80.txt
```

This is how layout work should be checked. Every view is a pure function of
(width, height, state) to a string, so a frame can be asserted on directly.

## Troubleshooting

**No sound.**
```bash
pw-top            # is anything running?
pw-cli ls Link    # did anything link?
```
The debug overlay (`` ` ``) shows drain callbacks per stream. **Zero callbacks on
both means nothing is draining at all** — usually a session manager that would
not configure the output device, which is not something this program can fix.

**Controller does nothing.**
1. `` ` `` opens the debug overlay. The MIDI section counts ignored messages per
   category; sysex and clock counts mean the controller is streaming something
   the application deliberately ignores (spec §7).
2. Check the unmapped counter — a rising count means messages arrive but match
   no binding.
3. Run `midimapdump` on your mapping to confirm the CC numbers.
4. Confirm the device name matches: matching is substring by default.

**Display is garbled.** Every line is truncated to the terminal width and every
frame is padded to exactly it, so a wrapped line should not be possible. If it
is, the terminal is reporting a width the program cannot honour.

## Verification

```bash
go test ./... -race && go vet ./... && gofmt -l .
```
