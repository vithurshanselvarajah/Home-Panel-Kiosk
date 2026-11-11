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

	"github.com/vithurshanselvarajah/Home-Panel-Kiosk/audio"
	"github.com/vithurshanselvarajah/Home-Panel-Kiosk/device/state"
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

func (b *fakeBackend) OpenClockedOutput(ctx context.Context, rate, channels int) (audio.ClockedOutput, error) {
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
// panel goes deaf until the turn asks again.
func TestTheMonitorGivesUpTheMicrophone(t *testing.T) {
	backend := &fakeBackend{}
	a := &deviceAudio{backend: backend, log: testLogger()}

	// The meter opens the microphone.
	a.setMonitoring(true)
	waitFor(t, func() bool { return backend.isOpen() }, "the meter did not open the microphone")

	// A turn starts and asks for it. This must not fail with the card busy.
	a.startCapture()
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
	a.startCapture()
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

// TestTheSendspinGainFollowsTheVolume checks that the music is scaled by
// the panel's volume.
//
// It is the whole of the music's level, so a volume of zero is silence.
// The panel reported zero for a long time, because the volume was never
// copied into the reported state and Home Assistant sent back what it
// was told.
func TestTheSendspinGainFollowsTheVolume(t *testing.T) {
	full := []byte{0x00, 0x40, 0x00, 0x40} // 16384, 16384
	half := append([]byte(nil), full...)
	applySendspinGain(half, 50, false, false, 0)

	if half[0] != 0x00 || half[1] != 0x20 {
		t.Errorf("at half volume the sample was %d, want %d", int16(uint16(half[1])|uint16(half[0])<<8), 8192)
	}

	silent := append([]byte(nil), full...)
	applySendspinGain(silent, 0, false, false, 0)
	if silent[0] != 0x00 || silent[1] != 0x00 {
		t.Error("at zero volume the music was not silent")
	}
}

// TestTheMuteSilencesTheMusic checks that the mute switch is honoured.
//
// The mute is the panel's own, and it is what the volume is not: the
// volume is a level and the mute is a switch.
func TestTheMuteSilencesTheMusic(t *testing.T) {
	pcm := []byte{0x00, 0x40, 0x00, 0x40}
	applySendspinGain(pcm, 100, true, false, 0)

	for i := 0; i < len(pcm); i += 2 {
		if pcm[i] != 0x00 || pcm[i+1] != 0x00 {
			t.Fatal("the music was not silent when it was muted")
		}
	}
}

// TestTheReportedVolumeIsThePanelsVolume checks that the panel reports
// the volume it is actually using.
//
// The volume is pushed to Home Assistant as the speaker's state, and Home
// Assistant sends the volume back as a command. A panel that reported
// zero would be told to be silent, and would then be silent.
func TestTheReportedVolumeIsThePanelsVolume(t *testing.T) {
	a := &deviceAudio{backend: &fakeBackend{}, log: testLogger()}
	a.setVolume(42)

	state := &State{}
	a.fillState(state)

	if state.Volume != 42 {
		t.Errorf("the reported volume was %d, want 42", state.Volume)
	}
	if state.Muted {
		t.Error("the panel reported itself muted when it was not")
	}
}

// TestTheMuteSwitchDoesNotMuteThePanel checks that the setting which
// says the device has a mute switch does not mute it.
//
// The setting describes the hardware. Treating it as the mute itself left
// the panel silent from the moment it was configured, because the setting
// is on by default.
func TestTheMuteSwitchDoesNotMuteThePanel(t *testing.T) {
	a := &deviceAudio{backend: &fakeBackend{}, log: testLogger()}

	present := true
	tracker := state.NewTracker()
	applyConfig(Config{MuteButton: &present}, &stateReporter{state: tracker}, a, nil, nil, testLogger())

	state := &State{}
	a.fillState(state)
	if state.Muted {
		t.Error("the panel muted itself because it has a mute switch")
	}
}

// TestTheServersMuteSilencesTheMusic checks that a mute from the music
// server reaches the speaker.
//
// The server can mute the music, and that is not the panel's own switch.
// The two are kept apart, so that unmuting on one does not unmute the
// other, and both are honoured.
func TestTheServersMuteSilencesTheMusic(t *testing.T) {
	a := &deviceAudio{backend: &fakeBackend{}, log: testLogger()}

	a.mu.Lock()
	a.sendspinMuted = true
	muted := a.muted || a.sendspinMuted
	a.mu.Unlock()

	pcm := []byte{0x00, 0x40, 0x00, 0x40}
	applySendspinGain(pcm, 100, muted, false, 0)
	for i := 0; i < len(pcm); i += 2 {
		if pcm[i] != 0x00 || pcm[i+1] != 0x00 {
			t.Error("the music was not silent when the server had muted it")
		}
	}
}
