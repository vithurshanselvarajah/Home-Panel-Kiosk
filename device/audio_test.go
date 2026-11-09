package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/wilbowes/EchoMuse-SDK/clients/go/echomuse"
)

// fakeBackend is a sound card that is not there.
//
// It refuses to open the microphone twice, exactly as a real card does,
// so a handover that is a race fails the test rather than passing it.
type fakeBackend struct {
	mu sync.Mutex
	// open is whether the microphone is open.
	open bool
	// opens counts the opens, so a test can tell a handover from a
	// reopen.
	opens int
}

func (b *fakeBackend) Capture(ctx context.Context, rate int) (io.ReadCloser, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.open {
		return nil, errors.New("the device is busy")
	}
	b.open = true
	b.opens++
	return &fakeReader{backend: b, done: make(chan struct{})}, nil
}

func (b *fakeBackend) Playback(ctx context.Context, rate int) (io.WriteCloser, error) {
	return &fakeWriter{}, nil
}

func (b *fakeBackend) OpenClockedOutput(ctx context.Context, rate, channels int) (echomuse.ClockedAudioOutput, error) {
	return nil, errors.New("no clocked output")
}

func (b *fakeBackend) SelectedAudioDevices() (string, string) { return "", "" }

func (b *fakeBackend) SelectAudioDevices(input, output string) error { return nil }

// isOpen says whether the microphone is open.
func (b *fakeBackend) isOpen() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.open
}

// opensCount is how many times the microphone has been opened.
func (b *fakeBackend) opensCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.opens
}

// fakeReader is a microphone that reads silence.
type fakeReader struct {
	backend *fakeBackend
	done    chan struct{}
	once    sync.Once
}

func (r *fakeReader) Read(p []byte) (int, error) {
	select {
	case <-r.done:
		return 0, io.EOF
	case <-time.After(100 * time.Millisecond):
	}
	// It blocks like a real one, for the length of a frame.
	return len(p), nil
}

func (r *fakeReader) Close() error {
	r.once.Do(func() {
		close(r.done)
		r.backend.mu.Lock()
		r.backend.open = false
		r.backend.mu.Unlock()
	})
	return nil
}

// fakeWriter is a speaker that swallows what it is given.
type fakeWriter struct{}

func (w *fakeWriter) Write(p []byte) (int, error) { return len(p), nil }
func (w *fakeWriter) Close() error                { return nil }

// testLogger is a logger that writes to the test's output.
func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}

// TestTheMonitorGivesUpTheMicrophone checks that the level meter lets go of
// the microphone when the controller asks for it.
//
// The sound card cannot be opened twice. The level meter opens it on its
// own so a person can see what the microphone hears while they are
// setting the device up, and the controller's capture opens it again when
// it wants to listen.
//
// The handover has to be a handover and not a race: a capture that starts
// while the meter still holds the card is told the card is busy, and the
// device goes deaf until the controller asks again.
func TestTheMonitorGivesUpTheMicrophone(t *testing.T) {
	backend := &fakeBackend{}
	a := &deviceAudio{backend: backend, log: testLogger()}

	// The meter opens the microphone.
	a.setMonitoring(true)
	waitFor(t, func() bool { return backend.isOpen() }, "the meter did not open the microphone")

	// The controller asks for it. This must not fail with the card busy.
	a.startCapture(false)
	defer a.stopCapture()

	// The capture opens the microphone on its own goroutine, so the wait
	// is for it to have the card.
	waitFor(t, func() bool { return backend.opensCount() >= 2 }, "the capture did not take the microphone")

	if !a.capturing {
		t.Fatal("the capture did not start")
	}
	if a.monitoringMic() {
		t.Error("the meter still holds the microphone after the capture took it")
	}
	// The microphone was opened twice: once for the meter and once for
	// the capture. The meter gave it up and the capture took it.
	if opens := backend.opensCount(); opens != 2 {
		t.Errorf("the microphone was opened %d times, want 2", opens)
	}
}

// TestTheMeterTakesTheMicrophoneBack checks that the meter opens the
// microphone when the controller is not using it.
//
// The meter is what a person looks at to see whether the microphone
// works, so it has to work on its own.
func TestTheMeterTakesTheMicrophoneBack(t *testing.T) {
	backend := &fakeBackend{}
	a := &deviceAudio{backend: backend, log: testLogger()}

	a.setMonitoring(true)
	waitFor(t, func() bool { return backend.isOpen() }, "the meter did not open the microphone")

	// The controller takes the microphone.
	a.startCapture(false)
	defer a.stopCapture()

	// The controller gives it back.
	a.stopCapture()
	a.setMonitoring(true)
	waitFor(t, func() bool { return backend.isOpen() }, "the meter did not take the microphone back")
}

// waitFor waits for a condition, up to a second.
func waitFor(t *testing.T, ok func() bool, message string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if ok() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal(message)
}
