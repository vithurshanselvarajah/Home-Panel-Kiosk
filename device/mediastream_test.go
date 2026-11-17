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

func TestTheStreamedMediaIsPlayed(t *testing.T) {

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

	defer func() {
		server.CloseClientConnections()
		server.Close()
	}()

	out := t.TempDir() + "/speaker.wav"
	backend := newFileBackend("", out)
	speaker := &deviceAudio{backend: backend, log: testLogger()}
	speaker.volume = 100

	go speaker.playMediaURL(context.Background(), server.URL, false)

	defer speaker.stopPortedSpeaker()

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

	defer func() {
		server.CloseClientConnections()
		server.Close()
	}()

	out := t.TempDir() + "/speaker.wav"
	backend := newFileBackend("", out)
	speaker := &deviceAudio{backend: backend, log: testLogger()}
	speaker.volume = 100

	go speaker.playMediaURL(context.Background(), server.URL, false)

	defer speaker.stopPortedSpeaker()

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

var _ io.ReadCloser = (*mediaStream)(nil)
