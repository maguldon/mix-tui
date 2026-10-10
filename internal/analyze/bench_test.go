package analyze

import "testing"

func BenchmarkComputeSpectrogram(b *testing.B) {
	pcm := clickTrack(128, 30)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		ComputeSpectrogram(pcm, windowLen, hopSize)
	}
}

func BenchmarkAnalyzeBeatGrid(b *testing.B) {
	pcm := clickTrack(128, 30)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		AnalyzeBeatGrid(pcm)
	}
}

func BenchmarkAnalyzeKey(b *testing.B) {
	pcm := clickTrack(128, 30)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		AnalyzeKey(pcm)
	}
}
