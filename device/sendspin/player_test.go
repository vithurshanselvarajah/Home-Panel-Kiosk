package sendspin

import "testing"

func TestPlayerBoundsAudioBufferedAcrossOutputStalls(t *testing.T) {
	p := newPlayer(newTimeFilter())
	p.push(1_000_000, make([]int16, maxBufferedSamples-10))
	p.push(11_000_000, make([]int16, 20))
	if got := p.buffered().Seconds(); got > 10 {
		t.Fatalf("player buffered %.2fs after an output stall; limit is 10s", got)
	}
	if p.stats().BufferOverruns != 1 {
		t.Fatalf("player recorded %d overruns, want 1", p.stats().BufferOverruns)
	}
	p.push(12_000_000, make([]int16, maxBufferedSamples+100))
	if got := p.buffered().Seconds(); got > 10 {
		t.Fatalf("player buffered %.2fs after an oversized chunk; limit is 10s", got)
	}
	if p.stats().BufferOverruns != 2 {
		t.Fatalf("player recorded %d overruns, want 2", p.stats().BufferOverruns)
	}
	if got := p.q[0].ts; got != 12_000_000+100*1_000_000/outRate {
		t.Fatalf("trimmed chunk starts at %d, want timestamp advanced to %d", got, 12_000_000+100*1_000_000/outRate)
	}
}
