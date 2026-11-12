package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"
)

// TestTheStreamedMediaIsPlayed checks that a stream that never ends is
// played.
//
// A radio station is a stream that never ends, and Home Assistant's
// proxy hands it over as raw samples or as FLAC. Both used to be read
// whole before being played, and a stream that never ends is never read
// whole: the panel sat there saying it was playing and playing nothing,
// and nothing said so.
//
// So the audio has to be read as it arrives.
func TestTheStreamedMediaIsPlayed(t *testing.T) {
	// A stream of raw samples that never ends.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "audio/wav")
		w.WriteHeader(http.StatusOK)
		chunk := make([]byte, 32000)
		for i := range chunk {
			chunk[i] = byte(i)
		}
		for {
			if _, err := w.Write(chunk); err != nil {
				return
			}
			if flusher, ok := w.(http.Flusher); ok {
				flusher.Flush()
			}
			time.Sleep(10 * time.Millisecond)
		}
	}))
	// The server is closed without waiting.
	//
	// The stream never ends, so the server would wait for the connection
	// to close for as long as the test ran.
	defer func() {
		server.CloseClientConnections()
		server.Close()
	}()

	out := t.TempDir() + "/speaker.wav"
	backend := newFileBackend("", out)
	speaker := &deviceAudio{backend: backend, log: testLogger()}
	speaker.volume = 100

	go speaker.playMediaURL(context.Background(), server.URL, false)

	// The speaker is playing within a few seconds.
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if info, err := os.Stat(out); err == nil && info.Size() > 100000 {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	info, _ := os.Stat(out)
	size := int64(0)
	if info != nil {
		size = info.Size()
	}
	t.Fatalf("the speaker only has %d bytes of the stream", size)
}

// TestTheStreamedFLACIsPlayed checks that a FLAC stream is played as it
// arrives.
//
// The proxy hands FLAC over when it has a FLAC file to give, and a FLAC
// stream is read a frame at a time.
func TestTheStreamedFLACIsPlayed(t *testing.T) {
	flacFile := os.Getenv("TEST_FLAC")
	if flacFile == "" {
		t.Skip("no FLAC file was named")
	}

	body, err := os.ReadFile(flacFile)
	if err != nil {
		t.Fatal(err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "audio/flac")
		w.WriteHeader(http.StatusOK)
		// The stream never ends: the file is sent again and again.
		for {
			if _, err := w.Write(body); err != nil {
				return
			}
			if flusher, ok := w.(http.Flusher); ok {
				flusher.Flush()
			}
			time.Sleep(10 * time.Millisecond)
		}
	}))
	// The server is closed without waiting.
	//
	// The stream never ends, so the server would wait for the connection
	// to close for as long as the test ran.
	defer func() {
		server.CloseClientConnections()
		server.Close()
	}()

	out := t.TempDir() + "/speaker.wav"
	backend := newFileBackend("", out)
	speaker := &deviceAudio{backend: backend, log: testLogger()}
	speaker.volume = 100

	go speaker.playMediaURL(context.Background(), server.URL, false)

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if info, err := os.Stat(out); err == nil && info.Size() > 50000 {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	info, _ := os.Stat(out)
	size := int64(0)
	if info != nil {
		size = info.Size()
	}
	t.Fatalf("the speaker only has %d bytes of the FLAC stream", size)
}

// The reader is closed when the stream is closed.
var _ io.ReadCloser = (*mediaStream)(nil)
