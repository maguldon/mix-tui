# mix-tui

A terminal DJ mixing system. Two decks, pitch, EQ, filters, a crossfader,
beatgrid sync, hotcues, and loops — driven by MIDI controllers and the
keyboard, rendered as a text-mode interface.

> **Status: in development.** The design and implementation plan are complete;
> the code is not written yet. See [Status](#status) below.

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

## Design goals

- **Mixable by ear and eye.** Beatgrid detection, tempo sync, and beat quantize,
  plus a CDJ-style zoomed waveform that tracks playback so you can align beats
  visually — the affordance that usually needs a third screen.
- **Keyboard-complete.** Every action is reachable from the keyboard alone.
- **Fully remappable.** MIDI mappings are per-device YAML files, hot-reloaded
  without dropping audio.
- **Sample-exact.** Loop wraps, hotcue jumps, and seeks all share one primitive
  that primes the resampler correctly, so they are seamless by construction.
- **Honest about failure.** A track with no detectable beat degrades the
  features that need one and says so; it does not invent a tempo. A key
  detection below the confidence threshold renders as *undetected* rather than
  as a confident guess.

## Status

The design specification and a full implementation plan are complete. **No
functional code exists yet** — the repository currently contains the spec, four
implementation plans, and this README.

| Artifact | Description |
|---|---|
| [`docs/superpowers/specs/2026-10-10-mix-tui-design.md`](docs/superpowers/specs/2026-10-10-mix-tui-design.md) | The design specification |
| [`docs/superpowers/plans/2026-10-10-audio-core.md`](docs/superpowers/plans/2026-10-10-audio-core.md) | Plan 1 — headless audio engine |
| [`docs/superpowers/plans/2026-10-11-device-output.md`](docs/superpowers/plans/2026-10-11-device-output.md) | Plan 2 — PipeWire output |
| [`docs/superpowers/plans/2026-10-11-control-layer.md`](docs/superpowers/plans/2026-10-11-control-layer.md) | Plan 3 — MIDI and config |
| [`docs/superpowers/plans/2026-10-11-interface.md`](docs/superpowers/plans/2026-10-11-interface.md) | Plan 4 — the terminal interface |

## Architecture at a glance

- **Engine owns all state.** The TUI and MIDI are producers of named
  `Action`s; the engine is the only thing that touches audio. This keeps knob
  turns responsive (they don't queue behind repaints) and makes the engine
  testable with no terminal and no sound card.
- **A loop wrap is a seek.** Hotcues, nudges, sync, and loop wraps all reduce to
  advancing the read head with correct resampler state. One primitive, tested
  for continuity, gives all of them seamless behaviour.
- **Pure-Go audio decoding.** mp3, wav, flac, and ogg decode without system
  codec dependencies. cgo appears only at the PipeWire and ALSA edges.
- **Tested by measurement, not by listening.** The engine renders to a WAV you
  can assert on — RMS, peak, zero-crossing rate, spectral content — because a
  subtly-detuned pitch shifter still passes a file diff.

## Requirements (planned)

- Linux
- Go 1.27+
- `libpipewire-0.3-dev` and `libasound2-dev` (for audio output and MIDI)

## License

[MIT](LICENSE) © maguldon
