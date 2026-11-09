package audio

import (
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/wilbowes/EchoMuse-SDK/clients/go/echomuse"
)

// The rates the controller expects.
//
// Capture and playback differ, and a device that gets them wrong plays
// audio at the wrong speed.
const (
	// CaptureRate is what the controller expects captured audio at.
	CaptureRate = 16000
	// PlaybackRate is what the controller sends audio at.
	PlaybackRate = 48000
)

// Backend is the device's audio.
//
// It satisfies the SDK's audio interfaces, so a device program can hand it
// to the SDK without the SDK knowing what it is.
type Backend struct {
	mu sync.RWMutex
	// input and output are the names as the listing reported them, which
	// is what the settings page shows and what is stored.
	input  string
	output string

	lastError string
}

// The SDK's interfaces, checked at compile time.
var (
	_ echomuse.AudioBackend        = (*Backend)(nil)
	_ echomuse.AudioDeviceProvider = (*Backend)(nil)
	_ echomuse.ClockedAudioBackend = (*Backend)(nil)
)

// New returns a backend that uses the named devices.
//
// An empty name is the system's default. The names are the ones the
// device listing reports, which are ALSA's own.
func New(input, output string) *Backend {
	return &Backend{
		input:  strings.TrimSpace(input),
		output: strings.TrimSpace(output),
	}
}

// target turns a device name into the name the sound system is given.
//
// The listing reports names as `alsa:CARD,DEV`, which is the card and the
// device on it. ALSA wants that as `hw:CARD=CARD,DEV=DEV`, with the
// `plughw:` wrapper so the sound card does the format conversion: the raw
// hardware does not, and a device asked for the wrong sample rate fails.
//
// A name that is already an ALSA name is left alone, so a name typed by
// hand works too.
func target(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "default"
	}
	if strings.HasPrefix(name, "alsa:") {
		card, device, found := strings.Cut(strings.TrimPrefix(name, "alsa:"), ",")
		if found {
			return "plughw:CARD=" + card + ",DEV=" + device
		}
		return "default"
	}
	if strings.HasPrefix(name, "hw:") {
		return "plughw:" + strings.TrimPrefix(name, "hw:")
	}
	return name
}

// Describe says what the backend is using.
func (b *Backend) Describe() string {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return fmt.Sprintf("%s (%s → %s)", describeAudio(), b.input, b.output)
}

// SelectedAudioDevices reports the devices in use.
//
// The names are the ones the listing reported, so what the settings page
// shows is what the device is using.
func (b *Backend) SelectedAudioDevices() (string, string) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.input, b.output
}

// LastError is the most recent failure, if any.
func (b *Backend) LastError() string {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.lastError
}

func (b *Backend) setError(err error) {
	b.mu.Lock()
	b.lastError = err.Error()
	b.mu.Unlock()
}

// SelectAudioDevices changes the devices in use.
//
// The names are checked against the listing first, because a device that
// is not there fails at the next capture rather than now, and a failure
// at the next capture is a failure the user cannot connect to a choice
// they made.
func (b *Backend) SelectAudioDevices(input, output string) error {
	inputs, outputs, err := List()
	if err != nil {
		return err
	}
	known := func(devices []Endpoint, name string) bool {
		for _, d := range devices {
			if d.Name == name {
				return true
			}
		}
		return false
	}
	if input != "" && !known(inputs, input) {
		return fmt.Errorf("unknown microphone %q", input)
	}
	if output != "" && !known(outputs, output) {
		return fmt.Errorf("unknown speaker %q", output)
	}
	b.mu.Lock()
	b.input = strings.TrimSpace(input)
	b.output = strings.TrimSpace(output)
	b.mu.Unlock()
	return nil
}

