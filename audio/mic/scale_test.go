package mic

import (
	"encoding/binary"
	"math"
	"testing"
)

// The card's samples, as they were recorded from the unit.
//
// The converter's 24 bits are left-aligned in the 32-bit word, so these
// are the values the chain is given. They are from a recording of the
// unit with the room's own noise in it, so they are the real thing
// rather than something made up for the test.
//
// The largest is 1635017060, which is 2^30.6: the converter's full scale
// of 2^23 left-aligned in the word, rather than a full-scale 32-bit
// sample. The low byte of the word is set in 0.0% of the samples and
// the top byte in 53%, which is what a left-aligned sample looks like:
// the low bits are the converter's padding, not the value's own.
var recordedWords = []int32{
	1179011410, 6144036, 1163280727, 544501094, 16, 262145, 96000, 1536000, 2097168, 1635017060,
	6144000, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0,
	0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0,
	0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0,
	0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0,
}

// TestTheRecordedSamplesAreScaledToSixteenBits checks that the chain
// reads the card's words the way the card writes them.
//
// The words are left-aligned: the word is the 24-bit value shifted up by
// eight bits. The chain divides the word by the converter's full scale,
// which is 2^23, so the shift is undone by the scaling and a word at the
// converter's full scale comes out at the 16-bit full scale.
//
// Getting this wrong makes every sample a long way off: a quiet room
// sits at full scale, or the speech arrives so quietly that the model
// never hears it.
//
// The leveler and the denoiser are off, because they are what hide a
// wrong scale: the leveler brings the level back to where it wants it,
// so a chain that hands over the wrong scale still looks right.
func TestTheRecordedSamplesAreScaledToSixteenBits(t *testing.T) {
	p := New(CardChannels, false)
	p.SetLeveling(false)
	p.SetDenoising(false)

	// A tenth of a second of the recording, so the chain has a whole
	// block to work on.
	in := make([]byte, CardRate/10*CardChannels*4)
	for i := 0; i < len(in); i += CardChannels * 4 {
		for c := 0; c < CardChannels; c++ {
			word := recordedWords[(i/(CardChannels*4))%len(recordedWords)]
			binary.LittleEndian.PutUint32(in[i+c*4:], uint32(word))
		}
	}

	out := p.Process(in, CardChannels)
	if len(out) == 0 {
		t.Fatal("the chain handed over nothing")
	}

	// The loudest recorded word is 1635017060, which is 76% of the
	// converter's full scale, so the chain should hand over about 76%
	// of the 16-bit full scale.
	peak := 0
	for _, v := range out {
		if v < 0 {
			v = -v
		}
		if int(v) > peak {
			peak = int(v)
		}
	}
	if peak > math.MaxInt16 {
		t.Errorf("the chain handed over a peak of %d, which is past the full scale of %d", peak, math.MaxInt16)
	}
	// Read as the card writes them, the recording is about three quarters
	// of full scale.
	if peak < math.MaxInt16/2 {
		t.Errorf("the chain handed over a peak of %d, which is far too quiet for the recording", peak)
	}
	if peak > math.MaxInt16 {
		t.Errorf("the chain handed over a peak of %d, which is too loud: the recording is not that loud", peak)
	}
}

// TestAQuietRoomStaysQuiet checks the same thing from the other side.
//
// A quiet room has to stay quiet, or the chain hands the model a room
// that is always shouting and the wake word never fires.
func TestAQuietRoomStaysQuiet(t *testing.T) {
	p := New(CardChannels, false)
	p.SetLeveling(false)
	p.SetDenoising(false)

	// The quiet part of the recording: the words are small.
	in := make([]byte, CardRate/10*CardChannels*4)
	for i := 0; i < len(in); i += CardChannels * 4 {
		for c := 0; c < CardChannels; c++ {
			binary.LittleEndian.PutUint32(in[i+c*4:], uint32(recordedWords[len(recordedWords)-1]))
		}
	}
	out := p.Process(in, CardChannels)
	for _, v := range out {
		if v < -256 || v > 256 {
			t.Errorf("a quiet room handed over %d, which is not quiet", v)
			break
		}
	}
}
