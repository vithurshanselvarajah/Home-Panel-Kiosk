package sendspin

import (
	"testing"
	"time"
)

func TestASkipLaysTheAnchorAgain(t *testing.T) {
	p := newPlacement(syncedFilter())
	p.open()

	p.position = func() (uint64, time.Duration) { return 0, 0 }
	p.latency = func() time.Duration { return 0 }

	p.write(nowUs(), []int16{1000, -1000, 2000, -2000})

	out := make([]int16, 4)
	p.Render(0, out)
	for i, want := range []int16{1000, -1000, 2000, -2000} {
		if out[i] != want {
			t.Fatalf("sample %d is %d, want %d", i, out[i], want)
		}
	}

	p.flush()
	after := nowUs()
	p.write(after, []int16{3000, -3000, 4000, -4000})

	if p.stats().Backwards != 0 {
		t.Fatalf("the chunk after the skip was dropped: %d", p.stats().Backwards)
	}

	p.mu.Lock()
	anchor := p.frame
	p.mu.Unlock()
	if anchor < 0 || anchor > outRate {
		t.Fatalf("the anchor is %d after the skip, want it near the card", anchor)
	}
}

func TestASmallJumpKeepsTheAnchor(t *testing.T) {
	p := newPlacement(syncedFilter())
	p.open()
	p.position = func() (uint64, time.Duration) { return 0, 0 }
	p.latency = func() time.Duration { return 0 }

	p.write(nowUs(), []int16{1000, -1000})
	p.mu.Lock()
	first := p.frame
	p.mu.Unlock()

	p.write(nowUs()+1_000_000, []int16{2000, -2000})

	p.mu.Lock()
	second := p.frame
	p.mu.Unlock()

	if second != first {
		t.Fatalf("the anchor moved from %d to %d over a second, want it kept", first, second)
	}
}

func TestTheAnchorMayBeNegative(t *testing.T) {
	p := newPlacement(syncedFilter())
	p.open()

	p.position = func() (uint64, time.Duration) { return 0, 0 }
	p.latency = func() time.Duration { return 0 }

	late := nowUs() - 1_000_000
	p.write(late, []int16{1000, -1000})

	p.mu.Lock()
	anchor := p.frame
	p.mu.Unlock()

	if anchor > 0 {
		t.Fatalf("the anchor is %d, want it before the card for audio that is a second late", anchor)
	}
}
