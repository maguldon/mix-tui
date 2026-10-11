package engine

import (
	"time"

	"github.com/maguldon/mix-tui/internal/audio"
	"github.com/maguldon/mix-tui/internal/dsp"
)

// NewTrackFromPCM builds an in-memory Track from interleaved stereo samples.
//
// It exists so tests and the synthetic-controller harness can put real audio
// into a deck without a file on disk. Production decodes through audio.Decode;
// this is the same Track shape, constructed directly.
func NewTrackFromPCM(pcm dsp.Stereo) *audio.Track {
	return &audio.Track{
		Path:       "mem",
		SampleRate: dsp.SampleRate,
		Channels:   2,
		PCM:        pcm,
		Duration:   time.Duration(len(pcm)/2) * time.Second / dsp.SampleRate,
	}
}
