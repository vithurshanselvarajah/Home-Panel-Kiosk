//go:build !linux

package audio

import (
	"context"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/wilbowes/EchoMuse-SDK/clients/go/echomuse"
)

// describeAudio names the sound system.
func describeAudio() string { return "no audio" }

// audioStream is a stream that cannot be opened.
type audioStream struct{}

// openStream cannot open a stream.
func openStream(device string, direction, rate, channels int) (*audioStream, error) {
	return nil, fmt.Errorf("audio needs Linux")
}

// Read cannot read.
func (p *audioStream) Read(buf []byte) (int, error) { return 0, fmt.Errorf("audio needs Linux") }

// Write cannot write.
func (p *audioStream) Write(buf []byte) (int, error) { return 0, fmt.Errorf("audio needs Linux") }

// Drain cannot drain.
func (p *audioStream) Drain() error { return nil }

// NextPlaybackTime has no clock.
func (p *audioStream) NextPlaybackTime() (time.Time, time.Duration, error) {
	return time.Time{}, 0, fmt.Errorf("audio needs Linux")
}

// Close does nothing.
func (p *audioStream) Close() error { return nil }

// unused keeps the sync import honest on a platform with no audio.
var _ = sync.Mutex{}

// unusedIo keeps the io import honest on a platform with no audio.
var _ io.Reader
