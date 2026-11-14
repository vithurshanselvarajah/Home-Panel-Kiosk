package rate

import (
	"encoding/binary"
	"math"
	"testing"
)

// The rate converters.
//
// The card runs at 96 kHz and the mix is made at 48 kHz, so the mix is
// taken up to the card's rate on the way out. What matters is that the
// conversion does not change the level and does not leave the images of
// the signal in the output.

// TestTheUpsamplerRaisesTheRate checks that the output is as long as it
// should be.
//
// Each input sample becomes several output samples, and the count has to
// be exact: one sample too many and the audio drifts against the clock.
func TestTheUpsamplerRaisesTheRate(t *testing.T) {
	u := NewUpsampler(2, 48, 2, 1)

	in := make([]byte, 4*100) // 100 stereo frames
	for i := 0; i < len(in); i += 4 {
		binary.LittleEndian.PutUint16(in[i:], uint16(int16(1000)))
		binary.LittleEndian.PutUint16(in[i+2:], uint16(int16(1000)))
	}

	out := u.S16LE(in, nil)
	// A hundred frames at twice the rate is two hundred frames.
	if len(out) != 100*2*4 {
		t.Errorf("a hundred frames came out as %d bytes, want %d", len(out), 100*2*4)
	}
}

// TestTheUpsamplerKeepsTheLevel checks that the conversion does not
// change the level.
//
// The filter is scaled for the factor, so a constant signal comes out at
// the same level it went in at. A conversion that quietly halved the
// level would be a volume that changed for a reason nobody could see.
func TestTheUpsamplerKeepsTheLevel(t *testing.T) {
	u := NewUpsampler(2, 48, 1, 1)

	const level = 8000
	in := make([]byte, 2*200)
	for i := 0; i < len(in); i += 2 {
		binary.LittleEndian.PutUint16(in[i:], uint16(int16(level)))
	}

	out := u.S16LE(in, nil)

	// The middle of the output is past the filter's settling, so it is
	// where the level is measured.
	var sum float64
	count := 0
	for i := len(out)/2; i+1 < len(out); i += 2 {
		v := float64(int16(binary.LittleEndian.Uint16(out[i:])))
		sum += v
		count++
	}
	got := sum / float64(count)

	if math.Abs(got-level) > level*0.05 {
		t.Errorf("the level came out as %.0f, want %.0f", got, float64(level))
	}
}

// TestTheUpsamplerHoldsSilence checks that silence stays silence.
//
// The speaker's stream is open between sounds, and a conversion that
// invented signal where there was none would put a hiss in the gaps.
func TestTheUpsamplerHoldsSilence(t *testing.T) {
	u := NewUpsampler(2, 48, 2, 1)

	in := make([]byte, 4*100)
	out := u.S16LE(in, nil)

	for i := 0; i+1 < len(out); i += 2 {
		if v := binary.LittleEndian.Uint16(out[i:]); v != 0 {
			t.Fatalf("silence came out as %d", int16(v))
		}
	}
}

// TestTheDecimatorLowersTheRate checks that the output is as long as it
// should be.
//
// Every factor-th input sample is kept, and the count has to be exact:
// one sample too many and the audio drifts.
func TestTheDecimatorLowersTheRate(t *testing.T) {
	d := NewDecimator(6, 192)

	const input = 9600
	out := 0
	for i := 0; i < input; i++ {
		if _, ok := d.Push(float32(i)); ok {
			out++
		}
	}

	if out != input/6 {
		t.Errorf("%d samples came out as %d, want %d", input, out, input/6)
	}
}

// TestTheDecimatorKeepsTheLevel checks that the conversion does not
// change the level.
//
// A constant signal comes out at the level it went in at, so the chain's
// gain is the gain and not the conversion's.
func TestTheDecimatorKeepsTheLevel(t *testing.T) {
	d := NewDecimator(6, 192)

	const level = 8000
	var sum float64
	count := 0
	for i := 0; i < 6000; i++ {
		if v, ok := d.Push(level); ok {
			sum += float64(v)
			count++
		}
	}
	got := sum / float64(count)

	if math.Abs(got-level) > level*0.05 {
		t.Errorf("the level came out as %.0f, want %.0f", got, float64(level))
	}
}
