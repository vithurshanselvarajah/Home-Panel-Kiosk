package mic

import (
	"io"
	"sync"
)

// The external capture.
//
// This project's device program owns the sound card through its own
// backend, so that a recording can stand in for the card in the tests.
// The ported microphone normally opens the card itself; with an external
// capture set, it reads that instead.
//
// The external capture gives whole frames at the card's own rate and
// width, the same as the card does, so that the mixing, the cancellation
// and the levelling all work on it exactly as they do on the hardware.
type ExternalCapture interface {
	// Read fills buf with the card's own frames.
	Read(buf []byte) (int, error)
	// Close releases the microphone.
	Close() error
}

var (
	externalMu sync.Mutex
	externalPc ExternalCapture
)

// SetExternalCapture sets the capture the microphone reads, or clears it
// with nil.
//
// It is set before the microphone is started, and it is what the
// microphone holds instead of the card.
func SetExternalCapture(pc ExternalCapture) {
	externalMu.Lock()
	externalPc = pc
	externalMu.Unlock()
}

// externalCapture returns the external capture, or nil.
func externalCapture() ExternalCapture {
	externalMu.Lock()
	defer externalMu.Unlock()
	return externalPc
}

// The unused import is kept honest.
var _ = io.EOF
