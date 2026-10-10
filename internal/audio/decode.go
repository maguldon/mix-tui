// Package audio decodes audio files into the single PCM format the engine
// consumes: interleaved stereo float32 at dsp.SampleRate.
//
// Every decoder normalises on the way in, so nothing downstream of Decode ever
// asks what rate or channel count a file happened to arrive as.
package audio

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"time"

	"github.com/hajimehoshi/go-mp3"
	"github.com/jfreymuth/oggvorbis"
	"github.com/mewkiz/flac"
	"github.com/mewkiz/flac/frame"

	"github.com/maguldon/mix-tui/internal/dsp"
)

// ErrUnsupported is returned for a file whose format has no decoder, or whose
// contents do not match any format we recognise.
var ErrUnsupported = errors.New("unsupported audio format")

// ErrCorrupt is returned when a file identifies as a supported format but
// cannot be decoded.
var ErrCorrupt = errors.New("corrupt audio data")

// Track is fully decoded audio plus what metadata the file carried.
//
// PCM is always interleaved stereo float32 at dsp.SampleRate regardless of how
// the file arrived, so the rest of the engine never branches on sample rate or
// channel count.
type Track struct {
	Path       string
	Title      string
	Artist     string
	Duration   time.Duration
	SampleRate int
	Channels   int
	PCM        dsp.Stereo
}

// format is the detected container/codec.
type format int

const (
	formatUnknown format = iota
	formatWAV
	formatMP3
	formatFLAC
	formatOGG
)

func (f format) String() string {
	switch f {
	case formatWAV:
		return "wav"
	case formatMP3:
		return "mp3"
	case formatFLAC:
		return "flac"
	case formatOGG:
		return "ogg"
	}
	return "unknown"
}

// detect sniffs the container from magic bytes.
//
// Content, not extension: a FLAC named .mp3 is common after a bad download, and
// decoding it as MP3 produces noise rather than an error.
func detect(head []byte) format {
	switch {
	case len(head) >= 12 && string(head[0:4]) == "RIFF" && string(head[8:12]) == "WAVE":
		return formatWAV
	case len(head) >= 4 && string(head[0:4]) == "fLaC":
		return formatFLAC
	case len(head) >= 4 && string(head[0:4]) == "OggS":
		return formatOGG
	case len(head) >= 3 && string(head[0:3]) == "ID3":
		return formatMP3
	case len(head) >= 2 && head[0] == 0xFF && head[1]&0xE0 == 0xE0:
		// MPEG audio frame sync.
		return formatMP3
	}
	return formatUnknown
}

// Decode reads and decodes an audio file.
//
// It never returns partial audio with a nil error: a file that fails halfway
// through is an error, not a short track, because a half-decoded track plays
// as a sudden dropout mid-set.
func Decode(path string) (*Track, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	head := make([]byte, 16)
	n, err := io.ReadFull(f, head)
	if err != nil && n < 4 {
		return nil, fmt.Errorf("%s: %w", path, ErrUnsupported)
	}
	head = head[:n]

	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}

	switch detect(head) {
	case formatWAV:
		return decodeWAV(f, path)
	case formatMP3:
		return decodeMP3(f, path)
	case formatFLAC:
		return decodeFLAC(f, path)
	case formatOGG:
		return decodeOGG(f, path)
	}
	return nil, fmt.Errorf("%s: %w", path, ErrUnsupported)
}

// Probe reads only the header, returning duration and sample rate without
// decoding the audio. The library scanner uses this so listing a few thousand
// files does not decode every one of them.
func Probe(path string) (time.Duration, int, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, 0, err
	}
	defer f.Close()

	head := make([]byte, 16)
	n, err := io.ReadFull(f, head)
	if err != nil && n < 4 {
		return 0, 0, fmt.Errorf("%s: %w", path, ErrUnsupported)
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return 0, 0, err
	}

	switch detect(head) {
	case formatWAV:
		return probeWAV(f)
	case formatMP3:
		return probeMP3(f)
	case formatFLAC:
		return probeFLAC(f)
	case formatOGG:
		return probeOGG(f)
	}
	return 0, 0, fmt.Errorf("%s: %w", path, ErrUnsupported)
}

