//go:build linux

package audio

import (
	"fmt"
	"sync"
	"time"
	"unsafe"

	"github.com/ebitengine/purego"
)

// The ALSA calls the backend makes.
//
// libasound is loaded at run time rather than linked against, which means
// the binary cross compiles without a C toolchain and starts on a machine
// with no sound card. The failure is a message rather than a missing
// shared library.
var (
	// sndPcmOpen opens a PCM stream.
	sndPcmOpen func(pcm *uintptr, name *byte, stream int, mode int) int
	// sndPcmSetParams sets the stream's format.
	sndPcmSetParams func(pcm uintptr, format int, access int, channels uint, rate uint, softResample int, latencyUsec uint) int
	// sndPcmWritei writes frames.
	sndPcmWritei func(pcm uintptr, data unsafe.Pointer, frames uintptr) int64
	// sndPcmReadi reads frames.
	sndPcmReadi func(pcm uintptr, data unsafe.Pointer, frames uintptr) int64
	// sndPcmDelay returns the frames waiting to be played.
	sndPcmDelay func(pcm uintptr, delay *int64) int64
	// sndPcmRecover recovers the stream from an error.
	sndPcmRecover func(pcm uintptr, err int, silent int) int
	// sndPcmDrain waits for the written audio to play.
	sndPcmDrain func(pcm uintptr) int
	// sndPcmDrop discards the queued audio.
	sndPcmDrop func(pcm uintptr) int
	// sndPcmClose closes the stream.
	sndPcmClose func(pcm uintptr) int
)

// The ALSA constants the backend uses.
const (
	// s16le is the sample format: signed 16-bit little-endian.
	s16le = 2
	// rwInterleaved is the access mode.
	rwInterleaved = 3
	// streamPlayback and streamCapture are ALSA's stream directions.
	streamPlayback = 0
	streamCapture  = 1
)

// libraryErr is why libasound could not be used, if it could not.
var (
	libraryOnce sync.Once
	libraryErr  error
)

// loadALSA loads libasound and binds the calls the backend makes.
//
// It is loaded once and kept: loading it per stream would mean loading it
// per capture and per playback, and the load is the slow part.
func loadALSA() error {
	libraryOnce.Do(func() {
		handle, err := purego.Dlopen("libasound.so.2", purego.RTLD_NOW|purego.RTLD_GLOBAL)
		if err != nil {
			libraryErr = fmt.Errorf("libasound is not available: %w", err)
			return
		}

		for _, entry := range []struct {
			target any
			name   string
		}{
			{&sndPcmOpen, "snd_pcm_open"},
			{&sndPcmSetParams, "snd_pcm_set_params"},
			{&sndPcmWritei, "snd_pcm_writei"},
			{&sndPcmReadi, "snd_pcm_readi"},
			{&sndPcmDelay, "snd_pcm_delay"},
			{&sndPcmRecover, "snd_pcm_recover"},
			{&sndPcmDrain, "snd_pcm_drain"},
			{&sndPcmDrop, "snd_pcm_drop"},
			{&sndPcmClose, "snd_pcm_close"},
		} {
			symbol, err := purego.Dlsym(handle, entry.name)
			if err != nil {
				libraryErr = fmt.Errorf("%s is missing from libasound: %w", entry.name, err)
				return
			}
			purego.RegisterFunc(entry.target, symbol)
		}
	})
	return libraryErr
}

// describeAudio names the sound system.
func describeAudio() string { return "ALSA" }

// audioStream is one open ALSA stream.
type audioStream struct {
	mu sync.Mutex
	// handle is the ALSA stream.
	handle uintptr
	// rate is the sample rate the stream was opened at, which the clock
	// needs to turn frames into time.
	rate int
	// channels is how many channels the stream carries.
	channels int
	// closed is whether the stream has been closed.
	closed bool
}

// openStream opens an ALSA stream.
//
// The latency is 100 milliseconds, which is the same figure the SDK's
// emulator uses: a small device has to buffer enough to survive a
// scheduling delay, and too large a buffer is latency the user hears.
func openStream(device string, direction, rate, channels int) (*audioStream, error) {
	if err := loadALSA(); err != nil {
		return nil, err
	}

	name := []byte(device + "\x00")
	var handle uintptr
	if rc := sndPcmOpen(&handle, &name[0], direction, 0); rc < 0 {
		// The handle is NOT closed here. Closing a handle that was never
		// opened is an assertion inside ALSA, and an assertion takes the
		// whole process down — so a device that is merely busy would kill
		// the application rather than say it is busy.
		return nil, fmt.Errorf("opening the ALSA device %q: %s", device, alsaError(int(rc)))
	}
	if rc := sndPcmSetParams(handle, s16le, rwInterleaved, uint(channels), uint(rate), 1, 100000); rc < 0 { // The stream is closed, because it was opened. Closing it is safe
		// here and closing it is right: leaving a stream open would take
		// the device away from anything else that wants it.		sndPcmClose(handle)
		return nil, fmt.Errorf("setting the format of the ALSA device %q: %s", device, alsaError(rc))
	}
	return &audioStream{handle: handle, rate: rate, channels: channels}, nil
}

