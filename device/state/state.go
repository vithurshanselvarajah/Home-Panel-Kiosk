package state

import (
	"sync"
	"time"
)

type Phase string

const (
	PhaseUnknown Phase = "unknown"

	PhaseIdle Phase = "idle"

	PhaseListening Phase = "listening"

	PhaseThinking Phase = "thinking"

	PhaseSpeaking Phase = "speaking"

	PhaseMuted Phase = "muted"

	PhaseDisconnected Phase = "disconnected"

	PhaseReady Phase = "ready"
)

type ListenMode string

const (
	ListenUnknown ListenMode = "unknown"

	ListenLocal ListenMode = "local"

	ListenStream ListenMode = "stream"

	ListenDegraded ListenMode = "degraded"
)

func (m ListenMode) Streams() bool { return m == ListenStream }

type Snapshot struct {
	Phase Phase

	PhaseReason string

	Listen ListenMode

	ListenReason string

	Muted bool

	Speaking bool

	Thinking bool

	Listening bool

	Volume int

	LinkUp bool

	WakeWordReady bool

	WakeWordEnabled bool

	WakeWord string

	LastError string

	UpdatedAt time.Time
}

type Tracker struct {
	mu        sync.RWMutex
	current   Snapshot
	listeners []func(Snapshot)
}

func NewTracker() *Tracker {
	return &Tracker{
		current: Snapshot{
			Phase:     PhaseUnknown,
			Listen:    ListenUnknown,
			UpdatedAt: time.Now(),
		},
	}
}

func (t *Tracker) Snapshot() Snapshot {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.current
}

func (t *Tracker) OnChange(fn func(Snapshot)) {
	t.mu.Lock()
	t.listeners = append(t.listeners, fn)
	t.mu.Unlock()
}

func (t *Tracker) update(mutate func(*Snapshot)) {
	t.mu.Lock()
	before := t.current
	mutate(&t.current)
	t.current.UpdatedAt = time.Now()
	after := t.current
	listeners := append([]func(Snapshot){}, t.listeners...)
	t.mu.Unlock()

	before.UpdatedAt = after.UpdatedAt
	if before == after {
		return
	}
	for _, fn := range listeners {
		fn(after)
	}
}

func (t *Tracker) SetPhase(phase Phase, reason string) {
	t.update(func(s *Snapshot) {
		s.Phase = phase
		s.PhaseReason = reason
	})
}

func (t *Tracker) SetListen(mode ListenMode, reason string) {
	t.update(func(s *Snapshot) {
		s.Listen = mode
		s.ListenReason = reason
	})
}

func (t *Tracker) SetMuted(muted bool) {
	t.update(func(s *Snapshot) {
		s.Muted = muted
		if muted {
			s.Phase = PhaseMuted
			s.PhaseReason = "the panel's mute switch is on"
		} else if s.Phase == PhaseMuted {
			s.Phase = PhaseIdle
			s.PhaseReason = ""
		}
	})
}

func (t *Tracker) SetSpeaking(speaking bool) {
	t.update(func(s *Snapshot) {
		s.Speaking = speaking
	})
}

func (t *Tracker) SetThinking(thinking bool) {
	t.update(func(s *Snapshot) {
		s.Thinking = thinking
	})
}

func (t *Tracker) SetListening(listening bool) {
	t.update(func(s *Snapshot) {
		s.Listening = listening
	})
}

func (t *Tracker) SetVolume(level int) {
	t.update(func(s *Snapshot) {
		s.Volume = level
	})
}

func (t *Tracker) SetLink(up bool) {
	t.update(func(s *Snapshot) {
		s.LinkUp = up
		if up {
			if s.Phase == PhaseDisconnected || s.Phase == PhaseUnknown {
				s.Phase = PhaseReady
				s.PhaseReason = "home assistant is connected"
			}
			return
		}
		s.Phase = PhaseDisconnected
		s.PhaseReason = "the link to home assistant is down"
	})
}

func (t *Tracker) SetWakeWord(model string, ready, enabled bool) {
	t.update(func(s *Snapshot) {
		s.WakeWord = model
		s.WakeWordReady = ready
		s.WakeWordEnabled = enabled
	})
}

func (t *Tracker) SetError(err error) {
	t.update(func(s *Snapshot) {
		if err == nil {
			s.LastError = ""
			return
		}
		s.LastError = err.Error()
	})
}