// normalise converts any channel count and rate into interleaved stereo
// float32 at dsp.SampleRate.
//
// planar holds interleaved-by-the-source data; mono sources are duplicated and
// rate conversion is linear, which is acceptable here because this is the
// offline load path, not the realtime thread.
func normalise(planar [][]float32, srcRate int) (dsp.Stereo, time.Duration) {
	if len(planar) == 0 {
		return nil, 0
	}
	chans := len(planar)
	frames := len(planar[0])
	for _, c := range planar {
		if len(c) < frames {
			frames = len(c)
		}
	}
	if frames == 0 {
		return nil, 0
	}
	if srcRate <= 0 {
		srcRate = dsp.SampleRate
	}

	// Mono: duplicate so downstream never sees a single channel.
	if chans == 1 {
		out := make(dsp.Stereo, 0, frames*2)
		for i := 0; i < frames; i++ {
			v := planar[0][i]
			out = append(out, v, v)
		}
		return resample(out, frames, srcRate, frames), durationOf(frames, srcRate)
	}
	if chans > 2 {
		// Fold extra channels in rather than dropping them: a 5.1 file
		// should still play, and the extra channels are usually surround.
		planar = planar[:2]
		chans = 2
	}

	inter := make(dsp.Stereo, frames*2)
	for i := 0; i < frames; i++ {
		inter[i*2] = planar[0][i]
		inter[i*2+1] = planar[1][i]
	}
	out := resample(inter, frames, srcRate, frames)
	return out, durationOf(frames, srcRate)
}

func durationOf(frames, rate int) time.Duration {
	if rate <= 0 {
		return 0
	}
	return time.Duration(float64(frames) / float64(rate) * float64(time.Second))
}

// resample rate-converts an interleaved stereo buffer. Only the load path uses
// this; the realtime path uses dsp.Resampler, which is far more accurate and
// which can prime itself for seamless seeks.
func resample(in dsp.Stereo, frames, srcRate, dstFrames int) dsp.Stereo {
	if srcRate == dsp.SampleRate || frames == 0 {
		return in
	}
	out := make(dsp.Stereo, dstFrames*2)
	step := float64(frames) / float64(dstFrames)
	for i := 0; i < dstFrames; i++ {
		src := float64(i) * step
		i0 := int(src)
		if i0 >= frames-1 {
			i0 = frames - 1
			if i0 < 0 {
				i0 = 0
			}
		}
		frac := float32(src - float64(i0))
		i1 := i0 + 1
		if i1 >= frames {
			i1 = i0
		}
		for c := 0; c < 2; c++ {
			a := in[i0*2+c]
			b := in[i1*2+c]
			out[i*2+c] = a + (b-a)*frac
		}
	}
	return out
}

// clampSample keeps a decoded sample finite and in range. A malformed file can
// otherwise inject NaN into the PCM, which then propagates through every
// filter and poisons the master bus permanently.
func clampSample(v float32) float32 {
	if math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) {
		return 0
	}
	if v > 1 {
		return 1
	}
	if v < -1 {
		return -1
	}
	return v
}

func clampAll(s dsp.Stereo) {
	for i := range s {
		s[i] = clampSample(s[i])
	}
}

// ---- WAV ----

func decodeWAV(r io.Reader, path string) (*Track, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	chans, rate, _, ok := parseWAV(data)
	if !ok {
		return nil, fmt.Errorf("%s: %w", path, ErrCorrupt)
	}
	pcm, dur := normalise(chans, rate)
	clampAll(pcm)
	t := &Track{Path: path, Duration: dur, SampleRate: dsp.SampleRate,
		Channels: 2, PCM: pcm}
	t.Title, t.Artist = wavMetadata(data)
	return t, nil
}

func probeWAV(r io.Reader) (time.Duration, int, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return 0, 0, err
	}
	_, rate, frames, ok := parseWAV(data)
	if !ok {
		return 0, 0, ErrCorrupt
	}
	return durationOf(frames, rate), rate, nil
}

