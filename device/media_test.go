package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// wavFile builds a WAV file with a header and some audio.
//
// The proxy hands over a WAV file, header and all, because that is what
// ffmpeg writes. The header has to be stripped, or the speaker plays it
// as audio.
func wavFile(rate, channels int, pcm []byte) []byte {
	var buf bytes.Buffer

	buf.WriteString("RIFF")
	// The size is the file's size less the eight bytes of the header.
	binary.Write(&buf, binary.LittleEndian, uint32(36+len(pcm)))
	buf.WriteString("WAVE")

	buf.WriteString("fmt ")
	binary.Write(&buf, binary.LittleEndian, uint32(16))
	binary.Write(&buf, binary.LittleEndian, uint16(1)) // PCM
	binary.Write(&buf, binary.LittleEndian, uint16(channels))
	binary.Write(&buf, binary.LittleEndian, uint32(rate))
	binary.Write(&buf, binary.LittleEndian, uint32(rate*channels*2))
	binary.Write(&buf, binary.LittleEndian, uint16(channels*2))
	binary.Write(&buf, binary.LittleEndian, uint16(16))

	buf.WriteString("data")
	binary.Write(&buf, binary.LittleEndian, uint32(len(pcm)))
	buf.Write(pcm)

	return buf.Bytes()
}

// TestTheWaveHeaderIsStripped checks that the audio comes out of the
// file without the header.
//
// The proxy sends a WAV file and the speaker takes PCM, so the header
// has to go. A speaker that is handed the header plays forty-odd bytes
// of it as a click.
func TestTheWaveHeaderIsStripped(t *testing.T) {
	pcm := []byte{1, 2, 3, 4, 5, 6, 7, 8}
	file := wavFile(48000, 1, pcm)

	out, rate, err := decodeMedia(file)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(out, pcm) {
		t.Errorf("the audio was %v, want %v", out, pcm)
	}
	if rate != 48000 {
		t.Errorf("the rate was %d, want 48000", rate)
	}
}

// TestTheWaveChannelsAreMixed checks that a stereo file becomes the one
// channel the speaker takes.
//
// The proxy is asked for one channel, so this is not the common case,
// but a file in stereo would otherwise be played with the channels
// alternating.
func TestTheWaveChannelsAreMixed(t *testing.T) {
	// Two stereo frames: 1000 and 2000, then -1000 and -2000.
	pcm := []byte{
		0xe8, 0x03, 0xd0, 0x07,
		0x18, 0xfc, 0x30, 0xf8,
	}
	file := wavFile(48000, 2, pcm)

	out, _, err := decodeMedia(file)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 4 {
		t.Fatalf("the audio was %d bytes, want 4", len(out))
	}
	// The first frame averages to 1500.
	if got := int16(uint16(out[0]) | uint16(out[1])<<8); got != 1500 {
		t.Errorf("the first frame was %d, want 1500", got)
	}
	// The second averages to -1500.
	if got := int16(uint16(out[2]) | uint16(out[3])<<8); got != -1500 {
		t.Errorf("the second frame was %d, want -1500", got)
	}
}

// TestPlainAudioIsPlayedAsItIs checks that audio that is not a file is
// passed through.
//
// The proxy sends PCM when it has nothing to convert, and that is the
// common case.
func TestPlainAudioIsPlayedAsItIs(t *testing.T) {
	pcm := []byte{9, 8, 7, 6}

	out, rate, err := decodeMedia(pcm)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(out, pcm) {
		t.Errorf("the audio was %v, want %v", out, pcm)
	}
	if rate != playbackRate {
		t.Errorf("the rate was %d, want %d", rate, playbackRate)
	}
}

// TestTheMediaIsConvertedToTheSpeakersRate checks that audio that is not
// at the speaker's rate is converted.
//
// The proxy is asked for the speaker's rate, so this is not the common
// case either, but a file that arrives at another rate would otherwise
// be played at the wrong speed.
func TestTheMediaIsConvertedToTheSpeakersRate(t *testing.T) {
	// A second of audio at 16 kHz would be far too long, so a short
	// piece is used and only the conversion's effect is checked.
	pcm := make([]byte, 3200)
	for i := 0; i < 1600; i++ {
		pcm[i*2] = byte(i)
		pcm[i*2+1] = 0
	}

	out := resampleToPlayback(pcm, 16000)
	// 1600 samples at 16 kHz become 4800 at 48 kHz, which is 9600
	// bytes. The conversion holds the last sample back rather than
	// interpolating towards a sample that has not arrived, so the
	// count is within a sample or two of it.
	if len(out) < 9590 || len(out) > 9600 {
		t.Errorf("the converted audio was %d bytes, want about 9600", len(out))
	}
}

