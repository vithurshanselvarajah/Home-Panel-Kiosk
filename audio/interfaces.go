package audio

import (
	"context"
	"io"
	"time"
)

// The audio types the panel speaks in.
//
// They were the SDK's, and they are here now because the SDK is going
// away. They are the boundary between the panel and the sound card: the
// panel asks for a stream of samples and the backend opens one.

// AudioBackend is the system supplied PCM input and output used by the
// panel. Samples are signed 16-bit little-endian PCM at the requested
// rate. Nothing here selects a platform audio system or a device.
type AudioBackend interface {
	Capture(context.Context, int) (io.ReadCloser, error)
	Playback(context.Context, int) (io.WriteCloser, error)
	Describe() string
}

// PlaybackDrainer is optionally implemented by a backend whose Close
// method discards queued audio. Drain waits until already-written audio
// has played.
type PlaybackDrainer interface{ Drain() error }

// AudioFailureReporter optionally exposes the last underlying device
// error.
type AudioFailureReporter interface{ LastError() string }

// AudioDevice is a system supplied audio endpoint. Name is the complete
// name accepted by the backend; Description is its human readable label.
type AudioDevice struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Default     bool   `json:"default,omitempty"`
}

// AudioDeviceProvider optionally supports runtime endpoint discovery and
// selection. Device names are opaque and are passed back unchanged.
type AudioDeviceProvider interface {
	AudioDevices() (inputs, outputs []AudioDevice, err error)
	SelectedAudioDevices() (input, output string)
	SelectAudioDevices(input, output string) error
}

// ClockedAudioOutput exposes the next hardware playback position for a
// synchronised stream such as Sendspin.
type ClockedAudioOutput interface {
	NextPlaybackTime() (time.Time, time.Duration, error)
	Write([]byte) (int, error)
	Close() error
}

// ClockedAudioBackend optionally provides hardware-clocked output.
type ClockedAudioBackend interface {
	OpenClockedOutput(context.Context, int, int) (ClockedAudioOutput, error)
}
