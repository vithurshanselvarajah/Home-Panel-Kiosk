package gate

import (
	"math"
	"sync"
	"time"
)

const (
	DefaultThreshold = 3.0

	DefaultRelease = 1200 * time.Millisecond

	DefaultAttack = 6.0
)

type Gate struct {
	mu sync.Mutex

	threshold float64

	release time.Duration

	attack float64

	floor float64

	floorSet bool

	openUntil time.Time

	open bool

	level float64

	lastAt time.Time
}

type Options struct {
	Threshold float64

	Release time.Duration

	Attack float64
}

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

func (g *Gate) Allow(pcm []byte, at time.Time) bool {
	level := Level(pcm)

	g.mu.Lock()
	defer g.mu.Unlock()

	g.level = level
	g.lastAt = at

	if !g.floorSet {
		g.floor = level
		g.floorSet = true
	} else {
		switch {
		case level < g.floor:
			g.floor += (level - g.floor) * floorFallRate
		case level > g.floor:

			g.floor += (level - g.floor) * floorRiseRate
		}
	}

	if g.open && at.After(g.openUntil) {
		g.open = false
	}

	if g.open {
		return true
	}

	threshold := g.floor * g.threshold
	if threshold <= 0 {

		return false
	}

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

const (
	floorFallRate = 0.05

	floorRiseRate = 0.002
)

func (g *Gate) Open() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.open
}

func (g *Gate) Floor() float64 {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.floor
}

func (g *Gate) Signal() float64 {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.level
}

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

	return int(math.Round(percent))
}

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

func (g *Gate) Reset() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.floor = 0
	g.floorSet = false
	g.open = false
	g.openUntil = time.Time{}
}
