package sendspin

import (
	"testing"
	"time"
)

func TestTheClockIsInUnixMicroseconds(t *testing.T) {
	now := nowUs()
	wall := time.Now().UnixMicro()

	if d := wall - now; d > 1_000_000 || d < -1_000_000 {
		t.Fatalf("the client clock is %d us from the wall clock, want within a second", d)
	}
}

func TestTheFilterMapsTheServerClockOntoOurs(t *testing.T) {
	f := newTimeFilter()

	drift := int64(250_000)

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

	serverNow := nowUs() + drift
	mapped := f.clientTime(serverNow)
	if d := mapped - nowUs(); d > 50_000 || d < -50_000 {
		t.Fatalf("the server's now maps to %d us from our now, want within 50 ms", d)
	}

	server := f.serverTime(nowUs())
	if d := server - serverNow; d > 50_000 || d < -50_000 {
		t.Fatalf("our now maps to %d us from the server's now, want within 50 ms", d)
	}
}
