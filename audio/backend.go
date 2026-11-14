package audio

import (
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

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

// The audio interfaces, checked at compile time.
var (
	_ Stream           = (*Backend)(nil)
	_ EndpointProvider = (*Backend)(nil)
	_ ClockedBackend   = (*Backend)(nil)
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
// The listing reports names as `alsa:CARD,DEV`. ALSA wants that as
// `hw:CARD=CARD,DEV=DEV`.
//
// The `plughw:` prefix is deliberately NOT used. It opens the hardware
// directly, which takes the card away from the sound server and fails with
// "busy" on a machine where the server already has it. A device that
// cannot open the microphone because something else is playing music is a
// device that does not work.
//
// A device that is busy for a real reason still fails — a card whose main
// device is held by another program cannot be opened, and saying so is
// better than pretending the microphone works.
//
// The default device is ALSA's own default, which is the sound server's
// default, and is the right answer when nothing has been chosen.
func target(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "default"
	}
	if strings.HasPrefix(name, "alsa:") {
		card, device, found := strings.Cut(strings.TrimPrefix(name, "alsa:"), ",")
		if found {
			return "hw:CARD=" + card + ",DEV=" + device
		}
		return "default"
	}
	if strings.HasPrefix(name, "hw:") {
		return name
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
	if err == nil {
		b.lastError = ""
	} else {
		b.lastError = err.Error()
	}
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

// ChooseDefaultInput picks the microphone to use when nobody has chosen
// one.
//
// The system default is not always a microphone that works. On the Echo
// Show 8 the card has two capture devices: the real microphone array,
// and a dummy that the machine driver puts in front of it. The dummy
// opens for the level meter's format, so it is taken for a working
// microphone, and it cannot be opened for the card's own format, so the
// wake word never hears anything.
//
// The devices are tried in the card's own format, and the first one that
// opens is the one that is used. A device that cannot be opened is not a
// microphone as far as the panel is concerned, so it is skipped rather
// than reported.
//
// The chosen device is remembered, so the choice is made once rather
// than on every capture.
func (b *Backend) ChooseDefaultInput() error {
	b.mu.RLock()
	chosen := b.input
	b.mu.RUnlock()
	if chosen != "" && chosen != DeviceDefault {
		return nil
	}

	inputs, _, err := List()
	if err != nil {
		return err
	}

	var lastErr error
	for _, endpoint := range inputs {
		if endpoint.Name == DeviceDefault {
			continue
		}
		// The card's own format, through the hardware alone: a device
		// that opens for the level meter's format but not for this one
		// is a dummy, and the wake word cannot use it.
		stream, err := openHardware(target(endpoint.Name), streamCapture, CardCaptureRate, CardCaptureChannels, FormatS32LE)
		if err != nil {
			lastErr = err
			continue
		}
		_ = stream.Close()

		b.mu.Lock()
		b.input = endpoint.Name
		b.mu.Unlock()
		return nil
	}

	// Nothing opened. The system default is left in place, so the panel
	// says why rather than claiming a microphone it does not have.
	if lastErr != nil {
		return lastErr
	}
	return fmt.Errorf("no microphone could be opened")
}

// AudioDevices lists the endpoints the machine has.
func (b *Backend) AudioDevices() (inputs, outputs []Endpoint, err error) {
	in, out, err := List()
	if err != nil {
		return nil, nil, err
	}
	return in, out, nil
}

// Capture opens the microphone.
//
// The reader is closed to stop the capture, and closing it releases the
// sound card: a capture that is only marked as stopped leaves the device
// open, and the next capture opens a second one. The device then streams
// whichever of the two the sound server gave it, and the controller hears
// silence while the display says it is listening.
//
// The stream is opened with two channels because that is what the capture
// devices have. The controller wants one, so the channels are averaged
// as the frames are read.
func (b *Backend) Capture(ctx context.Context, rate int) (io.ReadCloser, error) {
	b.mu.RLock()
	device := b.input
	b.mu.RUnlock()

	stream, err := openStream(target(device), streamCapture, rate, captureChannels)
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
//
// The channel count asked for is a request, not a demand. A sound card
// that will not open for stereo is opened for mono instead, and the
// output says how many channels it really has, so the caller can lay the
// audio out to match. A device that cannot play stereo still plays the
// music.
//
// The caller has to know what it got, so the returned output carries the
// channel count it was opened with.
func (b *Backend) OpenClockedOutput(ctx context.Context, rate, channels int) (ClockedOutput, error) {
	b.mu.RLock()
	device := b.output
	b.mu.RUnlock()

	stream, err := openStream(target(device), streamPlayback, rate, channels)
	if err != nil {
		// A card that will not open for the channels asked for may still
		// open for one. Mono is the fallback because every card has it.
		if channels <= 1 {
			b.setError(err)
			return nil, err
		}
		fallback, fallbackErr := openStream(target(device), streamPlayback, rate, 1)
		if fallbackErr != nil {
			b.setError(err)
			return nil, err
		}
		b.setError(nil)
		return &clocked{stream: fallback, done: make(chan struct{}), channels: 1}, nil
	}
	return &clocked{stream: stream, done: make(chan struct{}), channels: channels}, nil
}

// captureChannels is how many channels a capture is opened with.
//
// The capture devices on a laptop and on a Pi are stereo, and ALSA will not
// open one for a single channel. The controller wants mono, so the channels
// are averaged as the frames are read.
const captureChannels = 2

// CaptureFormat describes a capture in the card's own words, for a card
// that will not open for anything else.
//
// The Echo Show 8's sound card hands the microphones over as 32-bit
// samples at 96 kHz, and its machine driver asks for exactly that: the
// amplifier reads the top 16 bits of each 32-bit slot and the microphone
// converter writes 32-bit words. A 16-bit request is refused outright.
//
// The frames are read in this format and converted by the caller, because
// the conversion is the same work whether the caller is the wake word or
// the level meter.
type CaptureFormat struct {
	// Rate is the rate the card is opened at.
	Rate int
	// Channels is how many channels it is opened with.
	Channels int
	// Format is the ALSA sample format: S32LE or S16LE.
	Format int
}

// FormatS32LE and FormatS16LE are the ALSA sample formats.
//
// The numbers are ALSA's own: SND_PCM_FORMAT_S16_LE is 2 and
// SND_PCM_FORMAT_S32_LE is 10. The format that is 8 is S32_BE, the
// same width the other way round, and a card that is asked for it says
// the format is not available.
const (
	FormatS16LE = 2
	FormatS32LE = 10
)

// The card's own capture format.
//
// The Echo Show 8's sound card hands the microphones over as 32-bit
// samples at 96 kHz, and its machine driver asks for exactly that: the
// amplifier reads the top 16 bits of each 32-bit slot and the microphone
// converter writes 32-bit words. A 16-bit request is refused outright.
//
// These are the format a microphone has to open for to be usable at all.
// The card also has a dummy in front of the real array, and the dummy
// opens for the level meter's format but not for this one, so this is
// what tells the two apart.
const (
	// CardCaptureRate is the rate the card captures the microphones at.
	CardCaptureRate = 96000
	// CardCaptureChannels is how many microphone channels the card hands
	// over.
	CardCaptureChannels = 4
)

// RawCapture is a capture in the card's own format.
//
// It is what a board whose card insists on a format needs: the frames are
// read as the card gives them and the conversion is the caller's, because
// the caller's conversion is the one that needs doing anyway.
type RawCapture interface {
	// ReadRaw reads frames in the card's format. The buffer must be a
	// whole number of frames.
	ReadRaw(p []byte) (int, error)
	// Format is what the capture was opened as.
	Format() CaptureFormat
	// Close releases the microphone.
	Close() error
}

// CaptureRaw opens the microphone in the card's own format.
func (b *Backend) CaptureRaw(ctx context.Context, f CaptureFormat) (RawCapture, error) {
	b.mu.RLock()
	device := b.input
	b.mu.RUnlock()

	stream, err := openStreamFormat(target(device), streamCapture, f.Rate, f.Channels, f.Format)
	if err != nil {
		b.setError(err)
		return nil, err
	}
	return newRawCapture(stream, f), nil
}

// newRawCapture makes a capture in the card's own format.
//
// It is here so that the capture is made in one place: the channel it
// closes when the microphone is released has to be made with it, and a
// capture made without one panics the moment it is closed.
func newRawCapture(stream *audioStream, f CaptureFormat) *rawCapture {
	return &rawCapture{stream: stream, format: f, done: make(chan struct{})}
}

// rawCapture is a capture in the card's own format.
type rawCapture struct {
	stream *audioStream
	format CaptureFormat
	done   chan struct{}
	once   sync.Once
}

// ReadRaw reads frames in the card's format.
func (c *rawCapture) ReadRaw(p []byte) (int, error) {
	select {
	case <-c.done:
		return 0, io.EOF
	default:
	}
	return c.stream.Read(p)
}

// Format is what the capture was opened as.
func (c *rawCapture) Format() CaptureFormat { return c.format }

// Close releases the microphone.
func (c *rawCapture) Close() error {
	c.once.Do(func() { close(c.done) })
	return c.stream.Close()
}

// capture reads the microphone.
type capture struct {
	stream *audioStream
	done   chan struct{}
	once   sync.Once
	// mono holds the downmixed frames, so the averaging is done once and
	// not on every read.
	mono []byte
}

// Read reads one block of audio.
//
// The block is 100 milliseconds, which is the size the controller's
// capture frames are, so a frame is one read.
//
// The frames are averaged down to one channel, because that is what the
// controller expects and what the sound device gives is not.
func (c *capture) Read(p []byte) (int, error) {
	select {
	case <-c.done:
		return 0, io.EOF
	default:
	}

	// The device gives two channels and the caller wants one, so the
	// buffer has to be big enough for what comes out of the device.
	needed := len(p) * captureChannels
	if cap(c.mono) < needed {
		c.mono = make([]byte, needed)
	}
	buf := c.mono[:needed]

	n, err := c.stream.Read(buf)
	if n <= 0 {
		return 0, err
	}
	return downmix(buf[:n], p), err
}

// downmix averages the channels of a block of audio.
//
// It is a copy rather than an average in place, because the caller's
// buffer is not the device's buffer.
func downmix(src, dst []byte) int {
	frame := 2 * captureChannels
	out := 0
	for i := 0; i+frame <= len(src) && out+2 <= len(dst); i += frame {
		left := int16(uint16(src[i]) | uint16(src[i+1])<<8)
		right := int16(uint16(src[i+2]) | uint16(src[i+3])<<8)
		v := (int32(left) + int32(right)) / 2
		dst[out] = byte(v)
		dst[out+1] = byte(uint16(v) >> 8)
		out += 2
	}
	return out
}

// StereoToMono averages a stereo buffer into a mono one.
//
// It is exported because the device's mixer is the one that lays the
// music out, and the conversion belongs with the rest of the audio
// handling rather than being written twice.
//
// The music is stereo, because that is what the player fills, and a
// speaker may be mono. The two channels are averaged rather than one of
// them taken, because a hard-panned track played on one side only is
// quieter on that side than the other.
func StereoToMono(src, dst []byte) int {
	frame := 4
	out := 0
	for i := 0; i+frame <= len(src) && out+2 <= len(dst); i += frame {
		left := int32(int16(uint16(src[i]) | uint16(src[i+1])<<8))
		right := int32(int16(uint16(src[i+2]) | uint16(src[i+3])<<8))
		v := (left + right) / 2
		dst[out] = byte(v)
		dst[out+1] = byte(uint16(v) >> 8)
		out += 2
	}
	return out
}

// Close stops the capture and releases the sound card.
//
// It is called more than once — the capture's own loop closes it when it
// ends, and whoever stopped the capture closes it as well — so the close
// happens once. A second close of a sound card is an assertion inside
// ALSA, and an assertion takes the whole process down.
func (c *capture) Close() error {
	var err error
	c.once.Do(func() {
		close(c.done)
		err = c.stream.Close()
	})
	return err
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
	// channels is how many channels the stream really has, which may be
	// fewer than were asked for.
	channels int
}

// Channels is how many channels the output really carries.
//
// The caller lays the audio out to match, so a card that only does mono
// gets mono rather than the left channel played twice.
func (c *clocked) Channels() int { return c.channels }

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
