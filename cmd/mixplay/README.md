# mixplay

Plays a `mix-tui` engine state to a real PipeWire device.

This is the device-path counterpart to `mixrender`. Same decoders, resampler, DSP chain,
mixer, loop and sync logic — but the result goes to two independent output streams instead
of a file: **MAIN** to the house, **CUE** to headphones. MAIN and CUE are separate streams
with separate rings, nodes, and drain targets, so cueing a track is never audible in the
PA.

## Usage

```
mixplay [flags]
```

| Flag | Meaning |
|---|---|
| `-a PATH` | Track for deck 0. |
| `-b PATH` | Track for deck 1. |
| `-play N` | Play deck N. Repeatable. |
| `-seconds N` | Duration to play. |
| `-xf N` | Crossfader position, -1 (full A) to +1 (full B). Default 0. |
| `-bpm-a N` | Declare deck 0's tempo, enabling loops and sync on it. |
| `-bpm-b N` | Declare deck 1's tempo. |
| `-sync` | Lock deck 1's tempo to deck 0. Requires both `-bpm-a` and `-bpm-b`. |
| `-loop-a N` | Arm an N-beat loop on deck 0. Requires `-bpm-a`. |
| `-loop-b N` | Arm an N-beat loop on deck 1. Requires `-bpm-b`. |
| `-quantize` | Snap both decks to their nearest beat. Requires beatgrids. |
| `-roll N` | Roll both decks by N beats. Requires beatgrids. |
| `-rate N` | Device sample rate. Default 44100. A mismatched device rate is reported. |
| `-ring-frames N` | Ring depth in frames. 0 uses about 200ms. |
| `-main-target NAME` | Route MAIN to this PipeWire node. |
| `-cue-target NAME` | Route CUE to this PipeWire node. |
| `-stats` | Print the final engine, device, and pump state to stderr. |
| `-dump PATH` | Also write the mixed MAIN to this WAV. |

Decoded formats are whatever the engine supports: WAV, MP3, FLAC, OGG.

## Examples

Play one deck to the default device and report health:

```bash
mixplay -a track.mp3 -play 0 -seconds 30 -stats
```

Cue on headphones, main to a specific node:

```bash
mixplay -a track.mp3 -play 0 -main-target alsa_output.usb-Generic_USB_Audio-00.analog-stereo \
        -cue-target alsa_output.pci-0000_00_1f.3.analog-stereo
```

## Verifying the device path against the offline path

`-dump` is the bridge. It writes the exact MAIN mix the device received, so it can be
compared against a `mixrender` of the same script:

```bash
mixrender -out offline.wav -a trackA.mp3 -b trackB.mp3 -play 0 -play 1 -seconds 30 -bpm-a 128 -bpm-b 128 -sync
mixplay   -dump device.wav -a trackA.mp3 -b trackB.mp3 -play 0 -play 1 -seconds 30 -bpm-a 128 -bpm-b 128 -sync
```

Both use one encoder, so the files are directly comparable. If they diverge, the bug is in
the device path, which the offline render structurally cannot have.

## Reading `-stats`

- `callbacks` per stream — how many drain callbacks fired. Zero means the graph is not
  driving the stream; see the note below.
- `frames` — frames handed to the device.
- `starves` — underruns, where a drain found the ring empty. Should be zero in steady state.
- `rateMismatch` — the daemon granted a different sample rate than requested. Audio plays,
  but at the wrong pitch; this is how you find out.
- `pump.targetMisses` — the renderer could not keep the rings at their target depth.
- `pump.wallMax` — slowest observed render. This runs on the pump goroutine, not the audio
  thread, so it must stay far below one block period.

## Requirements

- A running PipeWire daemon.
- `libpipewire` development headers at build time.
- A session manager able to configure the output device. If `-stats` reports zero callbacks
  on every stream, check the WirePlumber log: this binary cannot distinguish "no device" from
  "device the session manager would not configure", because in both cases nothing drains.