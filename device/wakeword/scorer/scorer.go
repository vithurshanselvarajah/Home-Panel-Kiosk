package scorer

import (
	"fmt"
	"io"
	"log"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const queueFrames = 8

const DefaultRefractory = 1500 * time.Millisecond

type Stats struct {
	Frames uint64
	Drops  uint64

	NotReady uint64

	Crossings uint64

	MaxScore float32

	Threshold float32

	Errors  uint64
	LastErr string

	MaxInferMs uint32
	MaxGapMs   uint32
}

type Scorer struct {
	det       Engine
	threshold float32

	bargeThreshold float32
	speakerActive  func() bool
	refract        time.Duration
	onCross        func(score, threshold float32, at time.Time)

	ch   chan queued
	done chan struct{}
	stop sync.Once

	quit chan struct{}

	closed atomic.Bool

	closer io.Closer

	info string

	phrase string

	maxInferMs atomic.Int64
	maxGapMs   atomic.Int64

	lastPush time.Time

	mu        sync.Mutex
	stats     Stats
	ready     bool
	lastCross time.Time

	prevAbove bool
	prevAt    time.Time

	minFrames int
	runAts    []time.Time

	bargePeak   float32
	bargeFrames uint64
	bargeBar    float32

	resetReq bool

	traceLabel func() string
	hist       []float32
	trace      *scoreTrace
}

const (
	traceFloor = 0.15
	tracePre   = 12
	tracePost  = 12
)

type scoreTrace struct {
	label   string
	bar     float32
	pre     []float32
	post    []float32
	crossed bool
}

func (s *Scorer) SetTraceLabel(label func() string) {
	s.mu.Lock()
	s.traceLabel = label
	s.mu.Unlock()
}

func (s *Scorer) traceFrame(score, bar float32, crossed bool) {
	if s.traceLabel == nil {
		return
	}
	if t := s.trace; t != nil {
		t.post = append(t.post, score)
		t.crossed = t.crossed || crossed
		if len(t.post) >= tracePost {
			var b strings.Builder
			for _, v := range t.pre {
				fmt.Fprintf(&b, " %.3f", v)
			}
			b.WriteString(" |")
			for _, v := range t.post {
				fmt.Fprintf(&b, " %.3f", v)
			}
			log.Printf("[shadow] trace %s bar=%.2f crossed=%v:%s", t.label, t.bar, t.crossed, b.String())
			s.trace = nil
		}
	} else if score >= traceFloor {
		s.trace = &scoreTrace{
			label:   s.traceLabel(),
			bar:     bar,
			pre:     append([]float32(nil), s.hist...),
			post:    []float32{score},
			crossed: crossed,
		}
	}
	s.hist = append(s.hist, score)
	if len(s.hist) > tracePre {
		s.hist = s.hist[1:]
	}
}

func NewScorer(det Engine, threshold float32, onCross func(score, threshold float32, at time.Time)) *Scorer {
	s := &Scorer{
		det:       det,
		threshold: threshold,
		refract:   DefaultRefractory,
		onCross:   onCross,
		ch:        make(chan queued, queueFrames),
		done:      make(chan struct{}),
		quit:      make(chan struct{}),
	}
	go s.run()
	return s
}

func (s *Scorer) SetThreshold(t float32) {
	s.mu.Lock()
	s.threshold = t
	s.mu.Unlock()
}

const MaxMinFrames = 4

func (s *Scorer) SetMinFrames(n int) {
	s.mu.Lock()
	s.minFrames = min(n, MaxMinFrames)
	s.mu.Unlock()
}

func (s *Scorer) SetBargeThreshold(t float32, active func() bool) {
	s.mu.Lock()
	s.bargeThreshold = t
	s.speakerActive = active
	s.mu.Unlock()
}

func (s *Scorer) effectiveThresholdLocked() float32 {
	if s.bargeThreshold > 0 && s.speakerActive != nil && s.speakerActive() &&
		s.bargeThreshold < s.threshold {
		return s.bargeThreshold
	}
	return s.threshold
}

func (s *Scorer) Push(samples []int16) {
	cp := make([]int16, len(samples))
	copy(cp, samples)
	s.enqueue(cp, time.Now())
}

func (s *Scorer) PushBytes(b []byte) { s.PushBytesAt(b, time.Now()) }

func (s *Scorer) PushBytesAt(b []byte, at time.Time) {
	n := len(b) / 2
	if n == 0 {
		return
	}
	cp := make([]int16, n)
	for i := 0; i < n; i++ {
		cp[i] = int16(uint16(b[2*i]) | uint16(b[2*i+1])<<8)
	}
	s.enqueue(cp, at)
}

type Engine interface {
	Push(samples []int16) (score float32, ready bool, err error)

	Reset()
}

type queued struct {
	pcm []int16
	at  time.Time
}

func (s *Scorer) enqueue(cp []int16, at time.Time) {

	if s.closed.Load() {
		return
	}

	if now := time.Now(); !s.lastPush.IsZero() {
		if ms := now.Sub(s.lastPush).Milliseconds(); ms > s.maxGapMs.Load() {
			s.maxGapMs.Store(ms)
		}
		s.lastPush = now
	} else {
		s.lastPush = now
	}

	select {
	case s.ch <- queued{pcm: cp, at: at}:
	default:
		s.mu.Lock()
		s.stats.Drops++
		s.mu.Unlock()
	}
}

func (s *Scorer) Ready() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ready
}