// Read reads one block of frames.
//
// A negative return is an ALSA error, and the stream is recovered when it
// is one the stream can recover from: an underrun on the capture side is a
// scheduling delay, not a broken sound card.
func (p *audioStream) Read(buf []byte) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return 0, fmt.Errorf("the ALSA stream is closed")
	}

	frames := len(buf) / (2 * p.channels)
	if frames == 0 {
		return 0, nil
	}

	for {
		n := sndPcmReadi(p.handle, unsafe.Pointer(&buf[0]), uintptr(frames))
		if n >= 0 {
			return int(n) * 2 * p.channels, nil
		}
		if rc := sndPcmRecover(p.handle, int(n), 1); rc < 0 {
			return 0, fmt.Errorf("reading from the ALSA device: %s", alsaError(int(n)))
		}
	}
}

// Write writes one block of frames.
func (p *audioStream) Write(buf []byte) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return 0, fmt.Errorf("the ALSA stream is closed")
	}

	frames := len(buf) / (2 * p.channels)
	if frames == 0 {
		return 0, nil
	}

	for {
		n := sndPcmWritei(p.handle, unsafe.Pointer(&buf[0]), uintptr(frames))
		if n >= 0 {
			return int(n) * 2 * p.channels, nil
		}
		if rc := sndPcmRecover(p.handle, int(n), 1); rc < 0 {
			return 0, fmt.Errorf("writing to the ALSA device: %s", alsaError(int(n)))
		}
	}
}

// Drain waits until the audio that has been written has played.
//
// It is what stops the end of a reply being cut off: the controller
// releases its speaking state when playback is reported finished.
//
// A drain on a closed stream is not an error. ALSA asserts on it, and an
// assertion takes the whole process down, so the closed case is caught
// here rather than in the library.
func (p *audioStream) Drain() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return nil
	}
	if rc := sndPcmDrain(p.handle); rc < 0 {
		return fmt.Errorf("draining the ALSA device: %s", alsaError(int(rc)))
	}
	return nil
}

// NextPlaybackTime is when the next frame should reach the speaker.
//
// It is the number of frames waiting to be played turned into time. The
// sound card has a buffer of its own, so the time a frame is written is
// not the time it is heard, and a player that uses the write time drifts
// against the voice.
func (p *audioStream) NextPlaybackTime() (time.Time, time.Duration, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return time.Time{}, 0, fmt.Errorf("the ALSA stream is closed")
	}

	var frames int64
	if rc := sndPcmDelay(p.handle, &frames); rc < 0 {
		return time.Time{}, 0, fmt.Errorf("asking the ALSA device for its delay: %s", alsaError(int(rc)))
	}
	queued := time.Duration(float64(frames) / float64(p.rate) * float64(time.Second))
	return time.Now().Add(queued), 0, nil
}

// Close closes the stream.
//
// The queued audio is dropped rather than drained, because the stream is
// closing because something stopped, and playing the rest of a reply to a
// speaker nobody is listening to is worse than dropping it.
//
// Closing twice is not an error: the stream is closed by whoever stops the
// playback and again by the loop that was playing it, and a double close
// of a sound card is an assertion inside ALSA.
func (p *audioStream) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed || p.handle == 0 {
		p.closed = true
		return nil
	}
	p.closed = true
	sndPcmDrop(p.handle)
	sndPcmClose(p.handle)
	p.handle = 0
	return nil
}

// alsaError names an ALSA error code.
//
// ALSA's own codes are negative errno values with a few of its own, and
// the name is what makes the message useful to somebody reading a log.
func alsaError(code int) string {
	switch code {
	case -38:
		return "libasound is not available"
	case -22:
		return "the device does not accept that format"
	case -16:
		return "the device is busy — it is already in use, or it is the same device as the other one"
	case -11:
		return "the device is out of memory"
	case -32:
		return "the device reported a broken pipe"
	case -77:
		return "the device is not ready"
	default:
		return fmt.Sprintf("ALSA error %d", code)
	}
}
