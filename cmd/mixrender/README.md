# mixrender

Renders a `mix-tui` engine state to a WAV file.

This is the spec's §9 verification harness. The engine code path that would play to a
speaker writes to a file instead — same decoders, same resampler, same DSP chain, same
mixer, same loop and sync logic. Anything audible in the output is audible in playback,
and any DSP bug is reproducible offline and inspectable in any audio tool.

## Usage

```
mixrender -out <file.wav> [flags]
```

| Flag | Meaning |
|---|---|
| `-out PATH` | Output WAV path. **Required.** |
| `-a PATH` | Track for deck 0. |
| `-b PATH` | Track for deck 1. |
| `-play N` | Play deck N. Repeatable. Defaults to every loaded deck. |
| `-seconds N` | Duration to render. Default 4. |
| `-xf N` | Crossfader position, -1 (full A) to +1 (full B). Default 0. |
| `-bpm-a N` | Declare deck 0's tempo, enabling loops and sync on it. |
| `-bpm-b N` | Declare deck 1's tempo. |
| `-sync` | Lock deck 1's tempo to deck 0. Requires both `-bpm-a` and `-bpm-b`. |
| `-loop-a N` | Arm an N-beat loop on deck 0. Requires `-bpm-a`. |
| `-loop-b N` | Arm an N-beat loop on deck 1. Requires `-bpm-b`. |
| `-quantize` | Snap both decks to their nearest beat. Requires beatgrids. |
| `-roll N` | Roll both decks by N beats. Requires beatgrids. |
| `-stats` | Print the final Snapshot to stderr. |

Decoded formats are whatever the engine supports: WAV, MP3, FLAC, OGG. Output is always
16-bit PCM stereo at 44.1kHz.

## Examples

Render two tracks, synced, with a four-beat loop on deck 1:

```bash
go run ./cmd/mixrender -out /tmp/demo.wav \
  -a trackA.mp3 -b trackB.wav -play 0 -play 1 \
  -bpm-a 128 -bpm-b 124 -sync -loop-b 4 -seconds 30 -stats
```

Hard-pan to one side:

```bash
go run ./cmd/mixrender -out /tmp/a.wav -a track.mp3 -xf -1 -seconds 10
```

Quantize and roll, from the committed fixtures:

```bash
go run ./cmd/mixrender -out /tmp/roll.wav \
  -a internal/testdata/fixtures/sine440.wav \
  -b internal/testdata/fixtures/sine440.wav \
  -bpm-a 128 -bpm-b 128 -quantize -roll 1 -seconds 5 -stats
```

## Beatgrids

`-bpm-a` and `-bpm-b` declare a tempo rather than detecting one. Analysis is deferred to
the library scanner in a later milestone; for verification renders the tempo is known
exactly, and stating it keeps each run instant and deterministic.

Anything beat-dependent — loops, sync, quantize, roll — needs a grid and errors clearly
without one, rather than silently degrading.

## Reading the output

The WAV is ordinary and plays anywhere. `-stats` additionally prints the final Snapshot:

```
  crossfader  +0.00
  master      rms=0.3949 peak=0.6584
  loops       deck0:128.0bpm deck1:124.0bpm 4-beat
  cue         rms=0.0000 peak=0.0000
  overruns    0
  deck 0  loaded=true playing=true pos=900ms/1s pitch=+0.00 bpm=128.0 grid=true level=0.3740
```

`overruns` is the number to watch. It counts blocks the renderer produced that the
consumer could not keep up with; a non-zero value means audio was dropped between the
engine and the file.
