package speaker

import (
	"sync"
)

// The external playback.
//
// This project's device program owns the sound card through its own
// backend, so that a recording can stand in for the card in the tests.
// The ported speaker normally opens the card itself; with an external
// playback set, it writes to that instead.
//
// The external playback is the same interface the card is: it takes
// interleaved Rate, Channels, S16_LE frames and its writes block at the
// pace the output plays them.
type ExternalPlayback interface {
	// Write plays one block of audio.
	Write(p []byte) (int, error)
	// Close stops the playback.
	Close() error
	// Delay is how many frames written are still to be played.
	Delay() (int, error)
}

var (
	externalMu sync.Mutex
	externalPb ExternalPlayback
)

// SetExternalPlayback sets the playback the speaker writes to, or clears it
// with nil.
//
// It is set before the speaker is started, and it is what the speaker
// holds instead of the card.
func SetExternalPlayback(pb ExternalPlayback) {
	externalMu.Lock()
	externalPb = pb
	externalMu.Unlock()
}

// externalPlayback returns the external playback, or nil.
func externalPlayback() ExternalPlayback {
	externalMu.Lock()
	defer externalMu.Unlock()
	return externalPb
}
