package sendspin

import "testing"

func TestPlayerBoundsAudioBufferedAcrossOutputStalls(t *testing.T) {
	p := newPlayer(newTimeFilter())
	p.push(1_000_000, make([]int16, maxBufferedSamples-10))
	p.push(11_000_000, make([]int16, 20))
	if got := p.buffered().Seconds(); got > 32 {
		t.Fatalf("player buffered %.2fs after an output stall; limit is 32s", got)
	}
	if p.stats().BufferOverruns != 1 {
		t.Fatalf("player recorded %d overruns, want 1", p.stats().BufferOverruns)
	}
	p.push(12_000_000, make([]int16, maxBufferedSamples+100))
	if got := p.buffered().Seconds(); got > 32 {
		t.Fatalf("player buffered %.2fs after an oversized chunk; limit is 32s", got)
	}
	if p.stats().BufferOverruns != 2 {
		t.Fatalf("player recorded %d overruns, want 2", p.stats().BufferOverruns)
	}
	if got := p.q[0].ts; got != 12_000_000+100*1_000_000/outRate {
		t.Fatalf("trimmed chunk starts at %d, want timestamp advanced to %d", got, 12_000_000+100*1_000_000/outRate)
	}
}

// An overrun must drop the oldest audio, not everything.
//
// Throwing the whole queue away leaves the head of it far in the future —
// the server keeps its own queue on its own play timeline — and the player
// then sits in silence until the queue refills. That silence is what the
// music stopping was.
func TestPlayerOverrunDropsOldestAudioNotAll(t *testing.T) {
	p := newPlayer(newTimeFilter())
	// A queue that is already over the limit, then more on top of it.
	p.push(1_000_000, make([]int16, maxBufferedSamples))
	p.push(2_000_000, make([]int16, maxBufferedSamples))

	if len(p.q) == 0 {
		t.Fatal("the player threw away the whole queue")
	}
	// The newest chunk has to survive: it is the audio that is still to
	// come, and dropping it is the silence.
	if got := p.q[len(p.q)-1].ts; got != 2_000_000 {
		t.Fatalf("the newest chunk is at %d, want 2000000", got)
	}
	if got := p.buffered().Seconds(); got > 32 {
		t.Fatalf("player buffered %.2fs after an overrun; limit is 32s", got)
	}
}

// The server's queue must always fit inside the player's own limit.
//
// The server is told a capacity in WIRE bytes and counts its compressed
// chunks against it, so it can hold far more decoded audio than the byte
// figure suggests. If the player's limit is below what the server can
// queue, every stream ends in an overrun and a gap in the music.
func TestPlayerLimitIsAboveWhatTheServerCanQueue(t *testing.T) {
	// The spec's own ceiling on a player's outstanding duration.
	const specOutstanding = 30
	if maxBufferedSamples/outRate <= specOutstanding {
		t.Fatalf("the player's limit is %ds, which is not above the %ds the server may hold",
			maxBufferedSamples/outRate, specOutstanding)
	}
	// And the capacity the server is told has to be a byte count for the
	// codec on the wire, not a count of PCM bytes.
	if bufferCapacity*16 != 6*outRate*outChannels*outBits {
		t.Fatalf("the buffer capacity is %d bytes, which is not six seconds of %d-bit mono",
			bufferCapacity, outBits)
	}
}
