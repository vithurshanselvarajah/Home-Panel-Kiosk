package sendspin

import (
	"testing"
	"time"
)

// The placement gives the speaker back when it is closed.
//
// The placement attaches itself to the speaker when a stream opens and
// detaches when it is closed. A placement that is closed without detaching
// leaves the speaker pulling from a source that will never say anything
// again, and the speaker then says the panel is sounding whenever the
// source has been quiet for less than two seconds.
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

// The placement does not detach when it was never attached.
//
// A placement that was closed before any stream opened has nothing to
// give back, and saying that it gave it back would take the speaker away
// from whoever has it.
func TestTheUnopenedPlacementGivesNothingBack(t *testing.T) {
	p := newPlacement(syncedFilter())

	var detached bool
	p.SetAttach(func() {}, func() { detached = true })

	p.close()
	if detached {
		t.Error("the placement gave the speaker back, and it never took it")
	}
}

// The hold does not stop the audio being placed.
//
// The hold gates the rendering, not the writing: what the server sends
// while the audio is held is still placed, so the buffer is warm and the
// anchor is live, and a play starts at once.
//
// Dropping it instead would leave the placement with nothing to play and
// no anchor, and the audio would have to be laid again from scratch
// against the card — which is the gap at the start of every resumed
// track.
func TestTheHeldAudioIsStillPlaced(t *testing.T) {
	p := newPlacement(syncedFilter())
	p.open()

	started := true
	p.started = func() bool { return started }
	p.position = func() (uint64, time.Duration) { return 0, 0 }
	p.latency = func() time.Duration { return 0 }

	// The audio is held: the server has paused.
	p.hold(true)

	// A chunk arrives while it is held.
	p.write(nowUs(), []int16{1000, -1000, 2000, -2000})

	p.mu.Lock()
	queued := len(p.pcm)
	p.mu.Unlock()

	if queued == 0 {
		t.Error("the audio was dropped while it was held, and the buffer is cold for the play")
	}
}
