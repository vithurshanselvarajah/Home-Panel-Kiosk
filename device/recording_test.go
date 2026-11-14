package main

import (
	"context"
	"io"
	"os"
	"testing"
	"time"

	"github.com/vithurshanselvarajah/Home-Panel-Kiosk/audio"
)

// recordingBackend is a sound card whose microphone is a file.
//
// It is how the recordings are played into the real pipeline: the device
// reads the microphone, and the microphone is the recording.
type recordingBackend struct {
	recording []byte
	offset    int
}

func (b *recordingBackend) Capture(ctx context.Context, rate int) (io.ReadCloser, error) {
	return &recordingReader{backend: b}, nil
}

func (b *recordingBackend) Playback(ctx context.Context, rate int) (io.WriteCloser, error) {
	return nopWriteCloser{}, nil
}

func (b *recordingBackend) OpenClockedOutput(ctx context.Context, rate, channels int) (audio.ClockedOutput, error) {
	return nil, errNoClocked{}
}

func (b *recordingBackend) SelectedAudioDevices() (string, string) { return "", "" }

func (b *recordingBackend) SelectAudioDevices(input, output string) error { return nil }

func (b *recordingBackend) ChooseDefaultInput() error { return nil }

type errNoClocked struct{}

func (errNoClocked) Error() string { return "no clocked output" }

// recordingReader plays the recording and then silence.
type recordingReader struct {
	backend *recordingBackend
}

func (r *recordingReader) Read(p []byte) (int, error) {
	if r.backend.offset >= len(r.backend.recording) {
		// Silence, so the device sees the end of the utterance.
		for i := range p {
			p[i] = 0
		}
		time.Sleep(20 * time.Millisecond)
		return len(p), nil
	}
	n := copy(p, r.backend.recording[r.backend.offset:])
	r.backend.offset += n
	return n, nil
}

func (r *recordingReader) Close() error { return nil }

// TestTheRecordingIsSentToHomeAssistant plays a recording into the
// device's microphone and checks what is sent to Home Assistant.
//
// The device is a voice satellite: the microphone goes to Home Assistant
// and Home Assistant sends back what it heard. A microphone that
// changed the audio on the way would change what Home Assistant hears,
// and the panel would answer the wrong thing.
//
// So the recording is played into the real microphone path and the audio
// that goes on the wire is compared with the recording, byte for byte.
//
// The recording is named by the RECORDING environment variable, because
// it is a large file that does not belong in the repository:
//
//	RECORDING=/tmp/rec/time.wav go test ./device/ -run TestTheRecording
func TestTheRecordingIsSentToHomeAssistant(t *testing.T) {
	path := os.Getenv("RECORDING")
	if path == "" {
		t.Skip("no recording was named")
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	pcm, err := wavSamples(raw)
	if err != nil {
		t.Fatal(err)
	}

	backend := &recordingBackend{recording: pcm}
	speaker := &deviceAudio{backend: backend, log: testLogger()}

	// What goes to Home Assistant.
	sent := make(chan []byte, 64)
	speaker.onSendMicAudio = func(pcm []byte) {
		sent <- append([]byte(nil), pcm...)
	}

	// The wake word is listening, so the microphone is open.
	speaker.setListening(true)
	// The turn is running, so the microphone goes to Home Assistant.
	//
	// The order matters: opening the microphone clears the streaming
	// flag, because a microphone that was just opened has no turn on it
	// yet.
	speaker.setStreaming(true)

	// The audio that goes on the wire.
	var got []byte
	deadline := time.After(5 * time.Second)
	for len(got) < len(pcm) {
		select {
		case chunk := <-sent:
			got = append(got, chunk...)
		case <-deadline:
			t.Fatalf("only %d of %d bytes were sent", len(got), len(pcm))
		}
	}

	// The audio that goes to Home Assistant is the recording, unchanged.
	for i := range pcm {
		if got[i] != pcm[i] {
			t.Fatalf("byte %d was %d, it should be %d", i, got[i], pcm[i])
		}
	}
	t.Logf("%d bytes were sent to Home Assistant", len(got))
}
