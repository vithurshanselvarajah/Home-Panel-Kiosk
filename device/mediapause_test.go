package main

import (
	"context"
	"encoding/binary"
	"net/http"
	"net/http/httptest"
	"testing"
)

func newTestServer(file []byte) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "audio/wav")
		w.Write(file)
	}))
}

func TestTheMusicCanBePaused(t *testing.T) {

	pcm := make([]byte, 48000*20)
	for i := 0; i < len(pcm)/2; i++ {
		binary.LittleEndian.PutUint16(pcm[i*2:], uint16(i))
	}
	file := buildStreamingWAV(pcm)

	server := newTestServer(file)
	defer server.Close()

	audio := &deviceAudio{backend: &fakeBackend{}, log: testLogger()}

	defer audio.stopPortedSpeaker()
	defer audio.StopMedia()

	played := make(chan struct{})
	go func() {
		audio.playMediaURL(context.Background(), server.URL, false)
		close(played)
	}()

	waitFor(t, func() bool {
		audio.mu.Lock()
		defer audio.mu.Unlock()
		return len(audio.media) >= mediaBufferBytes
	}, "the music never started")

	audio.PauseMedia()
	audio.mu.Lock()
	paused := audio.mediaPaused
	audio.mu.Unlock()
	if !paused {
		t.Fatal("the music was not paused")
	}

	taken := 0
	for period := 0; period < 5; period++ {
		audio.mu.Lock()
		var chunk []byte
		if !audio.mediaPaused {
			chunk = audio.takeAudio(&audio.media, periodBytes)
		}
		audio.mu.Unlock()
		taken += len(chunk)
	}
	if taken != 0 {
		t.Errorf("%d bytes were taken out of the queue while it was paused", taken)
	}

	audio.ResumeMedia()
	audio.mu.Lock()
	resumed := !audio.mediaPaused
	audio.mu.Unlock()
	if !resumed {
		t.Error("the music was not played again")
	}
}

func TestTheMusicCanBeStopped(t *testing.T) {
	pcm := make([]byte, 48000*20)
	file := buildStreamingWAV(pcm)

	server := newTestServer(file)
	defer server.Close()

	audio := &deviceAudio{backend: &fakeBackend{}, log: testLogger()}

	defer audio.stopPortedSpeaker()
	go audio.playMediaURL(context.Background(), server.URL, false)

	waitFor(t, func() bool {
		audio.mu.Lock()
		defer audio.mu.Unlock()
		return len(audio.media) > 0
	}, "the music never started")

	audio.StopMedia()
	audio.mu.Lock()
	active := audio.mediaActive
	queued := len(audio.media)
	audio.mu.Unlock()
	if active {
		t.Error("the music was still active after it was stopped")
	}
	if queued != 0 {
		t.Errorf("%d bytes were left in the queue after the music was stopped", queued)
	}
}