// buildStreamingWAV makes a WAV file with unknown length, like ffmpeg's.
func buildStreamingWAV(pcm []byte) []byte {
	var buf bytes.Buffer
	buf.WriteString("RIFF")
	binary.Write(&buf, binary.LittleEndian, uint32(0xFFFFFFFF))
	buf.WriteString("WAVE")
	buf.WriteString("fmt ")
	binary.Write(&buf, binary.LittleEndian, uint32(16))
	binary.Write(&buf, binary.LittleEndian, uint16(1))
	binary.Write(&buf, binary.LittleEndian, uint16(1))
	binary.Write(&buf, binary.LittleEndian, uint32(48000))
	binary.Write(&buf, binary.LittleEndian, uint32(96000))
	binary.Write(&buf, binary.LittleEndian, uint16(2))
	binary.Write(&buf, binary.LittleEndian, uint16(16))
	// A LIST chunk, like the encoder writes. The size covers the whole
	// chunk: the four letters that say what it is, the four that name
	// the field, the size of the field and the field itself.
	buf.WriteString("LIST")
	binary.Write(&buf, binary.LittleEndian, uint32(26))
	buf.WriteString("INFOISFT")
	binary.Write(&buf, binary.LittleEndian, uint32(14))
	buf.WriteString("Lavf62.12.103")
	buf.WriteByte(0)
	buf.WriteString("data")
	binary.Write(&buf, binary.LittleEndian, uint32(0xFFFFFFFF))
	buf.Write(pcm)
	return buf.Bytes()
}

func TestTheStreamingWavIsRead(t *testing.T) {
	pcm := make([]byte, 400)
	for i := 0; i < 200; i++ {
		pcm[i*2] = byte(i)
		pcm[i*2+1] = 0
	}

	// The RIFF header is read first, the way openMedia reads it.
	file := buildStreamingWAV(pcm)
	head := make([]byte, 12)
	if _, err := io.ReadFull(bytes.NewReader(file), head); err != nil {
		t.Fatal(err)
	}

	stream, rate, err := newWAVStream(bytes.NewReader(file[12:]))
	if err != nil {
		t.Fatal(err)
	}
	if rate != 48000 {
		t.Errorf("the rate was %d, want 48000", rate)
	}

	got, err := io.ReadAll(stream)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(pcm) {
		t.Fatalf("read %d bytes, want %d", len(got), len(pcm))
	}
	for i := range got {
		if got[i] != pcm[i] {
			t.Fatalf("byte %d was %d, want %d", i, got[i], pcm[i])
		}
	}
}

// TestALongTrackIsPlayedAsItArrives checks that a track that takes a
// long time to arrive is played rather than refused.
//
// The proxy streams the audio as it converts it, and a slow network
// makes a track take minutes to arrive. A limit on the whole fetch would
// fail every one of them, so the limit is on the wait between chunks
// instead. The server here is slower than the limit would allow for the
// whole fetch, and the audio still arrives.
func TestALongTrackIsPlayedAsItArrives(t *testing.T) {
	pcm := make([]byte, 4000)
	for i := 0; i < 2000; i++ {
		pcm[i*2] = byte(i)
		pcm[i*2+1] = 0
	}
	file := buildStreamingWAV(pcm)

	// The server takes a moment between chunks, so the whole fetch takes
	// longer than the old thirty second limit would have allowed.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "audio/wav")
		w.Write(file[:12])
		w.(http.Flusher).Flush()
		for offset := 12; offset < len(file); offset += 400 {
			end := offset + 400
			if end > len(file) {
				end = len(file)
			}
			w.Write(file[offset:end])
			w.(http.Flusher).Flush()
			time.Sleep(2 * time.Millisecond)
		}
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	stream, rate, err := openMedia(ctx, server.URL)
	if err != nil {
		t.Fatalf("the audio could not be fetched: %v", err)
	}
	defer stream.Close()
	if rate != 48000 {
		t.Errorf("the rate was %d, want 48000", rate)
	}

	got, err := io.ReadAll(stream)
	if err != nil {
		t.Fatalf("the audio could not be read: %v", err)
	}
	if len(got) != len(pcm) {
		t.Fatalf("read %d bytes, want %d", len(got), len(pcm))
	}
	for i := range got {
		if got[i] != pcm[i] {
			t.Fatalf("byte %d was %d, want %d", i, got[i], pcm[i])
		}
	}
}

// TestTheFetchGivesUpOnAServerThatStops checks that a server that stops
// sending is given up on.
//
// The wait between chunks is bounded, so a server that goes quiet does
// not hold the player for ever.
func TestTheFetchGivesUpOnAServerThatStops(t *testing.T) {
	// The whole header is sent, up to and including the data chunk's own
	// header, and then nothing more comes.
	header := buildStreamingWAV(nil)[:78]
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "audio/wav")
		w.Write(header)
		w.(http.Flusher).Flush()
		// Then nothing more comes.
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	stream, _, err := openMedia(ctx, server.URL)
	if err != nil {
		t.Fatalf("the audio could not be fetched: %v", err)
	}
	defer stream.Close()

	// The read gives up rather than waiting for ever.
	done := make(chan error, 1)
	go func() {
		_, err := io.ReadAll(stream)
		done <- err
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("the read waited for ever on a server that had stopped")
	}
}
