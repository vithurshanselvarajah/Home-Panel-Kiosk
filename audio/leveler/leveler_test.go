package leveler

import (
	"math"
	"math/rand/v2"
	"testing"
)

// The leveler is the automatic gain control on the microphone.
//
// What it has to be right about is a room: the speech in it, the noise
// under it, and how far the two are apart. The tests below use made-up
// rooms and check the three things that matter — that quiet speech is
// brought up, that loud speech is not clipped, and that the noise under
// it is not mistaken for speech.

// frame is how many samples a frame is here.
const frame = 160

// speech is a frame of a voice at a level.
func speech(level float64, seed uint64) []int16 {
	r := rand.New(rand.NewPCG(seed, 1))
	out := make([]int16, frame)
	for i := range out {
		// A voice is not a sine wave, but the leveler only looks at the
		// level and the peak, so one is enough here.
		v := level * math.Sin(float64(i)*0.2)
		v += level * 0.3 * (r.Float64()*2 - 1)
		out[i] = int16(v * math.MaxInt16)
	}
	return out
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

// speechAt is a frame of a voice at a level in decibels below full scale.
func speechAt(dbfs float64, seed uint64) []int16 {
	amp := math.Pow(10, dbfs/20) * math.MaxInt16 * math.Sqrt2
	r := rand.New(rand.NewPCG(seed, 1))
	out := make([]int16, frame)
	for i := range out {
		v := amp * math.Sin(2*math.Pi*300*float64(i)/16000)
		v += amp * 0.2 * (r.Float64()*2 - 1)
		out[i] = int16(v)
	}
	return out
}

// noiseAt is a frame of steady noise at a level in decibels below full
// scale.
func noiseAt(dbfs float64, seed uint64) []int16 {
	amp := math.Pow(10, dbfs/20) * math.MaxInt16
	r := rand.New(rand.NewPCG(seed, 2))
	out := make([]int16, frame)
	for i := range out {
		out[i] = int16((r.Float64()*2 - 1) * amp)
	}
	return out
}

// rms is the level of a frame.
func rms(frame []int16) float64 {
	var sum float64
	for _, v := range frame {
		s := float64(v)
		sum += s * s
	}
	return math.Sqrt(sum / float64(len(frame)))
}

// TestTheGainBringsQuietSpeechUp checks that speech in a quiet room is
// brought up to a level the model knows.
//
// The model was trained on speech at a particular level, and speech
// twenty decibels below that is speech the model hears badly. The whole
// point of the leveller is that it does not matter what the room is like.
func TestTheGainBringsQuietSpeechUp(t *testing.T) {
	l := New(frame, 16000)
	// The room is measured over a few frames of nothing.
	for i := 0; i < 40; i++ {
		l.Observe(noise(0.002, uint64(i)))
	}

	// Then somebody talks, quietly.
	before := rms(speech(0.01, 1))
	out := speech(0.01, 1)
	l.Apply(out)

	after := rms(out)
	if after <= before {
		t.Errorf("quiet speech was not brought up: %v became %v", before, after)
	}
	// And it was brought up towards the target rather than to the ceiling.
	if after > 4000 {
		t.Errorf("quiet speech was brought to %v, which is louder than the target", after)
	}
}

// TestLoudSpeechIsNotClipped checks that the gain does not ask for more
// than the frame can take.
//
// The leveller's target is on the speech's average, and speech's peaks
// are well above its average. A gain that ignores the peak would clip
// every loud word.
func TestLoudSpeechIsNotClipped(t *testing.T) {
	l := New(frame, 16000)
	for i := 0; i < 40; i++ {
		l.Observe(noise(0.002, uint64(i)))
	}

	out := speech(0.3, 2)
	l.Apply(out)

	for i, v := range out {
		if v == math.MaxInt16 || v == math.MinInt16 {
			t.Errorf("sample %d is clipped at %d", i, v)
		}
	}
}

// TestTheNoiseIsNotTakenForSpeech checks that steady noise does not make
// the leveller turn the gain up.
//
// A room with a fan in it has noise that never stops. If the leveller
// followed that noise as if it were speech, the gain would climb until
// the speech was clipped.
func TestTheNoiseIsNotTakenForSpeech(t *testing.T) {
	l := New(frame, 16000)
	for i := 0; i < 60; i++ {
		l.Observe(noise(0.05, uint64(i)))
	}

	// The gain has not run away.
	if g := l.Gain(); g > 10 {
		t.Errorf("the gain is %v dB after only noise, which has run away", g)
	}
}

// TestSilenceDoesNotChangeTheGain checks that silence says nothing.
//
// Silence carries no information about how loud speech is, so the gain
// should stay where speech left it rather than climbing towards a target
// that silence can never reach.
func TestSilenceDoesNotChangeTheGain(t *testing.T) {
	l := New(frame, 16000)
	for i := 0; i < 40; i++ {
		l.Observe(noise(0.002, uint64(i)))
	}
	out := speech(0.05, 3)
	l.Apply(out)
	before := l.Gain()

	silent := make([]int16, frame)
	for i := 0; i < 20; i++ {
		l.Apply(silent)
	}

	if after := l.Gain(); after != before {
		t.Errorf("the gain moved from %v dB to %v dB on silence", before, after)
	}
}

// TestTheFloorFollowsTheRoom checks that the floor follows the noise.
//
// The floor is what speech is judged against, so it has to be the room's
// own noise and not a fixed number: a fixed number is below the noise in
// one house and above the speech in another.
func TestTheFloorFollowsTheRoom(t *testing.T) {
	quiet := New(frame, 16000)
	for i := 0; i < 60; i++ {
		quiet.Observe(noise(0.001, uint64(i)))
	}

	loud := New(frame, 16000)
	for i := 0; i < 60; i++ {
		loud.Observe(noise(0.05, uint64(i)))
	}

	if quiet.Floor() >= loud.Floor() {
		t.Errorf("a quiet room's floor is %v dB and a loud one's is %v dB",
			quiet.Floor(), loud.Floor())
	}
}

// TestTheLevelFollowsSpeech checks that the room's level rises when
// somebody talks.
//
// It is what the drawer's meter shows, and it is measured against the
// room's own noise so that it means the same in a quiet house as in a
// noisy one.
//
// The speech comes in sentences with pauses between them, because a
// continuous voice would pull the floor down onto itself and the level
// would have nothing to stand above.
func TestTheLevelFollowsSpeech(t *testing.T) {
	l := New(frame, 16000)
	settle(l, -57, 100)
	before := l.Level()

	talk(l, -57, -32, 2)

	if l.Level() <= before {
		t.Errorf("the level did not rise: %v became %v", before, l.Level())
	}
}

// settle feeds a steady level for a while, so the floor finds it.
//
// The frame is observed rather than applied, because the level is
// measured on what arrives rather than on what came out: the level is
// about the room, not about the gain in front of it.
func settle(l *Leveler, dbfs float64, frames int) {
	for i := 0; i < frames; i++ {
		l.Observe(speechAt(dbfs, uint64(i)))
	}
}

// talk feeds somebody speaking in a room: sentences with pauses between
// them, which is what keeps the floor on the room rather than on the
// voice.
func talk(l *Leveler, room, voice float64, seconds int) {
	for i := 0; i < seconds*16000/frame/20; i++ {
		for range 15 {
			l.Apply(speechAt(voice, uint64(i)))
		}
		for range 5 {
			l.Apply(noiseAt(room, uint64(i)))
		}
	}
}

// TestTheLevelIsRelativeToTheRoom checks that the same voice reads the
// same over a quiet room and a loud one.
//
// The published level is about the room, not about the gain in front of
// it. A ring set to react to it would otherwise be pinned at nothing in
// one house and at everything in another.
func TestTheLevelIsRelativeToTheRoom(t *testing.T) {
	quiet, loud := New(frame, 16000), New(frame, 16000)

	// Both rooms hear a voice eighteen decibels above their own noise.
	settle(quiet, -60, 200)
	settle(loud, -35, 200)

	quietVoice := levelAfter(quiet, -42, 40)
	loudVoice := levelAfter(loud, -17, 40)

	if math.Abs(quietVoice-loudVoice) > 0.1 {
		t.Errorf("the same voice reads %.2f over a quiet room and %.2f over a loud one",
			quietVoice, loudVoice)
	}
}

// levelAfter settles a room at a level and reports the level afterwards.
func levelAfter(l *Leveler, dbfs float64, frames int) float64 {
	settle(l, dbfs, frames)
	return l.Level()
}
