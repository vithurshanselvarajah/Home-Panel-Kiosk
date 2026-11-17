package sendspin

import (
	"testing"
	"time"
)

func TestTheClosedPlacementGivesTheSpeakerBack(t *testing.T) {
	p := newPlacement(syncedFilter())

	var attached, detached bool
	p.SetAttach(func() { attached = true }, func() { detached = true })

	p.openAgain()
	if !attached {
		t.Fatal("the placement did not take the speaker")
	}

	p.close()
	if !detached {
		t.Fatal("the placement did not give the speaker back")
	}
	if p.Ready() {
		t.Error("the placement is closed, and it says it is ready")
	}
}

func TestTheUnopenedPlacementGivesNothingBack(t *testing.T) {
	p := newPlacement(syncedFilter())

	var detached bool
	p.SetAttach(func() {}, func() { detached = true })

	p.close()
	if detached {
		t.Error("the placement gave the speaker back, and it never took it")
	}
}

func TestTheHeldAudioIsStillPlaced(t *testing.T) {
	p := newPlacement(syncedFilter())
	p.open()

	started := true
	p.started = func() bool { return started }
	p.position = func() (uint64, time.Duration) { return 0, 0 }
	p.latency = func() time.Duration { return 0 }

	p.hold(true)

	p.write(nowUs(), []int16{1000, -1000, 2000, -2000})

	p.mu.Lock()
	queued := len(p.pcm)
	p.mu.Unlock()

	if queued == 0 {
		t.Error("the audio was dropped while it was held, and the buffer is cold for the play")
	}
}
