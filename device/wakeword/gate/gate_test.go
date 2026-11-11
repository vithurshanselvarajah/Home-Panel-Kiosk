package gate

import (
	"encoding/binary"
	"math"
	"testing"
	"time"
)

// frame builds a frame of audio at the given amplitude.
func frame(amplitude float64, samples int) []byte {
	pcm := make([]byte, samples*2)
	value := int16(amplitude)
	for i := 0; i < samples; i++ {
		binary.LittleEndian.PutUint16(pcm[i*2:], uint16(value))
	}
	return pcm
}

// noiseFrame builds a frame of audio with a given mean square, which is
// what the gate actually measures.
func noiseFrame(meanSquare float64, samples int) []byte {
	pcm := make([]byte, samples*2)
	amplitude := int16(math.Sqrt(meanSquare))
	for i := 0; i < samples; i++ {
		binary.LittleEndian.PutUint16(pcm[i*2:], uint16(amplitude))
	}
	return pcm
}

func TestLevelIsMeanSquare(t *testing.T) {
	// 1000 and 2000 in the two halves of one sample.
	pcm := []byte{0xe8, 0x03, 0xd0, 0x07}
	got := Level(pcm)
	want := (1000.0*1000.0 + 2000.0*2000.0) / 2
	if math.Abs(got-want) > 0.5 {
		t.Errorf("the level was %f, want %f", got, want)
	}
}

func TestLevelOfEmptyFrameIsZero(t *testing.T) {
	if got := Level(nil); got != 0 {
		t.Errorf("the level of nothing was %f, want 0", got)
	}
	if got := Level([]byte{0x01}); got != 0 {
		t.Errorf("the level of half a sample was %f, want 0", got)
	}
}

func TestGateStaysShutOnSilence(t *testing.T) {
	gate := New(Options{})
	now := time.Now()

	// The first frame sets the floor. It is silence, so nothing is worth
	// scoring and the gate stays shut.
	for i := 0; i < 50; i++ {
		if gate.Allow(frame(0, 1280), now.Add(time.Duration(i)*80*time.Millisecond)) {
			t.Fatalf("frame %d: the gate opened on silence", i)
		}
	}
	if gate.Open() {
		t.Error("the gate is open on silence")
	}
}

func TestGateOpensOnSpeech(t *testing.T) {
	gate := New(Options{})
	now := time.Now()

	// A quiet room first, so the floor is measured.
	for i := 0; i < 50; i++ {
		gate.Allow(noiseFrame(100, 1280), now.Add(time.Duration(i)*80*time.Millisecond))
	}

	// Then somebody speaks. The frame is far above the floor, so the gate
	// opens and the scorer is given the frame.
	if !gate.Allow(noiseFrame(5000, 1280), now.Add(5*time.Second)) {
		t.Error("the gate stayed shut while somebody was speaking")
	}
	if !gate.Open() {
		t.Error("the gate is shut while somebody is speaking")
	}
}

func TestGateStaysOpenAcrossAPause(t *testing.T) {
	gate := New(Options{Attack: 6, Release: 1200 * time.Millisecond})
	now := time.Now()

	for i := 0; i < 50; i++ {
		gate.Allow(noiseFrame(100, 1280), now.Add(time.Duration(i)*80*time.Millisecond))
	}
	base := now.Add(5 * time.Second)
	gate.Allow(noiseFrame(5000, 1280), base)

	// A pause of half a second, which is a normal gap between words.
	// The gate must stay open, or the scorer would warm up again in the
	// gap and a wake word spoken across it would be missed.
	for i := 0; i < 6; i++ {
		at := base.Add(time.Duration(i+1) * 80 * time.Millisecond)
		if !gate.Allow(noiseFrame(100, 1280), at) {
			t.Fatalf("the gate shut %d frames into a pause", i)
		}
	}
}

func TestGateClosesAfterTheRelease(t *testing.T) {
	gate := New(Options{Release: 500 * time.Millisecond})
	now := time.Now()

	for i := 0; i < 50; i++ {
		gate.Allow(noiseFrame(100, 1280), now.Add(time.Duration(i)*80*time.Millisecond))
	}
	base := now.Add(5 * time.Second)
	gate.Allow(noiseFrame(5000, 1280), base)

	// Long after the release has passed, the gate is shut again and a
	// quiet frame is not passed on.
	later := base.Add(2 * time.Second)
	if gate.Allow(noiseFrame(100, 1280), later) {
		t.Error("the gate stayed open after its release")
	}
	if gate.Open() {
		t.Error("the gate is open after its release")
	}
}

func TestGateFollowsALouderRoom(t *testing.T) {
	gate := New(Options{})
	now := time.Now()

	// A quiet room, so the floor is low.
	for i := 0; i < 50; i++ {
		gate.Allow(noiseFrame(100, 1280), now.Add(time.Duration(i)*80*time.Millisecond))
	}
	quietFloor := gate.Floor()

	// Now the room gets louder — a fan, or a television. The floor has to
	// follow it up, or every frame in the new room would be scored and
	// the gate would never shut again.
	base := now.Add(10 * time.Second)
	for i := 0; i < 500; i++ {
		gate.Allow(noiseFrame(4000, 1280), base.Add(time.Duration(i)*80*time.Millisecond))
	}
	loudFloor := gate.Floor()

	if loudFloor <= quietFloor {
		t.Errorf("the floor did not follow the room up: %f then %f", quietFloor, loudFloor)
	}
	// The floor must stay below the room's own level, or the room's own
	// noise would be scored as speech for ever.
	if loudFloor >= 4000 {
		t.Errorf("the floor reached the room's own level: %f", loudFloor)
	}
}

func TestAmplitudeIsAPercentage(t *testing.T) {
	gate := New(Options{})
	now := time.Now()

	// A full-scale wave is 32768, so a square wave at 32767 is full
	// scale.
	gate.Allow(frame(32767, 1280), now)
	if got := gate.Amplitude(); got != 100 {
		t.Errorf("the amplitude of a full-scale frame was %d, want 100", got)
	}

	gate.Allow(frame(0, 1280), now.Add(time.Second))
	if got := gate.Amplitude(); got != 0 {
		t.Errorf("the amplitude of silence was %d, want 0", got)
	}
}

func TestResetForgetsTheFloor(t *testing.T) {
	gate := New(Options{})
	now := time.Now()

	for i := 0; i < 50; i++ {
		gate.Allow(noiseFrame(100, 1280), now.Add(time.Duration(i)*80*time.Millisecond))
	}
	if gate.Floor() == 0 {
		t.Fatal("the floor was never measured")
	}

	gate.Reset()
	if gate.Floor() != 0 {
		t.Errorf("the floor was %f after a reset, want 0", gate.Floor())
	}
	if gate.Open() {
		t.Error("the gate is open after a reset")
	}
}
