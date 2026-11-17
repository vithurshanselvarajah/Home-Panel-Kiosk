package audio

import (
	"context"
	"io"
	"time"
)

// The audio interfaces the device program is written against.
//
// They are small on purpose. The point of an interface here is to say what
// the device program needs from a sound card, not to describe a sound card.
//
// The samples are signed 16-bit little-endian PCM at the rate that is asked
// for. Nothing here chooses a platform audio system or a device: that is
// the backend's job, and the backend is the only part that knows.

// Stream is the system supplied PCM input and output.
type Stream interface {
	// Capture opens the microphone at the given rate.
	Capture(context.Context, int) (io.ReadCloser, error)
	// Playback opens the speaker at the given rate.
	Playback(context.Context, int) (io.WriteCloser, error)
	// Describe says what the backend is, in a word or two.
	Describe() string
}

// PlaybackDrainer is implemented by a backend whose Close method discards
// queued audio. Drain waits until already-written audio has played.
type PlaybackDrainer interface{ Drain() error }

// FailureReporter exposes the last underlying device error.
type FailureReporter interface{ LastError() string }

// EndpointProvider supports runtime endpoint discovery and selection.
//
// Endpoint names are opaque and are passed back unchanged.
type EndpointProvider interface {
	AudioDevices() (inputs, outputs []Endpoint, err error)
	SelectedAudioDevices() (input, output string)
	SelectAudioDevices(input, output string) error
}

// ClockedOutput exposes the next hardware playback position, for a
// synchronised stream such as Sendspin.
//
// The position is what keeps the music in step with the voice response:
// without it the music drifts against the reply.
type ClockedOutput interface {
	NextPlaybackTime() (time.Time, time.Duration, error)
	Write([]byte) (int, error)
	Close() error
}

// ClockedBackend provides hardware-clocked output.
type ClockedBackend interface {
	OpenClockedOutput(context.Context, int, int) (ClockedOutput, error)
}
