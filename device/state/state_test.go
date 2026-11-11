package state

import (
	"testing"
	"time"
)

func TestTrackerStartsUnknown(t *testing.T) {
	tracker := NewTracker()
	snapshot := tracker.Snapshot()

	if snapshot.Phase != PhaseUnknown {
		t.Errorf("the phase was %q, want %q", snapshot.Phase, PhaseUnknown)
	}
	if snapshot.Listen != ListenUnknown {
		t.Errorf("the listen mode was %q, want %q", snapshot.Listen, ListenUnknown)
	}
	// The opposite of unknown is a lie. A panel that has just started is
	// not muted and is not speaking.
	if snapshot.Muted || snapshot.Speaking || snapshot.Thinking || snapshot.Listening {
		t.Error("a new tracker claims to be doing something")
	}
}

func TestTrackerNotifiesOnChange(t *testing.T) {
	tracker := NewTracker()

	changes := 0
	tracker.OnChange(func(Snapshot) { changes++ })

	tracker.SetSpeaking(true)
	if changes != 1 {
		t.Errorf("the listener was called %d times, want 1", changes)
	}

	// The same value again is not a change.
	tracker.SetSpeaking(true)
	if changes != 1 {
		t.Errorf("the listener was called %d times after an unchanged value, want 1", changes)
	}

	tracker.SetSpeaking(false)
	if changes != 2 {
		t.Errorf("the listener was called %d times, want 2", changes)
	}
}

func TestMuteSetsThePhase(t *testing.T) {
	tracker := NewTracker()

	tracker.SetMuted(true)
	if got := tracker.Snapshot().Phase; got != PhaseMuted {
		t.Errorf("the phase was %q while muted, want %q", got, PhaseMuted)
	}

	tracker.SetMuted(false)
	if got := tracker.Snapshot().Phase; got != PhaseIdle {
		t.Errorf("the phase was %q after unmuting, want %q", got, PhaseIdle)
	}
}

func TestLinkDownDisconnects(t *testing.T) {
	tracker := NewTracker()

	tracker.SetLink(true)
	if got := tracker.Snapshot().Phase; got != PhaseReady {
		t.Errorf("the phase was %q with the link up, want %q", got, PhaseReady)
	}

	tracker.SetLink(false)
	snapshot := tracker.Snapshot()
	if snapshot.Phase != PhaseDisconnected {
		t.Errorf("the phase was %q with the link down, want %q", snapshot.Phase, PhaseDisconnected)
	}
	if snapshot.LinkUp {
		t.Error("the link is up with the link down")
	}
}

func TestLinkUpRecoversThePhase(t *testing.T) {
	tracker := NewTracker()

	tracker.SetLink(true)
	tracker.SetLink(false)
	tracker.SetLink(true)

	if got := tracker.Snapshot().Phase; got != PhaseReady {
		t.Errorf("the phase was %q after the link came back, want %q", got, PhaseReady)
	}
}

func TestOnlyTheStreamingModeStreams(t *testing.T) {
	// This is the question a privacy statement has to answer, so it is
	// worth being explicit about which mode answers it yes.
	if ListenLocal.Streams() {
		t.Error("the local mode streams")
	}
	if ListenDegraded.Streams() {
		t.Error("the degraded mode streams")
	}
	if !ListenStream.Streams() {
		t.Error("the streaming mode does not stream")
	}
}

func TestWakeWordIsRecorded(t *testing.T) {
	tracker := NewTracker()

	tracker.SetWakeWord("hey jarvis", true, true)
	snapshot := tracker.Snapshot()
	if snapshot.WakeWord != "hey jarvis" {
		t.Errorf("the wake word was %q, want %q", snapshot.WakeWord, "hey jarvis")
	}
	if !snapshot.WakeWordReady || !snapshot.WakeWordEnabled {
		t.Error("the wake word is not recorded as ready and enabled")
	}
}

func TestSnapshotIsACopy(t *testing.T) {
	tracker := NewTracker()
	tracker.SetVolume(50)

	snapshot := tracker.Snapshot()
	snapshot.Volume = 100

	if got := tracker.Snapshot().Volume; got != 50 {
		t.Errorf("the volume was %d after the snapshot was changed, want 50", got)
	}
}

func TestUpdatedAtMoves(t *testing.T) {
	tracker := NewTracker()
	before := tracker.Snapshot().UpdatedAt

	time.Sleep(time.Millisecond)
	tracker.SetVolume(10)

	if !tracker.Snapshot().UpdatedAt.After(before) {
		t.Error("the timestamp did not move")
	}
}
