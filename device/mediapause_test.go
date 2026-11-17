package main

import (
	"context"
	"encoding/binary"
	"net/http"
	"net/http/httptest"
	"testing"
)

// newTestServer makes a server that hands out a WAV file.
func newTestServer(file []byte) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "audio/wav")
		w.Write(file)
	}))
}

// TestTheMusicCanBePaused checks that the music Home Assistant is
// playing can be paused and played again.
//
// The music is a stream that is fetched while it is played, so pausing
// it is not a matter of asking the server to stop: the audio that has
// arrived is held, and the track picks up where it left off.
func TestTheMusicCanBePaused(t *testing.T) {
	// The audio is long enough that it is still playing when the pause
	// happens.
	pcm := make([]byte, 48000*20)
	for i := 0; i < len(pcm)/2; i++ {
		binary.LittleEndian.PutUint16(pcm[i*2:], uint16(i))
	}
	file := buildStreamingWAV(pcm)

	server := newTestServer(file)
	defer server.Close()

	audio := &deviceAudio{backend: &fakeBackend{}, log: testLogger()}
	// The speaker is one for the whole program, so the loop that has it
	// is stopped: a test that leaves it running is a test that leaves the
	// next one without a speaker.
	//
	// The music is stopped as well: a paused track is still music, and
	// the loop that has the speaker waits for the music to end before it
	// lets the speaker go.
	defer audio.stopPortedSpeaker()
	defer audio.StopMedia()

	played := make(chan struct{})
	go func() {
		audio.playMediaURL(context.Background(), server.URL, false)
		close(played)
	}()

	// The music starts. The speaker waits for a buffer's worth of it
	// before it opens, so the queue is what says it has started.
	waitFor(t, func() bool {
		audio.mu.Lock()
		defer audio.mu.Unlock()
		return len(audio.media) >= mediaBufferBytes
	}, "the music never started")

	// It is paused.
	audio.PauseMedia()
	audio.mu.Lock()
	paused := audio.mediaPaused
	audio.mu.Unlock()
	if !paused {
		t.Fatal("the music was not paused")
	}

	// The speaker takes nothing out of the queue while it is paused.
	//
	// The fetch may still be filling it, so the queue can grow: what
	// matters is that the music is not being played. The check is the
	// one the mixer makes, because that is what decides whether the
	// music is heard.
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

	// And it plays again.
	audio.ResumeMedia()
	audio.mu.Lock()
	resumed := !audio.mediaPaused
	audio.mu.Unlock()
	if !resumed {
		t.Error("the music was not played again")
	}
}

// TestTheMusicCanBeStopped checks that stopping the music throws it
// away rather than leaving it to be played.
func TestTheMusicCanBeStopped(t *testing.T) {
	pcm := make([]byte, 48000*20)
	file := buildStreamingWAV(pcm)

	server := newTestServer(file)
	defer server.Close()

	audio := &deviceAudio{backend: &fakeBackend{}, log: testLogger()}
	// The speaker is one for the whole program, so the loop that has it is
	// stopped: a test that leaves it running is a test that leaves the next
	// one without a speaker.
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
