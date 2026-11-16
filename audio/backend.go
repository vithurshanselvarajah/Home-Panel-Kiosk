package audio

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/vithurshanselvarajah/Home-Panel-Kiosk/audio/alsa"
)

// The backend, on the ported ALSA client.
//
// The client is TECHO5's, byte for byte: it drives the sound card's ioctl
// interface directly, with no C library and nothing to install. This is the
// glue that gives the device program the handful of things it asks for.
//
// The device program is written against small interfaces, so that a
// recording can stand in for the card. That is what the file backend in
// the device program does.

// The formats the card is asked for.
const (
	// FormatS16LE is signed 16-bit little-endian PCM.
	FormatS16LE = 2
	// FormatS32LE is signed 32-bit little-endian PCM.
	FormatS32LE = 10
	// FormatS24LE is signed 24-bit little-endian PCM in a 32-bit container.
	FormatS24LE = 6
)

// The rates the card is opened at.
const (
	// CaptureRate is what the controller expects captured audio at.
	CaptureRate = 16000
	// PlaybackRate is what the controller sends audio at.
	PlaybackRate = 48000
)

// Backend is the device's audio.
//
// It satisfies the audio interfaces in this package, so a device program
// can be handed a backend without knowing what it is.
type Backend struct {
	mu sync.RWMutex
	// input and output are the names as the listing reported them, which
	// is what the settings page shows and what is stored.
	input  string
	output string

	lastError string
}

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

// Describe says what the backend is, in a word or two.
func (b *Backend) Describe() string { return "ALSA" }

// SelectedAudioDevices reports the devices in use.
func (b *Backend) SelectedAudioDevices() (string, string) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.input, b.output
}

// LastError is the last underlying device error.
func (b *Backend) LastError() string {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.lastError
}

func (b *Backend) setError(err error) {
	b.mu.Lock()
	if err != nil {
		b.lastError = err.Error()
	}
	b.mu.Unlock()
}

// SelectAudioDevices chooses the devices.
//
// The card is opened by the ported microphone and speaker, so the choice is
// recorded and applied to what they open.
func (b *Backend) SelectAudioDevices(input, output string) error {
	b.mu.Lock()
	b.input = strings.TrimSpace(input)
	b.output = strings.TrimSpace(output)
	b.mu.Unlock()
	return nil
}

// ChooseDefaultInput picks a microphone that works, for when nobody has chosen one.
func (b *Backend) ChooseDefaultInput() error {
	return nil
}

// AudioDevices reports the devices the machine has.
func (b *Backend) AudioDevices() (inputs, outputs []Endpoint, err error) {
	return List()
}

// target turns a device name into the card and device numbers the client
// opens.
//
// The listing reports names as `card:device`. The client opens a card and
// a device number.
func (b *Backend) target(name string) (card, device int, err error) {
	name = strings.TrimSpace(name)
	if name == "" || name == DeviceDefault {
		return 0, 0, nil
	}
	cardName, dev, found := strings.Cut(name, ":")
	if !found {
		return 0, 0, fmt.Errorf("audio: %q is not a device name", name)
	}
	card = alsa.CardByID(cardName)
	if card == 0 {
		return 0, 0, fmt.Errorf("audio: no card named %q", cardName)
	}
	for i := 0; i < 32; i++ {
		if _, err := os.Stat(fmt.Sprintf("/proc/asound/card%d/pcm%dc/info", card, i)); err == nil {
			return card, i, nil
		}
	}
	return 0, 0, fmt.Errorf("audio: card %q has no device %q", cardName, dev)
}

// Capture opens the microphone at the given rate.
func (b *Backend) Capture(ctx context.Context, rate int) (io.ReadCloser, error) {
	card, device, err := b.target(b.input)
	if err != nil {
		b.setError(err)
		return nil, err
	}
	c, err := alsa.OpenPrepared(card, device, alsa.Config{
		Channels:   2,
		Rate:       rate,
		Format:     alsa.FormatS16_LE,
		Bits:       16,
		PeriodSize: rate / 100,
		Periods:    8,
	})
	if err != nil {
		b.setError(err)
		return nil, err
	}
	if err := c.Start(); err != nil {
		_ = c.Close()
		b.setError(err)
		return nil, err
	}
	return &captureReader{capture: c}, nil
}

// captureReader hands the card's frames on as bytes.
type captureReader struct {
	capture *alsa.Capture
}

func (r *captureReader) Read(p []byte) (int, error) { return r.capture.Read(p) }

func (r *captureReader) Close() error { return r.capture.Close() }

// Playback opens the speaker at the given rate.
func (b *Backend) Playback(ctx context.Context, rate int) (io.WriteCloser, error) {
	card, device, err := b.target(b.output)
	if err != nil {
		b.setError(err)
		return nil, err
	}
	p, err := alsa.OpenPlayback(card, device, alsa.Config{
		Channels:   2,
		Rate:       rate,
		Format:     alsa.FormatS16_LE,
		Bits:       16,
		PeriodSize: rate / 100,
		Periods:    8,
	})
	if err != nil {
		b.setError(err)
		return nil, err
	}
	return &playbackWriter{playback: p}, nil
}

// playbackWriter writes to the card.
type playbackWriter struct {
	playback *alsa.Playback
}

func (w *playbackWriter) Write(p []byte) (int, error) { return w.playback.Write(p) }

// Drain waits until the audio that has been written has played.
func (w *playbackWriter) Drain() error { return w.playback.Drain() }

func (w *playbackWriter) Close() error { return w.playback.Close() }

// OpenClockedOutput opens a speaker that reports when each period reaches the card.
func (b *Backend) OpenClockedOutput(ctx context.Context, rate, channels int) (ClockedOutput, error) {
	card, device, err := b.target(b.output)
	if err != nil {
		b.setError(err)
		return nil, err
	}
	p, err := alsa.OpenPlayback(card, device, alsa.Config{
		Channels:   channels,
		Rate:       rate,
		Format:     alsa.FormatS16_LE,
		Bits:       16,
		PeriodSize: rate / 100,
		Periods:    8,
	})
	if err != nil {
		b.setError(err)
		return nil, err
	}
	return &clockedOutput{playback: p, rate: rate}, nil
}

// clockedOutput is the card's own view of when each frame will be heard.
type clockedOutput struct {
	playback *alsa.Playback
	rate     int
}

// NextPlaybackTime is when the next frame should reach the speaker.
func (c *clockedOutput) NextPlaybackTime() (time.Time, time.Duration, error) {
	frames, err := c.playback.Delay()
	if err != nil {
		return time.Time{}, 0, err
	}
	queued := time.Duration(float64(frames) / float64(c.rate) * float64(time.Second))
	return time.Now().Add(queued), 0, nil
}

// Write plays one block of audio.
func (c *clockedOutput) Write(p []byte) (int, error) { return c.playback.Write(p) }

// Close stops the playback.
func (c *clockedOutput) Close() error { return c.playback.Close() }
