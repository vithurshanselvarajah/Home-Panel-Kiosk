// Package gate keeps the wake word from running on silence.
//
// The wake word is the most expensive thing the panel does. Scoring a
// frame of audio costs about 29 milliseconds of a core, and it has to run
// often enough to catch a wake word in the moment it is said. A panel
// that scores every frame of every second of every day spends a third of
// a core to hear a room that is empty.
//
// So the score is not the first thing that happens to a frame of audio.
// The frame is measured first, and it is cheap: a sum of squares over a
// few thousand samples. Only a frame that is loud enough to be somebody
// talking is handed to the scorer.
//
// This is a gate, not a filter. It does not decide what was said, and it
// never decides that a wake word was or was not said — the scorer does
// that, and it sees every frame that passes. What the gate decides is
// only whether the room is loud enough to be worth scoring.
//
// The threshold is not fixed. It is learned from the room, the same way
// the noise floor is: a quiet frame lowers it, a loud one leaves it
// alone. So the gate follows a room that gets louder and one that gets
// quieter, and it does not need to be told.
//
// # Why the wake word has to run at all
//
// The alternative is to stream the microphone to Home Assistant and let
// it detect the wake word. That is what a device without a local model
// has to do, and it costs a constant upload of the room to the network,
// whether or not anybody is speaking. A local wake word keeps the room
// in the room: the audio leaves the machine only when somebody has
// actually said the wake word, and only for the seconds of the reply.
package gate

import (
	"math"
	"sync"
	"time"
)

// The defaults.
//
// The threshold is a ratio, not a level: how far above the measured noise
// floor a frame has to be to count as worth scoring.
const (
	// DefaultThreshold is how far above the noise floor a frame has to be.
	//
	// It is 3.0 because that is what the speech detector uses, and the
	// two are measuring the same thing. A gate that is more eager than
	// the detector below it would score frames the detector would reject
	// anyway, which is the work the gate exists to avoid.
	DefaultThreshold = 3.0

	// DefaultRelease is how long the gate stays open after it hears
	// something.
	//
	// The gate opens on a loud frame and closes when the room goes quiet
	// again. Without a hold, a person speaking with a pause in the
	// middle would have the gate shut between the words, and the scorer
	// would see the gap and warm its features up again — and a wake word
	// spoken across that gap would be missed.
	//
	// Just over a second is long enough to cover the gap between words
	// and short enough that a room which has gone quiet stops being
	// scored promptly.
	DefaultRelease = 1200 * time.Millisecond

	// DefaultAttack is how loud a frame has to be to open the gate
	// straight away.
	//
	// A frame this loud is not the tail of somebody talking; it is
	// somebody starting to talk. Opening on it immediately means the
	// wake word is scored from its first syllable rather than from the
	// second or third.
	DefaultAttack = 6.0
)

// Gate measures the room and says whether it is worth scoring.
//
// It is safe for concurrent use: the microphone goroutine measures and
// the scorer goroutine reads, and they do not have to be the same one.
type Gate struct {
	mu sync.Mutex

	// threshold is how far above the noise floor a frame has to be.
	threshold float64
	// release is how long the gate stays open after a loud frame.
	release time.Duration
	// attack is how loud a frame has to be to open the gate at once.
	attack float64

	// floor is the measured noise floor, as a mean square.
	floor float64
	// floorSet says whether the floor has been measured yet.
	floorSet bool

	// openUntil is when the gate stops being open, if it is open.
	openUntil time.Time
	// open says whether the gate is open right now.
	open bool

	// level is the last frame's level, for the level meter.
	level float64
	// lastAt is when the last frame was measured.
	lastAt time.Time
}

// Options configures a Gate.
type Options struct {
	// Threshold is how far above the noise floor a frame has to be to
	// count. Zero selects DefaultThreshold.
	Threshold float64
	// Release is how long the gate stays open after a loud frame. Zero
	// selects DefaultRelease.
	Release time.Duration
	// Attack is how loud a frame has to be to open the gate at once.
	// Zero selects DefaultAttack.
	Attack float64
}

// New returns a Gate.
func New(options Options) *Gate {
	gate := &Gate{
		threshold: options.Threshold,
		release:   options.Release,
		attack:    options.Attack,
	}
	if gate.threshold <= 0 {
		gate.threshold = DefaultThreshold
	}
	if gate.release <= 0 {
		gate.release = DefaultRelease
	}
	if gate.attack <= 0 {
		gate.attack = DefaultAttack
	}
	return gate
}

// Level returns the mean square of a frame of audio.
//
// The samples are signed 16-bit little-endian mono at 16 kHz. The result
// is a mean square, which is the energy of the frame: it is a number
// that grows with loudness and does not depend on how many samples the
// frame had, so a short frame and a long frame can be compared.
//
// An empty frame has no energy, and says so.
func Level(pcm []byte) float64 {
	if len(pcm) < 2 {
		return 0
	}
	var sum float64
	count := 0
	for i := 0; i+1 < len(pcm); i += 2 {
		sample := float64(int16(uint16(pcm[i]) | uint16(pcm[i+1])<<8))
		sum += sample * sample
		count++
	}
	if count == 0 {
		return 0
	}
	return sum / float64(count)
}

