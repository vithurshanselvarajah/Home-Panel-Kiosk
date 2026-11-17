package sendspin

import (
	"testing"
	"time"
)

// The first chunk of a stream must play on arrival.
//
// The server stamps a chunk with the moment it was sent, not the moment it
// is to be played, so the first chunk of a stream is already a moment old
// by the time it arrives. The anchor is laid from that chunk's timestamp,
// and an anchor laid from a stale timestamp is in the future: the audio is
// then a second ahead of the card, and the drift correction spends the next
// few seconds pulling it back a second at a time.
//
// That is the jitter at the start of a track, and it is gone when the
// offset is clamped at zero: the audio plays on arrival, as it does.
func TestTheFirstChunkPlaysOnArrival(t *testing.T) {
	p := newPlacement(syncedFilter())
	p.open()

	// The card is at frame zero and the output has no latency.
	p.position = func() (uint64, time.Duration) { return 0, 0 }
	p.latency = func() time.Duration { return 0 }

	// A chunk stamped a second ago: the server sent it a second ago.
	p.write(nowUs()-1_000_000, []int16{1000, -1000})

	p.mu.Lock()
	anchor := p.frame
	p.mu.Unlock()

	// The anchor must be at the card, not a second ahead of it.
	if anchor > outRate/10 {
		t.Fatalf("the anchor is %d frames, want it at the card: the chunk was placed in the future", anchor)
	}
}

// A chunk that is already late must not be dropped.
//
// The audio the server sends may arrive after the moment it was meant for,
// and it still belongs where it was meant to go.
func TestALateChunkIsStillPlaced(t *testing.T) {
	p := newPlacement(syncedFilter())
	p.open()
	p.position = func() (uint64, time.Duration) { return 0, 0 }
	p.latency = func() time.Duration { return 0 }

	// A chunk stamped two seconds ago.
	p.write(nowUs()-2_000_000, []int16{1000, -1000})

	if p.stats().Backwards != 0 {
		t.Fatalf("the late chunk was dropped: %d", p.stats().Backwards)
	}
	p.mu.Lock()
	queued := len(p.pcm)
	p.mu.Unlock()
	if queued == 0 {
		t.Fatal("the late chunk was not queued")
	}
}

// The anchor must not be laid before the speaker has started.
//
// A speaker that has not written to the card has no position and no
// latency reading, so an anchor laid against it is the send-ahead alone:
// the audio is a moment ahead of a card that has not started, and the
// drift correction spends the first seconds of the track pulling it back.
//
// The chunks that arrive in the meantime are held rather than dropped, and
// placed once the anchor is laid.
func TestTheAnchorWaitsForTheSpeaker(t *testing.T) {
	p := newPlacement(syncedFilter())
	p.open()

	started := false
	p.started = func() bool { return started }
	p.position = func() (uint64, time.Duration) { return 0, 0 }
	p.latency = func() time.Duration { return 0 }

	// The first chunk arrives before the speaker has started.
	p.write(nowUs(), []int16{1000, -1000, 2000, -2000})

	p.mu.Lock()
	anchored := p.anchored
	held := len(p.pending)
	p.mu.Unlock()

	if anchored {
		t.Fatal("the anchor was laid before the speaker started")
	}
	if held != 1 {
		t.Fatalf("%d chunks are held, want 1", held)
	}

	// The speaker starts, and the next chunk lays the anchor and releases
	// the one that was held.
	started = true
	p.write(nowUs()+1000, []int16{3000, -3000, 4000, -4000})

	p.mu.Lock()
	anchored = p.anchored
	held = len(p.pending)
	queued := len(p.pcm)
	p.mu.Unlock()

	if !anchored {
		t.Fatal("the anchor was not laid once the speaker started")
	}
	if held != 0 {
		t.Fatalf("%d chunks are still held, want none", held)
	}
	if queued == 0 {
		t.Fatal("nothing is queued: the chunk that was held was lost")
	}
}