// parseWAV handles PCM integer and IEEE-float WAV, which is everything the
// fixtures and any normal encoder produce. It returns per-channel slices.
func parseWAV(data []byte) (planar [][]float32, rate, frames int, ok bool) {
	if len(data) < 12 || string(data[0:4]) != "RIFF" || string(data[8:12]) != "WAVE" {
		return nil, 0, 0, false
	}
	pos := 12
	var bitsPerSample, blockAlign uint16
	var audioFmt uint16
	var raw []byte
	for pos+8 <= len(data) {
		id := string(data[pos : pos+4])
		size := int(binary.LittleEndian.Uint32(data[pos+4 : pos+8]))
		body := pos + 8
		if size < 0 || body+size > len(data) {
			// Truncated final chunk: use what is there.
			size = len(data) - body
		}
		switch id {
		case "fmt ":
			if body+16 > len(data) {
				return nil, 0, 0, false
			}
			audioFmt = binary.LittleEndian.Uint16(data[body : body+2])
			ch := int(binary.LittleEndian.Uint16(data[body+2 : body+4]))
			rate = int(binary.LittleEndian.Uint32(data[body+4 : body+8]))
			blockAlign = binary.LittleEndian.Uint16(data[body+12 : body+14])
			bitsPerSample = binary.LittleEndian.Uint16(data[body+14 : body+16])
			if ch < 1 {
				return nil, 0, 0, false
			}
		case "data":
			raw = data[body : body+size]
		}
		pos = body + size
		if size%2 == 1 {
			pos++ // chunks are word-aligned
		}
	}
	if raw == nil || rate == 0 || blockAlign == 0 {
		return nil, 0, 0, false
	}
	ch := int(blockAlign) * 8 / int(bitsPerSample)
	if ch < 1 {
		return nil, 0, 0, false
	}
	frames = len(raw) / int(blockAlign)

	out := make([][]float32, ch)
	for c := range out {
		out[c] = make([]float32, frames)
	}
	for i := 0; i < frames; i++ {
		base := i * int(blockAlign)
		for c := 0; c < ch; c++ {
			off := base + c*int(bitsPerSample)/8
			if off+int(bitsPerSample)/8 > len(raw) {
				break
			}
			out[c][i] = wavSample(raw[off:], audioFmt, bitsPerSample)
		}
	}
	return out, rate, frames, true
}

func wavSample(b []byte, audioFmt, bits uint16) float32 {
	switch {
	case audioFmt == 3 && bits == 32: // IEEE float
		return math.Float32frombits(binary.LittleEndian.Uint32(b))
	case audioFmt == 3 && bits == 64:
		return float32(math.Float64frombits(binary.LittleEndian.Uint64(b)))
	case bits == 8: // unsigned
		return (float32(b[0]) - 128) / 128
	case bits == 16:
		return float32(int16(binary.LittleEndian.Uint16(b))) / 32768
	case bits == 24:
		v := int32(b[0]) | int32(b[1])<<8 | int32(b[2])<<16
		if v&0x800000 != 0 {
			v |= ^0xFFFFFF
		}
		return float32(v) / (1 << 23)
	case bits == 32:
		return float32(int32(binary.LittleEndian.Uint32(b))) / (1 << 31)
	}
	return 0
}

// wavMetadata pulls TITLE and ARTIST from any LIST/INFO chunk.
func wavMetadata(data []byte) (title, artist string) {
	pos := 12
	for pos+8 <= len(data) {
		id := string(data[pos : pos+4])
		size := int(binary.LittleEndian.Uint32(data[pos+4 : pos+8]))
		body := pos + 8
		if size < 0 || body+size > len(data) {
			return
		}
		if id == "LIST" && size >= 4 && string(data[body:body+4]) == "INFO" {
			p := body + 4
			for p+8 <= body+size {
				sid := string(data[p : p+4])
				sz := int(binary.LittleEndian.Uint32(data[p+4 : p+8]))
				val := data[p+8 : p+8+sz]
				if len(val) > 0 {
					if n := trimNul(val); n != "" {
						switch sid {
						case "INAM":
							title = n
						case "IART":
							artist = n
						}
					}
				}
				p += 8 + sz
				if sz%2 == 1 {
					p++
				}
			}
		}
		pos = body + size
		if size%2 == 1 {
			pos++
		}
	}
	return
}

func trimNul(b []byte) string {
	for i, c := range b {
		if c == 0 {
			return string(b[:i])
		}
	}
	return string(b)
}

// ---- MP3 ----

func decodeMP3(r io.Reader, path string) (*Track, error) {
	dec, err := mp3.NewDecoder(r)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, ErrCorrupt)
	}
	// go-mp3 outputs stereo interleaved int16; there is no channel accessor.
	rate := dec.SampleRate()
	planar, err := readPlanar(dec, rate, 2)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, ErrCorrupt)
	}
	pcm, dur := normalise(planar, rate)
	clampAll(pcm)
	return &Track{Path: path, Duration: dur, SampleRate: dsp.SampleRate,
		Channels: 2, PCM: pcm}, nil
}

func probeMP3(r io.Reader) (time.Duration, int, error) {
	dec, err := mp3.NewDecoder(r)
	if err != nil {
		return 0, 0, ErrCorrupt
	}
	rate := dec.SampleRate()
	return durationOf(int(dec.Length()), rate), rate, nil
}

