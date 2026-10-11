package audio

import (
	"encoding/binary"
	"math"
	"os"
)

// WAVWriter accumulates interleaved stereo float samples and writes a 16-bit
// PCM WAV on Close.
//
// It is NOT realtime safe: every method allocates or grows a buffer. It is used
// by the offline render path and by mixplay's -dump, both of which run off the
// audio thread.
type WAVWriter struct {
	sampleRate int
	buf        []byte
}

// NewWAVWriter returns a writer producing a WAV at the given sample rate.
func NewWAVWriter(sampleRate int) *WAVWriter {
	if sampleRate <= 0 {
		sampleRate = 44100
	}
	return &WAVWriter{sampleRate: sampleRate}
}

// WriteStereo appends one interleaved stereo block. Samples are clipped and
// quantised to 16-bit, which is what the offline path has always produced —
// keeping one encoder means mixrender and mixplay -dump are directly
// comparable byte for byte.
func (w *WAVWriter) WriteStereo(s []float32) {
	for i := 0; i+1 < len(s); i += 2 {
		w.buf = binary.LittleEndian.AppendUint16(w.buf, uint16(floatToPCM16(s[i])))
		w.buf = binary.LittleEndian.AppendUint16(w.buf, uint16(floatToPCM16(s[i+1])))
	}
}

func floatToPCM16(f float32) int16 {
	v := math.Round(float64(f) * 32767)
	if v > 32767 {
		v = 32767
	}
	if v < -32768 {
		v = -32768
	}
	return int16(v)
}

// Close writes the accumulated audio to path as a WAV file.
func (w *WAVWriter) Close(path string) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	dataLen := len(w.buf)
	hdr := make([]byte, 44)
	copy(hdr[0:4], "RIFF")
	binary.LittleEndian.PutUint32(hdr[4:8], uint32(36+dataLen))
	copy(hdr[8:12], "WAVE")
	copy(hdr[12:16], "fmt ")
	binary.LittleEndian.PutUint32(hdr[16:20], 16) // PCM header size
	binary.LittleEndian.PutUint16(hdr[20:22], 1)  // format 1 = PCM
	binary.LittleEndian.PutUint16(hdr[22:24], 2)  // channels
	binary.LittleEndian.PutUint32(hdr[24:28], uint32(w.sampleRate))
	byteRate := uint32(w.sampleRate * 2 * 2)
	binary.LittleEndian.PutUint32(hdr[28:32], byteRate)
	binary.LittleEndian.PutUint16(hdr[32:34], 4) // block align
	binary.LittleEndian.PutUint16(hdr[34:36], 16)
	copy(hdr[36:40], "data")
	binary.LittleEndian.PutUint32(hdr[40:44], uint32(dataLen))

	if _, err := f.Write(hdr); err != nil {
		return err
	}
	_, err = f.Write(w.buf)
	return err
}
