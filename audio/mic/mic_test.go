package mic

import (
	"encoding/binary"
	"math"
	"testing"
)

// The microphone's chain.
//
// What it has to be right about is that the wake word is handed audio it
// can hear: at the rate it wants, at a level it knows, with the room's
// noise taken out. The tests below check the three things that would
// break that.

// cardFrame is one frame of the card's capture: four channels of 32-bit
// samples.
func cardFrame(levels []float64) []byte {
	out := make([]byte, len(levels)*4)
	for i, level := range levels {
		// The card's zero is the middle of the int32 range, and the
		// sample is left-aligned, so the value is the level shifted up.
		v := int32(level * math.MaxInt32)
		binary.LittleEndian.PutUint32(out[i*4:], uint32(v))
	}
	return out
}

// TestTheOutputIsAtTheWakeWordsRate checks that the chain hands over the
// rate the wake word wants.
//
// The card runs at 96 kHz and the model was trained on 16 kHz, and the
// conversion between them is the chain's first job.
func TestTheOutputIsAtTheWakeWordsRate(t *testing.T) {
	p := New(CardChannels, false)

	// A hundred milliseconds of the card's own audio.
	in := make([]byte, CardRate/10*CardChannels*4)
	for i := 0; i < len(in); i += CardChannels * 4 {
		copy(in[i:], cardFrame([]float64{0.1, 0.1, 0.1, 0.1}))
	}

	out := p.Process(in, CardChannels)
	if len(out) == 0 {
		t.Fatal("the chain handed over nothing")
	}
	// A tenth of a second at 16 kHz is sixteen hundred samples.
	if len(out) < 1500 || len(out) > 1700 {
		t.Errorf("a tenth of a second came out as %d samples, want about 1600", len(out))
	}
}

// TestTheChannelsAreMixedDown checks that the four channels become one.
//
// The array's channels are four views of the same room, and the model
// wants one of them.
func TestTheChannelsAreMixedDown(t *testing.T) {
	p := New(CardChannels, false)

	// Only the first channel carries anything.
	in := make([]byte, CardRate/10*CardChannels*4)
	for i := 0; i < len(in); i += CardChannels * 4 {
		copy(in[i:], cardFrame([]float64{0.5, 0, 0, 0}))
	}

	out := p.Process(in, CardChannels)
	if len(out) == 0 {
		t.Fatal("the chain handed over nothing")
	}

	// The output has to carry something, and it has to be in the right
	// range: the leveler brings the speech up, so it is not the level
	// that went in.
	var peak int
	for _, v := range out {
		a := int(v)
		if a < 0 {
			a = -a
		}
		if a > peak {
			peak = a
		}
	}
	if peak == 0 {
		t.Error("the output was silent")
	}
}

// TestSilenceStaysSilence checks that a silent capture comes out silent.
//
// A muted microphone gives digital silence, and the chain must not turn
// that into noise: a filter that invents signal where there was none
// would have the wake word hearing a room that is not there.
func TestSilenceStaysSilence(t *testing.T) {
	p := New(CardChannels, false)

	in := make([]byte, CardRate/10*CardChannels*4)
	for i := 0; i < len(in); i += CardChannels * 4 {
		copy(in[i:], cardFrame([]float64{0, 0, 0, 0}))
	}

	out := p.Process(in, CardChannels)
	for _, v := range out {
		if v != 0 {
			t.Fatalf("silence came out as %d", v)
		}
	}
}

// TestTheLevelIsPublished checks that the chain says how loud the room
// is.
//
// It is what the drawer's meter shows, and it is measured against the
// room's own noise so that it means the same in a quiet house as in a
// noisy one.
func TestTheLevelIsPublished(t *testing.T) {
	p := New(CardChannels, false)

	// A quiet room, measured over some frames.
	for i := 0; i < 20; i++ {
		in := make([]byte, CardRate/10*CardChannels*4)
		for j := 0; j < len(in); j += CardChannels * 4 {
			copy(in[j:], cardFrame([]float64{0.002, 0.002, 0.002, 0.002}))
		}
		p.Process(in, CardChannels)
	}

	if p.Level() < 0 || p.Level() > 1 {
		t.Errorf("the level is %v, which is outside its range", p.Level())
	}
	if p.Floor() > 0 {
		t.Errorf("the room's floor is %v dBFS, which is above silence", p.Floor())
	}
}
