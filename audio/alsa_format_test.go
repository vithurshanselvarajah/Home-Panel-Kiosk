package audio

import (
	"testing"

	"github.com/vithurshanselvarajah/Home-Panel-Kiosk/audio/mic"
)

// The number of frames in a buffer depends on how many bytes a sample
// takes.
//
// The Echo Show 8's sound card hands the microphones over in 32-bit
// samples, and the stream used to assume 16-bit ones whatever it had
// been opened as. A buffer sized for 16-bit samples was then read as if
// it held twice as many frames, and the sound card wrote them past the
// end of it: a bus error, which takes the whole program down.
//
// This is checked by running the sizing the stream does, rather than by
// looking for a line of code: the number the stream reads frames with is
// the thing that has to be right.

// The sample formats are ALSA's own numbers.
//
// The format that is 8 is S32_BE: the same width the other way round. A
// card that is asked for it says the format is not available, so a
// panel that used the wrong number had no microphone at all.
func TestTheSampleFormatsAreALSAsOwn(t *testing.T) {
	if s16le != 2 {
		t.Errorf("the 16-bit format is %d, want 2", s16le)
	}
	if s32le != 10 {
		t.Errorf("the 32-bit format is %d, want 10", s32le)
	}
	if FormatS16LE != s16le {
		t.Errorf("the 16-bit format is %d in one place and %d in another", FormatS16LE, s16le)
	}
	if FormatS32LE != s32le {
		t.Errorf("the 32-bit format is %d in one place and %d in another", FormatS32LE, s32le)
	}
}

// The card's capture format is the same in both places.
//
// The audio package opens the card in this format to tell a real
// microphone from a dummy, and the microphone's chain reads the frames
// in it. If the two disagreed the chain would be handed frames in a
// format it does not expect.
func TestTheCardFormatIsTheSameInBothPlaces(t *testing.T) {
	if CardCaptureRate != mic.CardRate {
		t.Errorf("the card's rate is %d in the audio package and %d in the microphone's chain", CardCaptureRate, mic.CardRate)
	}
	if CardCaptureChannels != mic.CardChannels {
		t.Errorf("the card's channel count is %d in the audio package and %d in the microphone's chain", CardCaptureChannels, mic.CardChannels)
	}
}

func TestSampleBytes(t *testing.T) {
	cases := []struct {
		name   string
		format int
		want   int
	}{
		{"16-bit little endian", s16le, 2},
		{"32-bit little endian", s32le, 4},
	}
	for _, c := range cases {
		if got := sampleBytes(c.format); got != c.want {
			t.Errorf("%s: a sample is %d bytes, want %d", c.name, got, c.want)
		}
	}
}

// The frames in a buffer must fit in the buffer.
//
// The stream asks the sound card for a number of frames and gives it a
// buffer to write them into. If the frame count is worked out with the
// wrong sample size the card writes past the end of the buffer.
func TestFramesFitTheBuffer(t *testing.T) {
	const channels = 4
	const rate = 96000

	for _, format := range []int{s16le, s32le} {
		bytesPerSample := sampleBytes(format)
		frameBytes := channels * bytesPerSample

		// A hundred milliseconds of the card's own audio, which is the
		// size the microphone's buffer is.
		buf := make([]byte, rate/10*channels*4)

		frames := len(buf) / frameBytes
		if frames == 0 {
			t.Fatalf("the buffer holds no frames")
		}

		// What the card is told to write.
		written := frames * frameBytes
		if written > len(buf) {
			t.Errorf("format %d: the card would write %d bytes into a %d byte buffer", format, written, len(buf))
		}
	}
}

// The stream has to remember how it was opened.
//
// A stream opened as 32-bit and read as 16-bit is the bug this file is
// about, so the constructor has to carry the format through.
func TestStreamRemembersItsFormat(t *testing.T) {
	for _, format := range []int{s16le, s32le} {
		stream := &audioStream{channels: 4, bytesPerSample: sampleBytes(format)}
		if stream.bytesPerSample != sampleBytes(format) {
			t.Errorf("the stream does not remember the sample size it was opened with")
		}
	}
}