// AudioDevices lists the endpoints the machine has.
func (b *Backend) AudioDevices() (inputs, outputs []echomuse.AudioDevice, err error) {
	in, out, err := List()
	if err != nil {
		return nil, nil, err
	}
	for _, d := range in {
		inputs = append(inputs, echomuse.AudioDevice{Name: d.Name, Description: d.Description, Default: d.Default})
	}
	for _, d := range out {
		outputs = append(outputs, echomuse.AudioDevice{Name: d.Name, Description: d.Description, Default: d.Default})
	}
	return inputs, outputs, nil
}

// Capture opens the microphone.
//
// The reader is closed to stop the capture, and closing it releases the
// sound card: a capture that is only marked as stopped leaves the device
// open, and the next capture opens a second one. The device then streams
// whichever of the two the sound server gave it, and the controller hears
// silence while the display says it is listening.
func (b *Backend) Capture(ctx context.Context, rate int) (io.ReadCloser, error) {
	b.mu.RLock()
	device := b.input
	b.mu.RUnlock()

	stream, err := openStream(target(device), streamCapture, rate, 1)
	if err != nil {
		b.setError(err)
		return nil, err
	}
	return &capture{stream: stream, done: make(chan struct{})}, nil
}

// Playback opens the speaker.
func (b *Backend) Playback(ctx context.Context, rate int) (io.WriteCloser, error) {
	b.mu.RLock()
	device := b.output
	b.mu.RUnlock()

	stream, err := openStream(target(device), streamPlayback, rate, 1)
	if err != nil {
		b.setError(err)
		return nil, err
	}
	return &playback{stream: stream, done: make(chan struct{})}, nil
}

// OpenClockedOutput opens a speaker that reports when the next frame
// should play.
//
// The clock is what keeps the Sendspin stream in step with the voice
// response: without it the music drifts against the reply.
func (b *Backend) OpenClockedOutput(ctx context.Context, rate, channels int) (echomuse.ClockedAudioOutput, error) {
	b.mu.RLock()
	device := b.output
	b.mu.RUnlock()

	stream, err := openStream(target(device), streamPlayback, rate, channels)
	if err != nil {
		b.setError(err)
		return nil, err
	}
	return &clocked{stream: stream, done: make(chan struct{})}, nil
}

// capture reads the microphone.
type capture struct {
	stream *audioStream
	done   chan struct{}
	once   sync.Once
}

// Read reads one block of audio.
//
// The block is 100 milliseconds, which is the size the controller's
// capture frames are, so a frame is one read.
func (c *capture) Read(p []byte) (int, error) {
	select {
	case <-c.done:
		return 0, io.EOF
	default:
	}
	return c.stream.Read(p)
}

// Close stops the capture and releases the sound card.
func (c *capture) Close() error {
	c.once.Do(func() { close(c.done) })
	return c.stream.Close()
}

// playback writes to the speaker.
type playback struct {
	stream *audioStream
	done   chan struct{}
	once   sync.Once
}

// Write plays one block of audio.
func (w *playback) Write(p []byte) (int, error) {
	select {
	case <-w.done:
		return 0, io.EOF
	default:
	}
	return w.stream.Write(p)
}

// Close stops the playback.
func (w *playback) Close() error {
	w.once.Do(func() { close(w.done) })
	return w.stream.Close()
}

// Drain waits until the audio that has been written has played.
//
// The controller releases its speaking state when playback is reported
// finished, so a close that drops the tail of the reply is a reply that
// is cut off.
func (w *playback) Drain() error { return w.stream.Drain() }

// clocked is a speaker that knows when the next frame should play.
type clocked struct {
	stream *audioStream
	done   chan struct{}
	once   sync.Once
}

// NextPlaybackTime is when the next frame should reach the speaker.
func (c *clocked) NextPlaybackTime() (time.Time, time.Duration, error) {
	return c.stream.NextPlaybackTime()
}

// Write plays one block of audio.
func (c *clocked) Write(p []byte) (int, error) {
	select {
	case <-c.done:
		return 0, io.EOF
	default:
	}
	return c.stream.Write(p)
}

// Close stops the playback.
func (c *clocked) Close() error {
	c.once.Do(func() { close(c.done) })
	return c.stream.Close()
}
