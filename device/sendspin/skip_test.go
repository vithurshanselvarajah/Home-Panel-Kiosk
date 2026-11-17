package sendspin

import (
	"testing"
	"time"
)

// A skip must not stop the music.
//
// The server sends the audio of the new track with timestamps that belong
// to the new track's timeline. The placement's anchor was laid against the
// old one, so a chunk that arrives far from the anchor is a new timeline
// and the anchor is laid again — against the card, so the audio lands where
// the speaker is.
//
// Without this the chunk was placed millions of frames ahead of the card,
// dropped as too far ahead, and the music stopped while the server thought
// it was playing.
func TestASkipLaysTheAnchorAgain(t *testing.T) {
	p := newPlacement(syncedFilter())
	p.open()

	// The speaker is at frame zero and the output has no latency, so a
	// chunk stamped now lands at frame zero.
	p.position = func() (uint64, time.Duration) { return 0, 0 }
	p.latency = func() time.Duration { return 0 }

	// The first chunk of the first track.
	p.write(nowUs(), []int16{1000, -1000, 2000, -2000})

	// The card plays it.
	out := make([]int16, 4)
	p.Render(0, out)
	for i, want := range []int16{1000, -1000, 2000, -2000} {
		if out[i] != want {
			t.Fatalf("sample %d is %d, want %d", i, out[i], want)
		}
	}

	// A skip.
	//
	// The server's clock does not jump: it keeps telling the time, and
	// the audio it sends after the skip is stamped with the time it was
	// sent. What the server does send is a clear, and the placement is
	// flushed: the queue and the anchor go, and the next chunk lays a new
	// anchor against the card.
	//
	// The flush is the server's signal. The re-anchor is for the case
	// where it does not come, and it is modelled here as a chunk whose
	// timestamp the anchor was not laid against.
	p.flush()
	after := nowUs()
	p.write(after, []int16{3000, -3000, 4000, -4000})

	// The chunk must not have been dropped, and the placement must have
	// laid a new anchor.
	if p.stats().Backwards != 0 {
		t.Fatalf("the chunk after the skip was dropped: %d", p.stats().Backwards)
	}

	// And the card must be able to play it: the placement's anchor has to
	// be near the card again.
	p.mu.Lock()
	anchor := p.frame
	p.mu.Unlock()
	if anchor < 0 || anchor > outRate {
		t.Fatalf("the anchor is %d after the skip, want it near the card", anchor)
	}
}

// A skip within a track must NOT lay a new anchor.
//
// The server keeps one timeline across a track, and a chunk that is a
// second or two from the anchor is the same track a moment later. Laying a
// new anchor for every such chunk would move the audio a little each time,
// and it would drift.
func TestASmallJumpKeepsTheAnchor(t *testing.T) {
	p := newPlacement(syncedFilter())
	p.open()
	p.position = func() (uint64, time.Duration) { return 0, 0 }
	p.latency = func() time.Duration { return 0 }

	p.write(nowUs(), []int16{1000, -1000})
	p.mu.Lock()
	first := p.frame
	p.mu.Unlock()

	// A second later in the server's clock.
	p.write(nowUs()+1_000_000, []int16{2000, -2000})

	p.mu.Lock()
	second := p.frame
	p.mu.Unlock()

	if second != first {
		t.Fatalf("the anchor moved from %d to %d over a second, want it kept", first, second)
	}
}

// The anchor must not be clamped at zero.
//
// The audio the server sends may already be late, and the frame it belongs
// at is then before the speaker started writing — a negative frame index.
// Holding the anchor unsigned and clamping it at zero threw the whole
// offset away: the anchor was then nothing like the frame the audio
// belonged at, and the drift correction drove it below zero.
func TestTheAnchorMayBeNegative(t *testing.T) {
	p := newPlacement(syncedFilter())
	p.open()

	// The speaker has written nothing, and the audio is a second late.
	p.position = func() (uint64, time.Duration) { return 0, 0 }
	p.latency = func() time.Duration { return 0 }

	late := nowUs() - 1_000_000
	p.write(late, []int16{1000, -1000})

	p.mu.Lock()
	anchor := p.frame
	p.mu.Unlock()

	// The anchor must be where the audio belongs: a second before the
	// card's first frame, not at the card's first frame.
	if anchor > 0 {
		t.Fatalf("the anchor is %d, want it before the card for audio that is a second late", anchor)
	}
}
