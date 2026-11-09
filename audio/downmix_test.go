package audio

import (
	"testing"
)

// TestDownmixAveragesTheChannels checks that a stereo capture becomes the
// mono the controller wants.
//
// The capture devices on a laptop and on a Pi are stereo, and ALSA will not
// open one for a single channel, so the averaging happens here.
func TestDownmixAveragesTheChannels(t *testing.T) {
	// Two frames of stereo: the first is 1000 and 2000, the second is
	// -1000 and -2000.
	src := []byte{
		0xe8, 0x03, 0xd0, 0x07, // 1000, 2000
		0x18, 0xfc, 0x30, 0xf8, // -1000, -2000
	}
	dst := make([]byte, 4)

	n := downmix(src, dst)
	if n != 4 {
		t.Fatalf("the downmix produced %d bytes, want 4", n)
	}

	// The first frame averages to 1500.
	if got := int16(uint16(dst[0]) | uint16(dst[1])<<8); got != 1500 {
		t.Errorf("the first frame was %d, want 1500", got)
	}
	// The second averages to -1500.
	if got := int16(uint16(dst[2]) | uint16(dst[3])<<8); got != -1500 {
		t.Errorf("the second frame was %d, want -1500", got)
	}
}

// TestDownmixLeavesTheRestAlone checks that a buffer that is not a whole
// number of frames is not read past its end.
func TestDownmixLeavesTheRestAlone(t *testing.T) {
	// One whole frame and half of another.
	src := []byte{0xe8, 0x03, 0xd0, 0x07, 0x18, 0xfc}
	dst := make([]byte, 4)

	n := downmix(src, dst)
	if n != 2 {
		t.Errorf("the downmix produced %d bytes, want 2", n)
	}
}

// TestDownmixHandlesAnEmptyBlock checks that there is nothing to do.
func TestDownmixHandlesAnEmptyBlock(t *testing.T) {
	if n := downmix(nil, make([]byte, 4)); n != 0 {
		t.Errorf("the downmix produced %d bytes, want 0", n)
	}
}

// TestTargetUsesTheHardwareName checks how a listed name becomes the name
// the sound system is given.
//
// The `plughw:` prefix is deliberately not used: it opens the hardware
// directly, which takes the card away from the sound server and fails with
// "busy" on a machine where the server already has it.
func TestTargetUsesTheHardwareName(t *testing.T) {
	cases := map[string]string{
		"":                   "default",
		"default":            "default",
		"alsa:C920,0":        "hw:CARD=C920,DEV=0",
		"alsa:sofhdadsp,6":   "hw:CARD=sofhdadsp,DEV=6",
		"hw:CARD=C920,DEV=0": "hw:CARD=C920,DEV=0",
		"alsa:not-a-card":    "default",
	}
	for input, want := range cases {
		if got := target(input); got != want {
			t.Errorf("target(%q) was %q, want %q", input, got, want)
		}
	}
}

// TestStereoToMonoAveragesTheChannels checks that a stereo buffer becomes
// a mono one.
//
// The music is stereo and the speaker may be mono, so the two channels are
// averaged on the way out. Averaged rather than one side taken, because a
// hard-panned track played on one side only is quieter on that side than
// the other.
func TestStereoToMonoAveragesTheChannels(t *testing.T) {
	src := []byte{
		0xe8, 0x03, 0xd0, 0x07, // 1000, 2000
		0x18, 0xfc, 0x30, 0xf8, // -1000, -2000
	}
	dst := make([]byte, 4)

	n := StereoToMono(src, dst)
	if n != 4 {
		t.Fatalf("the conversion produced %d bytes, want 4", n)
	}
	if got := int16(uint16(dst[0]) | uint16(dst[1])<<8); got != 1500 {
		t.Errorf("the first frame was %d, want 1500", got)
	}
	if got := int16(uint16(dst[2]) | uint16(dst[3])<<8); got != -1500 {
		t.Errorf("the second frame was %d, want -1500", got)
	}
}

// TestStereoToMonoLeavesTheRestAlone checks that a buffer that is not a
// whole number of frames is not read past its end.
func TestStereoToMonoLeavesTheRestAlone(t *testing.T) {
	// One whole frame and half of another.
	src := []byte{0xe8, 0x03, 0xd0, 0x07, 0x18, 0xfc}
	dst := make([]byte, 4)

	n := StereoToMono(src, dst)
	if n != 2 {
		t.Errorf("the conversion produced %d bytes, want 2", n)
	}
}

// TestStereoToMonoHandlesAnEmptyBlock checks that there is nothing to do.
func TestStereoToMonoHandlesAnEmptyBlock(t *testing.T) {
	if n := StereoToMono(nil, make([]byte, 4)); n != 0 {
		t.Errorf("the conversion produced %d bytes, want 0", n)
	}
}
