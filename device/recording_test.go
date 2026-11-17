package main

import (
	"context"
	"io"
	"os"
	"testing"
	"time"

	"github.com/vithurshanselvarajah/Home-Panel-Kiosk/audio"
)

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

type recordingReader struct {
	backend *recordingBackend
}

func (r *recordingReader) Read(p []byte) (int, error) {
	if r.backend.offset >= len(r.backend.recording) {

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

	sent := make(chan []byte, 64)
	speaker.onSendMicAudio = func(pcm []byte) {
		sent <- append([]byte(nil), pcm...)
	}

	speaker.setListening(true)

	speaker.setStreaming(true)

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

	for i := range pcm {
		if got[i] != pcm[i] {
			t.Fatalf("byte %d was %d, it should be %d", i, got[i], pcm[i])
		}
	}
	t.Logf("%d bytes were sent to Home Assistant", len(got))
}