// Allow measures a frame and reports whether the scorer should see it.
//
// The frame is measured, the noise floor is updated, and the answer is
// whether the frame was loud enough to be somebody talking.
//
// A frame that arrives while the gate is open is allowed without being
// measured against the floor, because the floor is not what decides
// while the gate is open.
func (g *Gate) Allow(pcm []byte, at time.Time) bool {
	level := Level(pcm)

	g.mu.Lock()
	defer g.mu.Unlock()

	g.level = level
	g.lastAt = at

	// The floor is learned in both directions, but not at the same speed.
	//
	// It comes down quickly and goes up slowly. A quiet frame lowers it
	// straight away, because the room is quiet now and the bar should
	// reflect that. A loud frame raises it only a little, because a
	// single loud frame is more likely to be somebody talking than the
	// room having changed.
	//
	// So the floor settles at the level of the room's own noise, and
	// speech has to be louder than that to be heard — whether the
	// microphone opened during silence or in the middle of a sentence.
	if !g.floorSet {
		g.floor = level
		g.floorSet = true
	} else {
		switch {
		case level < g.floor:
			g.floor += (level - g.floor) * floorFallRate
		case level > g.floor:
			// The floor only follows a loud room up while the room keeps
			// being loud. A frame that is louder than the floor but
			// nowhere near speech — a television, a fan — raises the bar
			// a little, and fifty of them raise it enough.
			g.floor += (level - g.floor) * floorRiseRate
		}
	}

	// The gate is open for a while after anything loud, so a pause
	// between words does not shut it.
	if g.open && at.After(g.openUntil) {
		g.open = false
	}

	// An open gate lets everything through. The scorer is already warm and
	// already running, and the point of the gate is to save work, not to
	// decide what was said.
	if g.open {
		return true
	}

	threshold := g.floor * g.threshold
	if threshold <= 0 {
		// The floor is zero, which means the room is silent or the floor
		// has not been measured. Nothing is worth scoring against a floor
		// of nothing, so the gate stays shut until there is something to
		// measure against.
		return false
	}

	// A frame far louder than the floor opens the gate at once: it is
	// somebody starting to talk, and the wake word may be in it.
	if level >= g.floor*g.attack {
		g.open = true
		g.openUntil = at.Add(g.release)
		return true
	}

	if level < threshold {
		return false
	}

	g.open = true
	g.openUntil = at.Add(g.release)
	return true
}

// The rates at which the floor follows the room.
//
// The floor comes down quickly and goes up slowly. It comes down quickly
// because a quiet frame is almost always the room being quiet, and the
// bar should follow that at once. It goes up slowly because a loud frame
// is usually somebody talking, and a bar that rose to meet every sentence
// would miss the sentence that followed it.
//
// The rise rate is what lets a room that has got louder — a fan, a
// television — settle at its own level over a few seconds rather than
// being scored for ever.
const (
	// floorFallRate is how far the floor moves towards each quiet frame.
	floorFallRate = 0.05
	// floorRiseRate is how far the floor moves towards each loud frame.
	floorRiseRate = 0.002
)

// Open reports whether the gate is open right now.
//
// It is for the log and the state: a panel that says it is listening and
// is not scoring anything is a panel that is lying about itself.
func (g *Gate) Open() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.open
}

// Floor returns the measured noise floor, as a mean square.
func (g *Gate) Floor() float64 {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.floor
}

// Signal returns the level of the last frame, as a mean square.
func (g *Gate) Signal() float64 {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.level
}

// Amplitude returns the level of the last frame as a percentage of full
// scale, which is what a person can read on a level meter.
//
// The mean square is turned into a root-mean-square first, because a mean
// square of 1000 sounds like a hundred times louder than it is on a
// meter.
func (g *Gate) Amplitude() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.level <= 0 {
		return 0
	}
	rms := math.Sqrt(g.level)
	percent := rms / 32768 * 100
	if percent > 100 {
		return 100
	}
	// Rounded rather than truncated, so a frame at full scale reads as
	// 100 and not 99. A level meter that never reaches the top looks
	// broken.
	return int(math.Round(percent))
}

// Configure replaces the gate's settings.
//
// The measured floor is kept, because the floor is a property of the room
// rather than of the settings.
func (g *Gate) Configure(threshold float64, release time.Duration) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if threshold > 0 {
		g.threshold = threshold
	}
	if release > 0 {
		g.release = release
	}
}

// Reset forgets the measured floor.
//
// It is for when the microphone restarts: a floor measured before a gap
// says nothing about the room after it.
func (g *Gate) Reset() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.floor = 0
	g.floorSet = false
	g.open = false
	g.openUntil = time.Time{}
}
