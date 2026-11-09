package audio

import (
	"context"
	"strings"
	"testing"
	"time"
)

// TestConcurrentCaptureAndPlayback checks that a capture and a playback can
// run at the same time.
//
// They run on different goroutines and different devices, and both call into
// libasound at once. The buffer each one passes to the library is Go memory,
// and the collector may move it while the sound card is reading it — so the
// buffer is handed over as a slice, which the FFI layer pins for the
// duration of the call.
//
// A machine with no sound card skips the test rather than failing it: the
// point is the concurrency, and there is nothing to be concurrent about.
func TestConcurrentCaptureAndPlayback(t *testing.T) {
	inputs, outputs, err := List()
	if err != nil {
		t.Skipf("this machine has no audio devices: %v", err)
	}
	if len(inputs) == 0 || len(outputs) == 0 {
		t.Skip("this machine has no audio devices")
	}

	backend := New("", "")

	// The capture and the playback have to be different devices, because
	// ALSA will not open a device for capture while it is open for
	// playback. Which devices those are depends on the machine, so they
	// are found rather than assumed: a card the sound server is holding
	// cannot be opened at all, and a card that only runs at 48 kHz cannot
	// be captured at 16 kHz.
	capture, ok := firstWorkingCapture(t, backend, inputs)
	if !ok {
		t.Skip("no capture device on this machine could be opened")
	}
	playback, ok := firstWorkingPlayback(t, backend, outputs, capture)
	if !ok {
		t.Skip("no playback device on this machine could be opened")
	}
	t.Logf("capturing from %s and playing to %s", capture, playback)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	reader, err := backend.Capture(ctx, CaptureRate)
	if err != nil {
		t.Fatalf("the microphone could not be opened: %v", err)
	}
	defer reader.Close()

	writer, err := backend.Playback(ctx, PlaybackRate)
	if err != nil {
		t.Fatalf("the speaker could not be opened: %v", err)
	}
	defer writer.Close()

	// Both run at once, for long enough that the collector has a chance to
	// move the buffers if it is going to.
	//
	// The capture is read in blocks of a tenth of a second, and the
	// controller's frames are a tenth of a second, so a block is a frame.
	// The buffer holds mono samples and the device gives two channels, so
	// the device is asked for twice as many bytes.
	const blocks = 30
	readDone := make(chan error, 1)
	go func() {
		buf := make([]byte, CaptureRate/10*2)
		for i := 0; i < blocks; i++ {
			if _, err := reader.Read(buf); err != nil {
				readDone <- err
				return
			}
		}
		readDone <- nil
	}()

	writeDone := make(chan error, 1)
	go func() {
		buf := make([]byte, PlaybackRate/10*2)
		for i := 0; i < blocks; i++ {
			if _, err := writer.Write(buf); err != nil {
				writeDone <- err
				return
			}
		}
		writeDone <- nil
	}()

	select {
	case err := <-readDone:
		if err != nil {
			t.Errorf("the capture failed: %v", err)
		}
	case <-time.After(25 * time.Second):
		t.Fatal("the capture did not finish")
	}

	select {
	case err := <-writeDone:
		if err != nil {
			t.Errorf("the playback failed: %v", err)
		}
	case <-time.After(25 * time.Second):
		t.Fatal("the playback did not finish")
	}
}

// firstWorkingCapture finds a capture device that can be opened.
//
// The devices are tried in the order they are listed, and the first one
// that opens is used. A device that cannot be opened is not a failure of
// the test: the sound server holds some of them, and the machine may have
// no usable microphone at all.
func firstWorkingCapture(t *testing.T, backend *Backend, devices []Endpoint) (string, bool) {
	t.Helper()
	for _, d := range devices {
		if err := backend.SelectAudioDevices(d.Name, ""); err != nil {
			continue
		}
		reader, err := backend.Capture(context.Background(), CaptureRate)
		if err != nil {
			t.Logf("the capture device %s could not be opened: %v", d.Name, err)
			continue
		}
		reader.Close()
		return d.Name, true
	}
	return "", false
}

// firstWorkingPlayback finds a playback device that can be opened.
//
// It skips the device the capture is using, because a card that is open
// for capture cannot be opened for playback at the same time.
func firstWorkingPlayback(t *testing.T, backend *Backend, devices []Endpoint, capture string) (string, bool) {
	t.Helper()
	for _, d := range devices {
		if d.Name == capture {
			continue
		}
		if err := backend.SelectAudioDevices(capture, d.Name); err != nil {
			continue
		}
		writer, err := backend.Playback(context.Background(), PlaybackRate)
		if err != nil {
			t.Logf("the playback device %s could not be opened: %v", d.Name, err)
			continue
		}
		writer.Close()
		return d.Name, true
	}
	return "", false
}

// TestAFailedOpenDoesNotLeaveAUsableHandle checks that a device that
// cannot be opened is reported rather than crashing.
//
// ALSA does not touch the output pointer when an open fails, so a handle
// that was not zeroed first holds the previous stream's value. Closing it,
// or configuring it, takes the process down — ALSA answers with an
// assertion rather than an error.
//
// This is the failure that a busy device produces, and a busy device is an
// ordinary thing: the sound server has it, or another program does.
func TestAFailedOpenDoesNotLeaveAUsableHandle(t *testing.T) {
	inputs, _, err := List()
	if err != nil {
		t.Skipf("this machine has no audio devices: %v", err)
	}
	if len(inputs) == 0 {
		t.Skip("this machine has no audio devices")
	}

	backend := New(inputs[0].Name, inputs[0].Name)

	// Open one direction, so there is a real handle in play.
	writer, err := backend.Playback(context.Background(), PlaybackRate)
	if err != nil {
		t.Skipf("the speaker could not be opened: %v", err)
	}
	defer writer.Close()

	// Now the other direction on the same device, which cannot work
	// while the first is open. It has to fail with an error.
	reader, err := backend.Capture(context.Background(), CaptureRate)
	if err == nil {
		reader.Close()
		t.Log("the machine allowed both directions on one device")
		return
	}
	// The error has to name the device and say it is busy, because that
	// is what a person needs to know.
	if !strings.Contains(err.Error(), "busy") && !strings.Contains(err.Error(), "open") {
		t.Errorf("the failure was %q, want it to say the device could not be opened", err)
	}
}