func (s *Scorer) Reset() {
	s.mu.Lock()
	s.resetReq = true
	s.mu.Unlock()
}

func (s *Scorer) Drain() Stats {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := s.stats
	s.stats = Stats{}
	out.MaxInferMs = uint32(s.maxInferMs.Swap(0))
	out.MaxGapMs = uint32(s.maxGapMs.Swap(0))
	return out
}

func (s *Scorer) TakeBargeWindow() (peak, bar float32, frames uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	peak, bar, frames = s.bargePeak, s.bargeBar, s.bargeFrames
	s.bargePeak, s.bargeFrames = 0, 0
	return
}

func (s *Scorer) Info() string { return s.info }

func (s *Scorer) Phrase() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.phrase
}

func (s *Scorer) Close() {
	s.stop.Do(func() {
		s.closed.Store(true)
		close(s.quit)
		<-s.done

		if s.closer != nil {
			_ = s.closer.Close()
		}
	})
}

func (s *Scorer) run() {
	defer close(s.done)
	for {
		var q queued
		select {
		case <-s.quit:
			return
		case q = <-s.ch:
		}
		pcm := q.pcm
		s.mu.Lock()
		reset := s.resetReq
		s.resetReq = false
		threshold := s.effectiveThresholdLocked()
		lowBar := threshold < s.threshold
		s.stats.Threshold = threshold
		s.mu.Unlock()

		if reset {
			s.det.Reset()
			s.mu.Lock()
			s.ready = false
			s.prevAbove = false
			s.runAts = s.runAts[:0]
			s.hist, s.trace = nil, nil
			s.mu.Unlock()
		}

		t0 := time.Now()
		score, ready, err := s.det.Push(pcm)
		if err != nil {
			s.recordErr(err)
			continue
		}
		if !ready {
			s.mu.Lock()
			s.stats.Frames++
			s.stats.NotReady++
			s.mu.Unlock()
			continue
		}
		s.mu.Lock()
		s.ready = true
		s.mu.Unlock()

		now := time.Now()

		if ms := now.Sub(t0).Milliseconds(); ms > s.maxInferMs.Load() {
			s.maxInferMs.Store(ms)
		}
		s.mu.Lock()
		s.stats.Frames++
		if score > s.stats.MaxScore {
			s.stats.MaxScore = score
		}

		if lowBar {
			s.bargeFrames++
			s.bargeBar = threshold
			if score > s.bargePeak {
				s.bargePeak = score
			}
		}
		above := score >= threshold

		need := 1
		if !lowBar && s.minFrames > 1 {
			need = s.minFrames
		}
		if above && !lowBar {
			s.runAts = append(s.runAts, q.at)
			if len(s.runAts) > need {
				copy(s.runAts, s.runAts[len(s.runAts)-need:])
				s.runAts = s.runAts[:need]
			}
		} else {
			s.runAts = s.runAts[:0]
		}
		crossed := above && (!lowBar || s.prevAbove) && (lowBar || len(s.runAts) >= need) &&
			now.Sub(s.lastCross) >= s.refract

		at := q.at
		if crossed && lowBar {
			at = s.prevAt
		} else if crossed && need > 1 {
			at = s.runAts[0]
		}
		s.prevAbove, s.prevAt = above, q.at
		if crossed {
			s.stats.Crossings++
			s.lastCross = now
		}
		s.traceFrame(score, threshold, crossed)
		s.mu.Unlock()

		if crossed && s.onCross != nil {
			s.onCross(score, threshold, at)
		}
	}
}

func (s *Scorer) recordErr(err error) {
	s.mu.Lock()
	s.stats.Frames++
	s.stats.Errors++
	s.stats.LastErr = err.Error()
	s.mu.Unlock()
}
