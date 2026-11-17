package sendspin

import (
	"testing"
	"time"
)

// The clock filter is fed Unix microseconds, and the audio chunks are
// stamped in Unix microseconds too.
//
// The server's clock and ours differ by whatever their clocks differ by,
// and the filter works that difference out. If the client's own timestamps
// were in a different domain from the server's — the time since the process
// started, rather than the time since the epoch — the difference between
// them would be decades rather than the drift between two clocks, and every
// chunk would be placed that far from where it belongs.
//
// This is what happened: the audio was placed half an hour ahead of the
// card, so the card played silence and the queue grew without bound.
func TestTheClockIsInUnixMicroseconds(t *testing.T) {
	now := nowUs()
	wall := time.Now().UnixMicro()

	// The two must be within a second of each other: they are the same
	// clock, read two ways.
	if d := wall - now; d > 1_000_000 || d < -1_000_000 {
		t.Fatalf("the client clock is %d us from the wall clock, want within a second", d)
	}
}

// The filter must map a server timestamp onto our wall clock.
//
// A server timestamp of now, fed through the filter, must come back as
// about now. Before the fix it came back as the server's timestamp minus
// the filter's offset, which was the epoch difference rather than the
// difference between two clocks.
func TestTheFilterMapsTheServerClockOntoOurs(t *testing.T) {
	f := newTimeFilter()

	// A clock that runs 250 ms ahead of ours, exchanged the way the
	// protocol does it.
	drift := int64(250_000)
	// The exchanges are a second apart, so the filter sees the clock moving.
	for i := int64(1); i <= 3; i++ {
		clientSent := nowUs() + i*1_000_000
		serverReceived := clientSent + drift
		serverTransmitted := serverReceived + 1000
		clientReceived := clientSent + 2000

		offset := float64((serverReceived-clientSent)+(serverTransmitted-clientReceived)) / 2
		delay := float64((clientReceived-clientSent)-(serverTransmitted-serverReceived)) / 2
		f.update(pyRound(offset), pyRound(delay), clientReceived)
	}

	if !f.synchronized() {
		t.Fatal("the filter did not converge on three exchanges")
	}

	// A timestamp of the server's now maps to about our now.
	//
	// The filter is smooth, so it lands within a millisecond or two of the
	// truth rather than exactly on it, and it lags the exchange by the round
	// trip. Fifty milliseconds is a wide bound and still a tight one.
	serverNow := nowUs() + drift
	mapped := f.clientTime(serverNow)
	if d := mapped - nowUs(); d > 50_000 || d < -50_000 {
		t.Fatalf("the server's now maps to %d us from our now, want within 50 ms", d)
	}

	// And the other way: our now maps to the server's now.
	server := f.serverTime(nowUs())
	if d := server - serverNow; d > 50_000 || d < -50_000 {
		t.Fatalf("our now maps to %d us from the server's now, want within 50 ms", d)
	}
}
