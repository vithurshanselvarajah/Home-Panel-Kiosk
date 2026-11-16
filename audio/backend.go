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
	"github.com/vithurshanselvarajah/Home-Panel-Kiosk/audio/speaker"
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

// The format the card is played at.
//
// The card runs at the amplifier's own rate, because the tuning inside the
// amplifier was made for it: at 48 kHz every corner of its equaliser and its
// limiter sits an octave low, so the tone is wrong and the protection that
// should be catching the peaks is not catching them.
//
// A card that wants 24 bits in a 32-bit container is given them that way.
var (
	playbackRate = speaker.CardRate()
	playbackBits = speaker.CardBits()
)

// playbackFormat is the ALSA format that goes with the width.
var playbackFormat = func() int {
	if playbackBits == 32 {
		return alsa.FormatS24_LE
	}
	return alsa.FormatS16_LE
}()

// The format the microphones come over in.
//
// The card hands them over at the amplifier's own rate, as four channels of
// 32-bit samples: the converter's 24 bits in a 32-bit container, left
// aligned. The ported microphone's own decimator brings that down to the
// rate the wake word wants.
var (
	captureRate     = 96000
	captureChannels = 4
	captureBits     = 32
)

// captureFormat is the ALSA format the microphones come over in.
//
// The card hands them over as 32-bit words: the converter's 24 bits in a
// 32-bit container, left aligned. The ported microphone's own decimator
// brings that down to what the wake word wants, and reads the words as
// they are, so the format here is the card's and not the wake word's.
var captureFormat = alsa.FormatS32_LE

// firstCapture is the card's first capture device.
//
// The numbers are not the same for capture and playback: a card's playback
// is usually device 0 and its captures follow it, so the first capture is
// found by asking the kernel which device is which rather than by counting.
func firstCapture(card int) (int, bool) {
	return firstCaptureIn("/proc/asound", card)
}

// firstCaptureIn is firstCapture against a given directory, so that it can
// be tried without a card.
func firstCaptureIn(root string, card int) (int, bool) {
	for i := 0; i < 32; i++ {
		b, err := os.ReadFile(fmt.Sprintf("%s/card%d/pcm%dc/info", root, card, i))
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(b), "\n") {
			name, value, found := strings.Cut(line, ":")
			if !found {
				continue
			}
			// The kernel writes the stream's direction in capitals.
			if strings.TrimSpace(name) == "stream" && strings.Contains(strings.ToUpper(value), "CAPTURE") {
				return i, true
			}
		}
	}
	return 0, false
}

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

// IsCard says the backend is the sound card itself, rather than a recording
// standing in for it.
//
// The ported audio stack opens the card as it knows how, with the card's own
// device number, channel count and sample format. A backend that is only
// standing in for the card has to be used through the backend's own methods,
// because it does not know how to open the card itself.
func (b *Backend) IsCard() bool { return true }

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
//
// The default is the card's own first capture and playback devices, found
// by asking the kernel which is which. The numbers are not the same for the
// two: a card's playback device is usually 0 and its captures follow it, so
// asking for the first capture by number would open the playback device
// instead, and the open fails with a device that does not exist.
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
//
// The microphones come over at the card's own rate and width: the card
// hands over four channels of 32-bit samples at 96 kHz, and the ported
// microphone's own decimator brings that down to what the wake word wants.
func (b *Backend) Capture(ctx context.Context, rate int) (io.ReadCloser, error) {
	card, device, err := b.target(b.input)
	if err != nil {
		b.setError(err)
		return nil, err
	}
	if device == 0 {
		// The default was asked for and the card's first capture is not
		// device 0, which is its playback.
		if found, ok := firstCapture(card); ok {
			device = found
		}
	}
	c, err := alsa.OpenPrepared(card, device, alsa.Config{
		Channels:   captureChannels,
		Rate:       captureRate,
		Format:     captureFormat,
		Bits:       captureBits,
		PeriodSize: captureRate / 100,
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
	if device == 0 {
		// The default was asked for and the card's first playback is
		// device 0, which is right.
		_ = device
	}
	p, err := alsa.OpenPlayback(card, device, alsa.Config{
		Channels:   2,
		Rate:       playbackRate,
		Format:     playbackFormat,
		Bits:       playbackBits,
		PeriodSize: playbackRate / 100,
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

// Blocks says the write waits for the hardware, which is what paces it.
//
// The card's own write blocks until the DMA ring has room, so the caller
// must not pace the write again: doing so would run the audio at half the
// rate the card plays it, and the queue would run dry between periods.
func (w *playbackWriter) Blocks() bool { return true }

// Delay is how many frames the card has yet to play.
//
// The card knows exactly how much of its ring is still to be heard, and
// asking it is far better than counting here: a count cannot tell how much
// the hardware has already taken, and a count that only grows turns into a
// latency of minutes, which the speaker then believes.
func (w *playbackWriter) Delay() (int, error) { return w.playback.Delay() }

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