func readPlanar(dec io.Reader, rate, chans int) ([][]float32, error) {
	if chans < 1 {
		chans = 2
	}
	// go-mp3 hands back stereo interleaved int16 through a plain io.Reader
	// with no alignment guarantee across calls. Reading everything first and
	// converting afterwards avoids stitching logic entirely and removes a
	// whole class of off-by-one channel bugs.
	raw, err := io.ReadAll(dec)
	if err != nil {
		return nil, err
	}
	planar := make([][]float32, chans)
	for i := 0; i+3 < len(raw); i += 4 {
		planar[0] = append(planar[0], float32(int16(binary.LittleEndian.Uint16(raw[i:])))/32768)
		planar[1] = append(planar[1], float32(int16(binary.LittleEndian.Uint16(raw[i+2:])))/32768)
	}
	if len(planar[0]) == 0 {
		return nil, ErrCorrupt
	}
	return planar, nil
}

// ---- FLAC ----

func decodeFLAC(r io.Reader, path string) (*Track, error) {
	stream, err := flac.New(r)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, ErrCorrupt)
	}
	info := stream.Info
	rate := int(info.SampleRate)
	chans := int(info.NChannels)
	planar := make([][]float32, chans)
	for {
		f, err := stream.ParseNext()
		if err != nil {
			break // io.EOF or a decode failure past what we have
		}
		for c := range f.Subframes {
			sub := f.Subframes[c]
			if c >= chans {
				break
			}
			planar[c] = append(planar[c], flacSamples(sub, info.BitsPerSample)...)
		}
	}
	if len(planar) == 0 || len(planar[0]) == 0 {
		return nil, fmt.Errorf("%s: %w", path, ErrCorrupt)
	}
	pcm, dur := normalise(planar, rate)
	clampAll(pcm)
	return &Track{Path: path, Duration: dur, SampleRate: dsp.SampleRate,
		Channels: 2, PCM: pcm}, nil
}

// flacSamples converts a decoded subframe to normalised float32.
//
// The library restores samples to the stream's own bit depth (it left-shifts
// back by the subframe's wasted-bits count, which is zero for a normal 16-bit
// stream), so the divisor must be 2^bps -- not 2^31, which would divide a
// 16-bit signal down by 32768 and decode it as silence.
func flacSamples(s *frame.Subframe, bps uint8) []float32 {
	n := s.NSamples
	out := make([]float32, n)
	// Signed PCM of bps bits spans [-2^(bps-1), 2^(bps-1)), so the divisor is
	// 2^(bps-1). Verified against a fixture whose decoded peak must match the
	// source WAV exactly.
	shift := int(bps) - 1
	if shift < 0 {
		shift = 0
	}
	div := float32(int64(1) << uint(shift))
	for i := 0; i < n; i++ {
		if i >= len(s.Samples) {
			break
		}
		out[i] = clampSample(float32(s.Samples[i]) / div)
	}
	return out
}

func probeFLAC(r io.Reader) (time.Duration, int, error) {
	stream, err := flac.New(r)
	if err != nil {
		return 0, 0, ErrCorrupt
	}
	info := stream.Info
	total := 0
	for {
		f, err := stream.ParseNext()
		if err != nil {
			break
		}
		total += int(f.BlockSize)
	}
	return durationOf(total, int(info.SampleRate)), int(info.SampleRate), nil
}

// ---- OGG Vorbis ----

func decodeOGG(r io.Reader, path string) (*Track, error) {
	dec, err := oggvorbis.NewReader(r)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, ErrCorrupt)
	}
	rate := dec.SampleRate()
	chans := dec.Channels()
	if rate == 0 || chans == 0 {
		return nil, fmt.Errorf("%s: %w", path, ErrCorrupt)
	}
	if chans < 1 {
		chans = 2
	}
	planar := make([][]float32, chans)
	block := make([]float32, 4096*chans)
	for {
		n, err := dec.Read(block)
		if n > 0 {
			for c := 0; c < chans; c++ {
				for i := 0; i < n/chans; i++ {
					planar[c] = append(planar[c], block[i*chans+c])
				}
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, ErrCorrupt)
		}
	}
	if len(planar[0]) == 0 {
		return nil, fmt.Errorf("%s: %w", path, ErrCorrupt)
	}
	pcm, dur := normalise(planar, rate)
	clampAll(pcm)
	return &Track{Path: path, Duration: dur, SampleRate: dsp.SampleRate,
		Channels: 2, PCM: pcm}, nil
}

func probeOGG(r io.Reader) (time.Duration, int, error) {
	dec, err := oggvorbis.NewReader(r)
	if err != nil {
		return 0, 0, ErrCorrupt
	}
	length := int(dec.Length())
	return durationOf(length, dec.SampleRate()), dec.SampleRate(), nil
}
