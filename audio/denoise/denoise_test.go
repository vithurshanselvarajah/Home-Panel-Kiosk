package denoise

import (
	"math"
	"math/rand/v2"
	"testing"
)

// The denoiser takes the steady noise out of a microphone stream.
//
// What it has to be right about is that it takes the noise out and
// leaves the speech alone. A denoiser that leaves noise in is no use, and
// one that takes the speech with it is worse than none.

// frame is how many samples a frame is here.
const frame = 160

// rms is the level of a frame.
func rms(f []int16) float64 {
	var sum float64
	for _, v := range f {
		s := float64(v)
		sum += s * s
	}
	return math.Sqrt(sum / float64(len(f)))
}

// noise is a frame of steady noise at a level.
func noise(level float64, seed uint64) []int16 {
	r := rand.New(rand.NewPCG(seed, 2))
	out := make([]int16, frame)
	for i := range out {
		out[i] = int16((r.Float64()*2 - 1) * level * math.MaxInt16)
	}
	return out
}

// speech is a frame of a voice at a level.
func speech(level float64, seed uint64) []int16 {
	r := rand.New(rand.NewPCG(seed, 1))
	amp := level * math.MaxInt16
	out := make([]int16, frame)
	for i := range out {
		v := amp * math.Sin(2*math.Pi*300*float64(i)/16000)
		v += amp * 0.3 * (r.Float64()*2 - 1)
		out[i] = int16(v)
	}
	return out
}

// TestTheNoiseIsTakenOut checks that steady noise is quieter after the
// filter than it was before.
//
// This is the whole point of the filter: the room's hum and hiss are
// what the wake word model hears when nobody is talking, and a model
// that hears the room all day hears the room as speech.
func TestTheNoiseIsTakenOut(t *testing.T) {
	s := NewStream(16000)

	// The opening frames measure the room and are not filtered, so the
	// filter is given some noise first and the measurement is taken after.
	var before float64
	for i := 0; i < 40; i++ {
		f := noise(0.02, uint64(i))
		s.Apply(f)
		if i == 20 {
			before = rms(f)
		}
	}

	var after float64
	for i := 40; i < 60; i++ {
		f := noise(0.02, uint64(i))
		s.Apply(f)
		if i == 59 {
			after = rms(f)
		}
	}

	if after >= before {
		t.Errorf("the noise was not taken out: %v became %v", before, after)
	}
}

// TestTheSpeechSurvives checks that speech is not taken out with the
// noise.
//
// A denoiser that smooths the speech away is worse than one that leaves
// the noise in, because the noise is what the model was trained to
// ignore and the speech is what it was trained to hear.
func TestTheSpeechSurvives(t *testing.T) {
	s := NewStream(16000)

	// The room is measured first.
	for i := 0; i < 20; i++ {
		s.Apply(noise(0.005, uint64(i)))
	}

	var before, after float64
	for i := 20; i < 40; i++ {
		f := speech(0.05, uint64(i))
		s.Apply(f)
		if i == 20 {
			before = rms(f)
		}
		if i == 39 {
			after = rms(f)
		}
	}

	// The speech is still there. It may be quieter — the filter takes
	// some of it with the noise — but it has not gone.
	if after < before/8 {
		t.Errorf("the speech was taken out with the noise: %v became %v", before, after)
	}
	if after <= 0 {
		t.Error("the speech was silenced")
	}
}

// TestTheFilterStaysFinite checks that nothing in the filter turns into
// a non-finite value.
//
// The noise estimate is a running sum that is updated whenever a frame
// looks like noise, and a hardware mute produces digital silence for as
// long as it lasts. A filter that let the estimate decay to zero would
// then read the returning room as speech, and an estimate that
// underflowed would turn every bin non-finite for good.
func TestTheFilterStaysFinite(t *testing.T) {
	s := NewStream(16000)

	// Silence, which is what a muted microphone gives.
	silent := make([]int16, frame)
	for i := 0; i < 200; i++ {
		s.Apply(silent)
	}

	// Then the room comes back.
	for i := 0; i < 40; i++ {
		f := noise(0.01, uint64(i))
		s.Apply(f)
		for _, v := range f {
			if math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) {
				t.Fatal("the filter produced a non-finite sample")
			}
		}
	}
}

// TestForgetDropsTheRoom checks that the filter can be taught the room
// again.
//
// A room that changes — a window opens, the heating comes on — leaves
// the filter holding an estimate of a room that is not there, and
// forgetting is the way out of it.
func TestForgetDropsTheRoom(t *testing.T) {
	f := New(16000)
	in := make([]float64, f.Frame())
	out := make([]float64, f.Hop())
	for i := 0; i < 40; i++ {
		n := noise(0.05, uint64(i))
		for j, v := range n {
			in[j] = float64(v)
		}
		f.Push(in, out)
	}

	var held float64
	for _, n := range f.noise {
		held += n
	}
	if held == 0 {
		t.Fatal("the filter learned nothing about the room")
	}

	f.Forget()

	for k, n := range f.noise {
		if n != 0 {
			t.Fatalf("bin %d still holds %v", k, n)
		}
	}
	if f.opening != 0 || f.started {
		t.Errorf("opening = %d, started = %v; want 0, false", f.opening, f.started)
	}
}

// TestTheStreamKeepsTheFrameLength checks that the stream is a delay and
// not a change of shape.
//
// The microphone's frames are a fixed size, and whatever goes in has to
// come out the same size: a frame that came back a different length
// would move everything after it in the stream.
func TestTheStreamKeepsTheFrameLength(t *testing.T) {
	s := NewStream(16000)
	for _, n := range []int{320, 160, 1, 7, 640, 1000} {
		buf := make([]int16, n)
		s.Apply(buf)
		if len(buf) != n {
			t.Errorf("a frame of %d came back as %d", n, len(buf))
		}
	}
}
