package main

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/vithurshanselvarajah/Home-Panel-Kiosk/audio"
)

// recordingWriter keeps what was written to the speaker.
type recordingWriter struct {
	mu      sync.Mutex
	written []byte
}

func (w *recordingWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	w.written = append(w.written, p...)
	w.mu.Unlock()
	return len(p), nil
}

func (w *recordingWriter) Close() error { return nil }

// TestTheMusicHasNoGaps checks that the music the speaker is given has
// no gaps in it.
//
// The proxy streams the audio as ffmpeg makes it, which is slower than
// the speaker plays it. The fetch has to stay ahead of the speaker, or
// the music arrives in bursts with silence between them, and that is a
// tick rather than music.
//
// This is the bug that made the music from Home Assistant a tick: the
// speaker was opened on the first chunk the network delivered and it
// then took the music as fast as the network could give it, which is
// slower than the speaker plays it.
func TestTheMusicHasNoGaps(t *testing.T) {
	// A second of music.
	pcm := make([]byte, 48000*8)
	for i := 0; i < len(pcm)/2; i++ {
		pcm[i*2] = 0x40
		pcm[i*2+1] = 0x00
	}
	file := buildStreamingWAV(pcm)

	// The server hands the audio over slowly, as the proxy does.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "audio/wav")
		w.Write(file[:12])
		w.(http.Flusher).Flush()
		for offset := 12; offset < len(file); offset += 512 {
			end := offset + 4096
			if end > len(file) {
				end = len(file)
			}
			w.Write(file[offset:end])
			w.(http.Flusher).Flush()
			// The network hands the audio over in bursts.
			time.Sleep(20 * time.Millisecond)
		}
	}))
	defer server.Close()

	writer := &recordingWriter{}
	backend := &fakeRecordingBackend{writer: writer}
	audio := &deviceAudio{backend: backend, log: testLogger()}
	audio.volume = 100

	go audio.playMediaURL(context.Background(), server.URL, false)
	// Watch the queue depth while it plays.
	for i := 0; i < 30; i++ {
		time.Sleep(100 * time.Millisecond)
		audio.mu.Lock()
		t.Logf("queue %d bytes", len(audio.media))
		audio.mu.Unlock()
	}

	writer.mu.Lock()
	written := len(writer.written)
	writer.mu.Unlock()

	// The speaker takes a period every ten milliseconds. A second of
	// music is a hundred periods, and every one of them has to have been
	// written: a gap is a period of silence where there should be music.
	t.Logf("%d bytes written to the speaker", written)
	if written == 0 {
		t.Fatal("nothing was written to the speaker at all")
	}
	// The music is a steady value, so a gap shows up as a run of zeros.
	writer.mu.Lock()
	zeros := 0
	longest := 0
	run := 0
	for i := 0; i+1 < len(writer.written); i += 2 {
		if writer.written[i] == 0 && writer.written[i+1] == 0 {
			zeros++
			run++
			if run > longest {
				longest = run
			}
		} else {
			run = 0
		}
	}
	writer.mu.Unlock()
	t.Logf("%d zero samples, the longest run %d", zeros, longest)
	// Where is the gap?
	writer.mu.Lock()
	first := -1
	for i := 0; i+1 < len(writer.written); i += 2 {
		if writer.written[i] == 0 && writer.written[i+1] == 0 {
			first = i
			break
		}
	}
	writer.mu.Unlock()
	t.Logf("the first gap is at byte %d of %d", first, written)
	if longest > 200 {
		t.Errorf("there is a gap of %d samples in the music", longest)
	}
}

// fakeRecordingBackend is a sound card that writes down what it is given.
type fakeRecordingBackend struct {
	writer *recordingWriter
}

func (b *fakeRecordingBackend) Capture(ctx context.Context, rate int) (io.ReadCloser, error) {
	return &fakeReader{backend: &fakeBackend{}, done: make(chan struct{})}, nil
}

func (b *fakeRecordingBackend) Playback(ctx context.Context, rate int) (io.WriteCloser, error) {
	return b.writer, nil
}

func (b *fakeRecordingBackend) OpenClockedOutput(ctx context.Context, rate, channels int) (audio.ClockedOutput, error) {
	return nil, errors.New("no clocked output")
}

func (b *fakeRecordingBackend) SelectedAudioDevices() (string, string) { return "", "" }

func (b *fakeRecordingBackend) SelectAudioDevices(input, output string) error { return nil }
