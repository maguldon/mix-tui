# mix-tui

A terminal DJ mixing system. Two decks, pitch, EQ, filters, a crossfader,
beatgrid sync, hotcues, and loops — driven by MIDI controllers and the
keyboard, rendered as a text-mode interface.

Mix the way Serato, Mixxx, or Traktor do — but in a terminal.

## What it is

mix-tui is a DJ application for Linux that renders its entire interface as
Unicode text. There is no GUI, no mouse, and no window. Waveforms are drawn
with Braille dot patterns, the mixer is a set of box-drawing characters, and
every control is a named action you can bind to a key or a knob.

It targets **PipeWire** for audio output (separate MAIN and CUE streams, so you
can cue one track in your headphones over the other) and speaks **MIDI** to
class-compliant controllers — the primary target being the
[Allen & Heath Xone K2](https://allen-heath.com/hardware/xone-k2/).

## Status

**Working and testable.** ~15,500 lines of Go plus ~12,500 lines of tests; the
full suite passes (`go test ./...`). All four binaries build and run.

| Component | State |
|---|---|
| `internal/audio` — pure-Go wav/mp3/flac/ogg decoding, lock-free SPSC ring | Complete |
| `internal/dsp` — resampler, 3-band EQ, filter, limiter | Complete |
| `internal/analyze` — beatgrid + Camelot key detection | Complete |
| `internal/engine` — decks, mixer, loops, sync, command queue | Complete |
| `internal/pipewire` — MAIN + CUE output, pump | Complete |
| `internal/midi` — message parsing, mappings, modifiers, hot reload | Complete |
| `internal/tui` — four layouts, waveforms, library browser, overlays | Complete |
| `internal/config` — YAML config and keybindings | Complete |

Known gaps, so nothing here is a surprise:

- **10 of the 45 declared actions have no consumer yet** — `ViewPrev`,
  `FocusNext`, `ZoomIn`, `ZoomOut`, `DebugToggle`, `HelpToggle`, `LibraryLoad`,
  `LibrarySearch`, `LibrarySort`, `LibraryFocusNext`. They parse from mapping
  files and are emitted by the engine, but neither the engine nor the TUI acts
  on them. The features they name are reachable by key instead.
- **Crates are static lists, and the crate file is yours to edit.** They load
  from `$XDG_CONFIG_HOME/mix-tui/crates.yaml` and a selected crate narrows the
  list to its tracks in the crate's own running order. `ctrl+n` creates one,
  `ctrl+a` adds the highlighted track to the selected crate, `ctrl+d` removes
  it. They are control-key bindings rather than letters because every printable
  key types into the filter, so a letter-bound action could never fire from the
  browser. Rule-based crates (smart playlists by folder, BPM range or date) are
  deferred; the schema leaves room for them.
- **Sample rate is negotiated once, at startup.** `mix-tui` asks for the rate
  your config asks for (44100 by default) and then builds the engine at whatever
  the device actually granted, warning when the two disagree. It is not
  renegotiated afterwards: a device that appears later — which it now can, since
  audio retries in the background — is rendered at the engine's rate rather than
  rebuilding the engine. `mixplay`, which has no interface to rebuild, reports
  `RATE MISMATCH` under `-stats` and carries on.
- **No controller has been validated against real hardware.** Nothing is
  installed into your config directory; the only mapping in the tree is
  `internal/testdata/mappings/xone-k2.yaml`, a fixture for the parser tests
  whose CC numbers are guesses. Write your own and check it with
  `midimapdump`.
- **Downbeats assume 4/4.** A 3/4 or 7/8 track gets a correct tempo and beat
  phase but the wrong bar lines.

## Requirements

- Linux
- Go 1.27.1+
- `libpipewire-0.3-dev` — audio output
- `libasound2-dev` — ALSA-seq MIDI (pulled in transitively by the MIDI driver)

```bash
sudo apt install libpipewire-0.3-dev libasound2-dev
```

`internal/pipewire` is the only package that imports `C`. Everything above it —
including MIDI parsing, mapping, and the entire engine — is pure Go and tests
without a device attached.

## Build

```bash
go build ./cmd/...
# or install one
go install github.com/maguldon/mix-tui/cmd/mix-tui@latest
```

## The binaries

| Command | What it does |
|---|---|
| `mix-tui` | The application. Two decks, mixer, library browser, MIDI and keyboard control, out to a PipeWire device. |
| `mixrender` | Renders an engine state to a WAV file, offline. The verification harness — no device, no terminal. |
| `mixplay` | Plays that same mix to a real device, with MAIN and CUE streams, so you can hear what `mixrender` renders. |
| `midimapdump` | Prints every binding a mapping file defines, so a mis-mapped controller is diagnosable without attaching one. |

### mix-tui

```bash
mix-tui -a trackA.mp3 -b trackB.mp3
```

| Flag | Meaning |
|---|---|
| `-a PATH` | Track for deck A. |
| `-b PATH` | Track for deck B. Requires `-a`. |
| `-library DIR` | Library root to scan at startup. Overrides `ui.library_root`. |
| `-config PATH` | Config file. Defaults to `$XDG_CONFIG_HOME/mix-tui/config.yaml`. |
| `-view A\|B\|C\|D` | Default layout. |
| `-xf N` | Crossfader position at startup, -1..1. |
| `-no-midi` | Disable MIDI input; keyboard only. |
| `-no-audio` | Run the interface without opening a device. |
| `-seed-test-midi` | Apply a scripted performance through the real MIDI path, with no hardware. |
| `-screenshot PATH` | Render one frame to a file and exit. |
| `-width N` / `-height N` | Render size for `-screenshot`. Defaults to 120×40. |

A missing config, a failed audio device, a failed library scan, and no MIDI
inputs at all are each warnings, not errors: the application starts anyway.
Diagnostics go to `$XDG_CACHE_HOME/mix-tui.log`, because the TUI owns the
terminal.

### mixrender and mixplay

```bash
mixrender -out mix.wav -a track1.mp3 -b track2.wav -play 0 -play 1 \
          -bpm-a 128 -bpm-b 124 -sync -loop-a 4 -xf 0.4 -stats

mixplay -a track1.mp3 -b track2.wav -play 0 -seconds 8 \
        -bpm-a 128 -bpm-b 124 -sync -loop-a 4 -stats
```

Both take the same control flags — `-play`, `-seconds`, `-xf`, `-loop-a`,
`-loop-b`, `-roll`, `-sync`, `-quantize`, `-bpm-a`, `-bpm-b`. Neither runs
beat detection; declare the tempo with `-bpm-a`/`-bpm-b` or the beat-dependent
controls will refuse, clearly, rather than invent a tempo. `mixplay` adds
`-main-target`, `-cue-target`, `-ring-frames`, and `-dump`. See
[`cmd/mixrender/README.md`](cmd/mixrender/README.md) and
[`cmd/mixplay/README.md`](cmd/mixplay/README.md).

`mixplay -dump mix.wav` records what the device actually played, so you can
compare it against `mixrender -out mix.wav` for the same flags: one is the
offline path, one is the realtime path. Compare over the frames both produced —
the dump length varies run to run, because the realtime pump is driven by the
wall clock and stops wherever `-seconds` lands.

### Headless rendering

```bash
mix-tui -a track.mp3 -no-audio -screenshot /tmp/shot.txt
mix-tui -a track.mp3 -no-audio -width 80 -height 24 -screenshot /tmp/shot80.txt
```

This is how layout work gets checked. Every view is a pure function of
(width, height, state) to a string, so a frame can be asserted on directly.

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
  library_root: "~/Music/DJ Music"   # scanned at startup; -library overrides
keys:
  space: {action: DeckPlayPause}
  c: {action: DeckCue, target: 0, value: toggle}
  "-": {action: MasterGain, value: dec}
```

Unknown fields are **errors**, not warnings. These files are user-editable, and
a silently ignored typo produces a controller that mysteriously does nothing.

`~` is expanded in `library_root`, so the obvious thing to write works.

### The library

`ui.library_root` is the directory scanned for tracks at startup. It is also
editable from inside the application: press **`S`** for the settings screen,
which shows the active root, the track count, and the config file it writes to.
Type a path, press `⏎`, and it is saved and re-scanned without a restart.

The scan runs off the render path, so the decks keep playing and the meters keep
moving while it works — a real collection is tens of thousands of files. If the
root cannot be read the existing library is kept rather than emptied.

Precedence is `-library` flag, then `ui.library_root`. The flag wins because a
path typed at the prompt is a request for this run. When one is set, the
settings screen says so, so editing the config is not a silent no-op.

There is no way to load a track by typing a path elsewhere: the scan is the only
source of loadable tracks, and `-a`/`-b` remain the one-shot path loaders.

### Mappings

Mapping files live in `$XDG_CONFIG_HOME/mix-tui/mappings/*.yaml`:

```yaml
device: "Xone K2"
match: substring          # controllers rename themselves across reconnects
modifiers:
  shift: {kind: cc, chan: 1, num: 49, pressed: 127, released: 0, mode: momentary}
controls:
  - midi: {kind: cc, chan: 1, num: 9}
    action: {kind: DeckPitch, target: 0}
  - midi: {kind: note, chan: 1, num: 40}
    mods: [shift]
    action: {kind: DeckHotcueDelete, target: 0}
    slot: 1
```

Mappings hot-reload while a device is connected: edit a file mid-set and it is
picked up within two seconds, without dropping audio. A file is installed only
if it both parses *and* compiles, and its timestamp is recorded only on
success — so a broken file is retried on every scan, and fixing it takes effect
immediately rather than requiring a restart. One bad file never blocks the rest.

```bash
midimapdump ~/.config/mix-tui/mappings/xone-k2.yaml   # what does this define?
midimapdump -all ~/.config/mix-tui/mappings/          # every mapping
midimapdump -json xone-k2.yaml                        # machine-readable
```

## Keyboard

Navigation keys are checked **before** config bindings and cannot be rebound:
if a mistyped mapping could shadow `esc` or `tab`, you could lock yourself out
of the screen you are on with no way back and no error.

| Key | Action |
|---|---|
| `tab` / `shift+tab` | Move deck focus, where the decks are on screen (deck focus in the overlay, pane focus inside the library) |
| `v` | Cycle layout: A → B → C → D |
| `z` / `Z` | Toggle the focused deck's zoomed waveform / cycle zoom bar count |
| `↑` `↓` | Nudge the crossfader |
| `/` | Library browser |
| `l` | Quick-load overlay (load without leaving the decks) |
| `S` | Settings: library root, track count, config path |
| `?` | Help · `` ` `` debug overlay · `esc` back |

Remappable deck controls:

| Key | Action |
|---|---|
| `space` | Play / pause the focused deck |
| `c` | Cue (stop and return) |
| `[` `]` | Pitch down / up |
| `s` | Sync toggle · `q` quantize to nearest beat |
| `1`–`8` | Set / jump hotcue slots 1–4 |
| `9` / `0` | Loop in / loop out |
| `w` `e` `y` `u` | 2 / 4 / 8 / 16 beat loop |
| `i` / `o` / `\` | Save loop · recall loop · release loop |
| `r` | Roll, 1 beat (toggles) |
| `m` | Cue/program mix · `-` `=` master gain |

In the library: `↑↓` select, `←→` move the caret in the filter, `⌫` / `⌦` delete
around it, `⏎` load to the focused deck, `s` sort, `d` reverse, and any other
printable key filters the list. Press `?` in the application for the live list.

To load to the **other** deck, move deck focus first and then press `⏎`:
`tab` in the mix view, or `tab` inside the quick-load overlay, which also moves
deck focus. There is no `shift+⏎` — see [Keyboard notes](#keyboard-notes).

<a id="keyboard-notes"></a>
**Keyboard notes.** These are transport facts, not preferences, and each one
cost a binding that looked implemented:

- **`space` is play/pause, not play.** `DeckPlay` only ever starts a deck and is
  what `mixplay` and `mixrender` use, so the transport button is a separate
  action, `DeckPlayPause`. A mapping that says `action: DeckPlay` means "start";
  one that says `DeckPlayPause` means the button.
- **`shift+⏎` does not exist.** A terminal sends Enter's bytes for both, and the
  terminal library reports `enter` for both. A keybinding on `shift+enter` is
  unreachable. Use `tab` then `⏎`.
- **`tab` means different things in different modes.** In the mix view and the
  quick-load overlay it moves *deck* focus. In the full library browser it
  cycles *pane* focus, because that view really does render crates, tracks and
  a filter field. In help, debug and settings — which draw no decks — it does
  nothing at all: a focus change you cannot see is a focus change that did not
  happen.
- Keys that mean navigation are checked before your config bindings and cannot
  be rebound. That is deliberate — a mistyped mapping must not be able to
  shadow `esc` — and it is why `tab`, `l`, `S` and `?` are not remappable.

The full browser needs 120 columns and says so when it does not have them. The
quick-load overlay and the settings screen render at any width, so a narrow
terminal is not a dead end.

### Deck targeting

A key that does not name a deck acts on the **focused** deck, so `tab` and then
`space` plays or pauses deck B. To pin a key to one deck regardless of focus, name it:

```yaml
keys:
  c: {action: DeckCue, target: 0, value: toggle}   # always deck A
```

### Reading the waveform

The waveform is coloured rather than drawn flat: waveform behind the playhead is
one colour and ahead of it another, with hotcues, beat markers, bar lines, the
active loop and the playhead each in their own. A regular beat is a grey tick and
a bar line is orange, so a downbeat is findable without every beat shouting.

When both decks are zoomed they share one window length, so a column is the same
offset from each playhead. At matching tempo the beat markers line up; as the
decks drift you watch the columns slide. Zoom is per-deck — `z` toggles the
focused deck, so `z` then `tab` then `z` puts both on the same scale to compare.

### When something is wrong

A deck that is holding audio it cannot play — the file has been deleted, or the
track has run out — says so on its panel, in place of the silence. It still shows
its title, BPM and waveform, so nothing else about it would tell you.

With no audio device the interface starts anyway and carries a `NO AUDIO` banner
with the reason, retrying every few seconds; the banner clears itself when a
device appears. The boot art reports the device state once before the interface
takes the terminal.

### Layout widths

Layout D is `deck A | mixer | deck B` with the mixer centred. Deck panels grow to
80 columns each and no further; past that the block centres itself rather than
stretching a deck into a mostly-empty panel. Below 90 columns it says so instead
of drawing overlapping panels.

## Architecture at a glance

```
internal/
  audio/      decoding to one canonical PCM format; lock-free SPSC ring
  dsp/        resampler, EQ, filter, limiter — pure Go, no device
  analyze/    STFT → beatgrid and Camelot key
  engine/     all playback state; the only thing that touches audio
  pipewire/   MAIN + CUE output and the producer pump   ← the only cgo
  midi/       raw bytes → Message → Action, with modifiers and hot reload
  config/     YAML config and keybindings
  tui/        four layouts, waveforms, library browser, overlays
```

Four ideas do most of the work:

- **The engine owns all state.** The TUI and MIDI are producers of named
  `engine.Action` values; the engine is the only thing that touches audio. This
  keeps knob turns responsive — a control change is posted to a lock-free
  command queue and lands at the start of the next 512-frame block, so it can
  never queue behind a repaint — and makes the engine testable with no terminal
  and no sound card.
- **A loop wrap is a seek.** Hotcues, nudges, sync, and loop wraps all reduce to
  advancing the read head with correct resampler state, through one primitive
  that primes the resampler *and* the EQ and filter delay lines from the
  destination's own past. One code path, tested for continuity by maximum
  sample delta, gives all of them seamless behaviour by construction.
- **Pure-Go audio decoding.** mp3, wav, flac, and ogg decode with no system
  codec dependency, detected by magic bytes rather than extension. cgo appears
  only at the PipeWire edge, and only transitively at the MIDI one.
- **Tested by measurement, not by listening.** The engine renders to a WAV you
  can assert on — RMS, peak, zero-crossing rate, spectral content — because a
  subtly-detuned pitch shifter still passes a file diff.

## Design goals

- **Mixable by ear and eye.** Beatgrid detection, tempo sync, and beat quantize,
  plus a CDJ-style zoomed waveform with a ΔBEAT readout that tracks playback, so
  you can align beats visually — the affordance that usually needs a third
  screen.
- **Keyboard-complete.** Every action is reachable from the keyboard alone.
- **Fully remappable.** MIDI mappings are per-device YAML files, hot-reloaded
  without dropping audio. Keys and controllers share one action vocabulary, so
  they cannot drift apart.
- **Sample-exact.** Loop wraps, hotcue jumps, and seeks all share one primitive
  that primes the resampler correctly, so they are seamless by construction.
- **Honest about failure.** A track with no detectable beat degrades the
  features that need one and says so; it does not invent a tempo. A key
  detection below the confidence threshold renders as *undetected* rather than
  as a confident guess.

## Verifying

```bash
go test ./... -race && go vet ./... && gofmt -l .
```

The engine and DSP tests assert on rendered samples; the TUI tests assert on
rendered frames. The whole suite runs with no sound card, no MIDI controller,
and no terminal.

## Design documents

| Artifact | Description |
|---|---|
| [`docs/superpowers/specs/2026-10-10-mix-tui-design.md`](docs/superpowers/specs/2026-10-10-mix-tui-design.md) | The design specification |
| [`docs/superpowers/plans/2026-10-10-audio-core.md`](docs/superpowers/plans/2026-10-10-audio-core.md) | Plan 1 — headless audio engine |
| [`docs/superpowers/plans/2026-10-11-device-output.md`](docs/superpowers/plans/2026-10-11-device-output.md) | Plan 2 — PipeWire output |
| [`docs/superpowers/plans/2026-10-11-control-layer.md`](docs/superpowers/plans/2026-10-11-control-layer.md) | Plan 3 — MIDI and config |
| [`docs/superpowers/plans/2026-10-11-interface.md`](docs/superpowers/plans/2026-10-11-interface.md) | Plan 4 — the terminal interface |

## License

[MIT](LICENSE) © maguldon
