package audio

import (
	"os"
	"path/filepath"
	"testing"
)

// The card's playback device is usually device 0 and its captures follow
// it, so the first capture is not found by counting. It is found by asking
// the kernel which device is which.
//
// The kernel writes the direction in capitals, and the file for a
// playback-only device does not exist at all, so a card whose first device
// is its playback has to be handled.
func TestFirstCaptureFindsTheCaptureDevice(t *testing.T) {
	root := t.TempDir()
	card := filepath.Join(root, "card0")
	if err := os.MkdirAll(card, 0o755); err != nil {
		t.Fatal(err)
	}
	// Each device has a directory of its own, with the info file in it.
	write := func(name, body string) {
		dir := filepath.Join(card, name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "info"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// Device 0 is the playback, and has no capture file at all.
	write("pcm1c", "card: 0\ndevice: 1\nstream: CAPTURE\nid: DL1_AWB_Record\n")
	write("pcm2c", "card: 0\ndevice: 2\nstream: CAPTURE\nid: Capture_1\n")

	got, ok := firstCaptureIn(root, 0)
	if !ok {
		t.Fatal("the card's capture device was not found")
	}
	if got != 1 {
		t.Fatalf("the capture device is %d, want 1", got)
	}
}

// A card with no capture at all is not a card that has one.
func TestFirstCaptureSaysWhenThereIsNone(t *testing.T) {
	root := t.TempDir()
	card := filepath.Join(root, "card0")
	if err := os.MkdirAll(card, 0o755); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(card, "pcm0p")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "info"),
		[]byte("card: 0\ndevice: 0\nstream: PLAYBACK\nid: Playback_1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, ok := firstCaptureIn(root, 0); ok {
		t.Fatal("a card with no capture was found to have one")
	}
}
