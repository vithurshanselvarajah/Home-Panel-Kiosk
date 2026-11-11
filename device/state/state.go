// Package state is what an application reads to know what the panel is
// doing.
//
// The panel is a voice satellite to Home Assistant, so its state is the
// state of a voice satellite: is it listening, is it thinking, is it
// speaking, is it muted. This package derives that state from what
// actually happens — a wake word firing, a turn starting, a reply
// playing — so an application never has to interpret the protocol itself.
//
// Every state is reported with the reason it is in that state, and every
// state that is not yet known is reported as unknown rather than as its
// opposite. A panel that has just started is not muted, and saying so
// would be a lie the application might act on.
package state

import (
	"sync"
	"time"
)

// Phase is the coarse state of the voice assistant.
type Phase string

const (
	// PhaseUnknown — nothing has happened yet, or the link dropped and the
	// last known state cannot be trusted.
	PhaseUnknown Phase = "unknown"
	// PhaseIdle — connected, not doing anything.
	PhaseIdle Phase = "idle"
	// PhaseListening — the microphone is open and the user may be speaking.
	PhaseListening Phase = "listening"
	// PhaseThinking — the wake word fired and Home Assistant is working.
	PhaseThinking Phase = "thinking"
	// PhaseSpeaking — the response is playing.
	PhaseSpeaking Phase = "speaking"
	// PhaseMuted — the panel's own mute switch is on.
	PhaseMuted Phase = "muted"
	// PhaseDisconnected — the link to Home Assistant is down.
	PhaseDisconnected Phase = "disconnected"
	// PhaseReady — the link is up and the panel is known to Home Assistant.
	PhaseReady Phase = "ready"
)

// ListenMode is what the panel is doing with its microphone.
type ListenMode string

const (
	// ListenUnknown — the panel has not said.
	ListenUnknown ListenMode = "unknown"
	// ListenLocal — the panel scores its own wake word and sends nothing
	// until it fires. No audio leaves the panel until then.
	ListenLocal ListenMode = "local"
	// ListenStream — the panel streams continuously to Home Assistant.
	ListenStream ListenMode = "stream"
	// ListenDegraded — the panel cannot run its own wake word, so Home
	// Assistant does the detecting and the audio streams.
	ListenDegraded ListenMode = "degraded"
)

// Streams reports whether audio is leaving the panel continuously.
//
// It is true only for the streaming mode, and false for the local mode —
// where the panel sends nothing until its wake word fires. It is the one
// question a privacy statement has to answer, so it is answered from what
// the panel reported rather than from what it was configured to do.
func (m ListenMode) Streams() bool { return m == ListenStream }

// Snapshot is the whole observable state of the panel at one instant.
//
// It is a value, not a live view: reading it never blocks and never races
// with the link.
type Snapshot struct {
	// Phase is the coarse state.
	Phase Phase
	// PhaseReason says why the phase is what it is, in words an
	// application can show a person.
	PhaseReason string

	// Listen is what the panel is doing with its microphone.
	Listen ListenMode
	// ListenReason explains a degraded or unknown listen mode.
	ListenReason string

	// Muted is the panel's own mute switch.
	Muted bool

	// Speaking is true while a response is playing.
	Speaking bool
	// Thinking is true while Home Assistant is working on a reply.
	Thinking bool
	// Listening is true while the microphone is open.
	Listening bool

	// Volume is the last volume level the panel reported.
	Volume int

	// LinkUp is whether the link to Home Assistant is up.
	LinkUp bool
	// WakeWordReady is whether the local wake word can run.
	WakeWordReady bool
	// WakeWordEnabled is whether the local wake word is switched on.
	WakeWordEnabled bool
	// WakeWord is the name of the wake word the panel is listening for.
	WakeWord string

	// LastError is the most recent error, if any.
	LastError string

	// UpdatedAt is when this snapshot was taken.
	UpdatedAt time.Time
}

// Tracker holds the panel state and tells subscribers when it changes.
//
// Nothing in the application has to parse a protocol message to know what
// the panel is doing.
type Tracker struct {
	mu        sync.RWMutex
	current   Snapshot
	listeners []func(Snapshot)
}

// NewTracker returns a tracker in the unknown state.
func NewTracker() *Tracker {
	return &Tracker{
		current: Snapshot{
			Phase:     PhaseUnknown,
			Listen:    ListenUnknown,
			UpdatedAt: time.Now(),
		},
	}
}

// Snapshot returns the current state.
func (t *Tracker) Snapshot() Snapshot {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.current
}

// OnChange registers a listener. It is called with every change, on the
// goroutine that made the change, so a listener that blocks blocks the
// link — hand work to another goroutine rather than doing it here.
func (t *Tracker) OnChange(fn func(Snapshot)) {
	t.mu.Lock()
	t.listeners = append(t.listeners, fn)
	t.mu.Unlock()
}

// update applies a change and notifies the listeners if anything moved.
func (t *Tracker) update(mutate func(*Snapshot)) {
	t.mu.Lock()
	before := t.current
	mutate(&t.current)
	t.current.UpdatedAt = time.Now()
	after := t.current
	listeners := append([]func(Snapshot){}, t.listeners...)
	t.mu.Unlock()

	// The timestamp is compared out of the way. It moves on every update,
	// so including it would make every update look like a change and the
	// listeners would be called for nothing.
	before.UpdatedAt = after.UpdatedAt
	if before == after {
		return
	}
	for _, fn := range listeners {
		fn(after)
	}
}

// SetPhase records the coarse state and its reason.
func (t *Tracker) SetPhase(phase Phase, reason string) {
	t.update(func(s *Snapshot) {
		s.Phase = phase
		s.PhaseReason = reason
	})
}

// SetListen records what the panel is doing with its microphone.
func (t *Tracker) SetListen(mode ListenMode, reason string) {
	t.update(func(s *Snapshot) {
		s.Listen = mode
		s.ListenReason = reason
	})
}

// SetMuted records the panel's mute switch.
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

// SetSpeaking records that a response is playing.
func (t *Tracker) SetSpeaking(speaking bool) {
	t.update(func(s *Snapshot) {
		s.Speaking = speaking
	})
}

// SetThinking records that Home Assistant is working on a reply.
func (t *Tracker) SetThinking(thinking bool) {
	t.update(func(s *Snapshot) {
		s.Thinking = thinking
	})
}

// SetListening records that the microphone is open.
func (t *Tracker) SetListening(listening bool) {
	t.update(func(s *Snapshot) {
		s.Listening = listening
	})
}

// SetVolume records the last reported volume level.
func (t *Tracker) SetVolume(level int) {
	t.update(func(s *Snapshot) {
		s.Volume = level
	})
}

// SetLink records whether the link to Home Assistant is up.
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

// SetWakeWord records what the local wake word is doing.
//
// The three are set together because they answer one question between
// them: is the panel going to hear the wake word, and if not, why not.
func (t *Tracker) SetWakeWord(model string, ready, enabled bool) {
	t.update(func(s *Snapshot) {
		s.WakeWord = model
		s.WakeWordReady = ready
		s.WakeWordEnabled = enabled
	})
}

// SetError records the most recent error.
func (t *Tracker) SetError(err error) {
	t.update(func(s *Snapshot) {
		if err == nil {
			s.LastError = ""
			return
		}
		s.LastError = err.Error()
	})
}
