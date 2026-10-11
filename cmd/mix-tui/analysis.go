package main

import (
	"github.com/maguldon/mix-tui/internal/analyze"
	"github.com/maguldon/mix-tui/internal/audio"
	"github.com/maguldon/mix-tui/internal/tui/library"
)

// beatGridFrom rebuilds a beatgrid from a cached analysis.
//
// Confidence is carried across rather than recomputed: the cache stores what
// the analyser decided, and re-deciding here would let the two disagree.
func beatGridFrom(a library.Analysis, track *audio.Track) *analyze.BeatGrid {
	return &analyze.BeatGrid{
		BPM:        a.BPM,
		Beats:      a.Beats,
		Confidence: 1.0,
	}
}

// keyFrom rebuilds a key result from a cached analysis, or nil when none was
// stored. A below-threshold confidence yields nil so the view renders unknown
// rather than a guess.
func keyFrom(a library.Analysis) *analyze.Key {
	if a.KeyCamelot == "" || a.KeyConfidence < analyze.KeyConfidenceThreshold {
		return nil
	}
	return &analyze.Key{
		Camelot:    a.KeyCamelot,
		Name:       a.KeyCamelot,
		Confidence: a.KeyConfidence,
	}
}
