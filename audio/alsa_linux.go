//go:build linux

package audio

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/ebitengine/purego"
)

// The ALSA calls the backend makes.
//
// libasound is loaded at run time rather than linked against, which means
// the binary cross compiles without a C toolchain and starts on a machine
// with no sound card. The failure is a message rather than a missing
// shared library.
//
// Every call that returns an error is declared as returning int32, and
// that is not a style choice. ALSA returns a C `int`, which is 32 bits
// wide and signed. The FFI layer hands back the raw 64-bit register, and
// on x86-64 a 32-bit result is zero extended into it: an error of -16
// arrives as 4294967280, which is positive. A check of `rc < 0` is then
// never true, the failure is invisible, and the code carries on with a
// handle that was never opened. ALSA answers that by taking the process
// down, which is how a busy sound card became a crash rather than a
// message. Declaring the return as int32 makes the sign real.
var (
	// sndPcmOpen opens a PCM stream.
	sndPcmOpen func(pcm *uintptr, name *byte, stream int, mode int) int32
	// sndPcmSetParams sets the stream's format.
	sndPcmSetParams func(pcm uintptr, format int, access int, channels uint, rate uint, softResample int, latencyUsec uint) int32
	// sndPcmWritei writes frames.
	//
	// The buffer is a slice rather than a pointer, because that is the
	// form purego understands: it keeps the memory alive for the call
	// and pins it, so the collector cannot move it while the sound card
	// is reading it. A bare pointer carries no such promise, and the
	// crash it causes is a segfault inside the library.
	sndPcmWritei func(pcm uintptr, data []byte, frames uintptr) int64
	// sndPcmReadi reads frames.
	sndPcmReadi func(pcm uintptr, data []byte, frames uintptr) int64
	// sndPcmDelay returns the frames waiting to be played.
	sndPcmDelay func(pcm uintptr, delay *int64) int32
	// sndPcmRecover recovers the stream from an error.
	sndPcmRecover func(pcm uintptr, err int, silent int) int32
	// sndPcmDrain waits for the written audio to play.
	sndPcmDrain func(pcm uintptr) int32
	// sndPcmDrop discards the queued audio.
	sndPcmDrop func(pcm uintptr) int32
	// sndPcmClose closes the stream.
	sndPcmClose func(pcm uintptr) int32
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
	// reading is whether a read is in progress.
	//
	// A read blocks in the sound card until the next frame arrives, and
	// the close has to get past it: a close that waits for the read to
	// finish holds the card for the length of a frame, and the thing
	// that wants the card next is told it is busy.
	reading bool
}

// openStream opens an ALSA stream.
//
// The latency is 100 milliseconds, which is the same figure the SDK's
// emulator uses: a small device has to buffer enough to survive a
// scheduling delay, and too large a buffer is latency the user hears.
//
// The device is opened through more than one name when the first does not
// work, because a sound card that cannot do the rate the controller wants
// is an ordinary thing rather than a broken one.
func openStream(device string, direction, rate, channels int) (*audioStream, error) {
	if err := loadALSA(); err != nil {
		return nil, err
	}

	var failure error
	for _, candidate := range streamCandidates(device) {
		stream, err := openOne(candidate, device, direction, rate, channels)
		if err == nil {
			return stream, nil
		}
		failure = err
		// A device that is busy is busy however it is opened, so there is
		// nothing to gain from trying the next name.
		if isBusy(failure) {
			break
		}
	}
	return nil, failure
}

// streamCandidates are the names a device is opened through, in order.
//
// The hardware name comes first: it is the one with the least latency.
// The plug name comes second: it is the same hardware with the rate
// converted in software, which is what makes a microphone that only runs
// at 48 kHz usable at the 16 kHz the controller asks for.
//
// The system default is left alone. It already goes through the sound
// server, which does the conversion itself.
func streamCandidates(device string) []string {
	if strings.HasPrefix(device, "hw:") {
		return []string{device, "plug" + device}
	}
	return []string{device}
}

// openOne opens a stream through one name.
//
// The name it is given and the name it is reported under are different
// things: the device is opened as `hw:CARD=Audio,DEV=0` but it is
// reported as `alsa:Audio,0`, which is what the settings page shows.
func openOne(candidate, device string, direction, rate, channels int) (*audioStream, error) {
	name := []byte(candidate + "\x00")
	// The handle is zeroed before the open, because ALSA does not touch
	// the output pointer when the open fails. A handle that still holds
	// the previous stream's value would be closed or configured by
	// mistake, and ALSA answers that by taking the process down.
	var handle uintptr
	if rc := sndPcmOpen(&handle, &name[0], direction, 0); rc < 0 {
		// The handle is NOT closed here. Closing a handle that was never
		// opened is an assertion inside ALSA, and an assertion takes the
		// whole process down — so a device that is merely busy would kill
		// the application rather than say it is busy.
		return nil, &alsaFailure{operation: "opening", device: device, code: int(rc)}
	}
	if rc := sndPcmSetParams(handle, s16le, rwInterleaved, uint(channels), uint(rate), 1, 100000); rc < 0 {
		// The stream is closed, because it was opened. Closing it is safe
		// here and closing it is right: leaving a stream open would take
		// the device away from anything else that wants it.
		sndPcmDrop(handle)
		sndPcmClose(handle)
		return nil, &alsaFailure{operation: "setting the format of", device: device, code: int(rc), rate: rate}
	}
	return &audioStream{handle: handle, rate: rate, channels: channels}, nil
}

// alsaFailure is an ALSA call that failed, with the code it failed with.
//
// The code is kept rather than only the message, because whether another
// name is worth trying depends on what the failure was: a device that is
// busy stays busy, and a device that does not accept the rate works
// through the plug.
type alsaFailure struct {
	// operation is what was being done.
	operation string
	// device is the name the device was chosen by.
	device string
	// code is the ALSA error code.
	code int
	// rate is the rate that was asked for, when the failure was about it.
	rate int
}

// Error says what failed and why.
func (f *alsaFailure) Error() string {
	if f.rate > 0 {
		return fmt.Sprintf("%s the ALSA device %q at %d Hz: %s", f.operation, f.device, f.rate, alsaError(f.code))
	}
	return fmt.Sprintf("%s the ALSA device %q: %s", f.operation, f.device, alsaError(f.code))
}

// isBusy says whether the failure was a device that was already in use.
func isBusy(err error) bool {
	var failure *alsaFailure
	if !errors.As(err, &failure) {
		return false
	}
	return failure.code == -16
}

// Read reads one block of frames.
//
// A negative return is an ALSA error, and the stream is recovered when it
// is one the stream can recover from: an underrun on the capture side is a
// scheduling delay, not a broken sound card.
//
// The lock is not held across the read. A read blocks in the sound card
// until the next frame arrives, and a close that had to wait for the
// lock behind it would hold the card for the length of a frame — long
// enough for the next thing that wants it to be told it is busy.
func (p *audioStream) Read(buf []byte) (int, error) {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return 0, fmt.Errorf("the ALSA stream is closed")
	}
	handle := p.handle
	channels := p.channels
	p.reading = true
	p.mu.Unlock()
	defer func() {
		p.mu.Lock()
		p.reading = false
		p.mu.Unlock()
	}()

	frames := len(buf) / (2 * channels)
	if frames == 0 {
		return 0, nil
	}

	for {
		n := sndPcmReadi(handle, buf[:frames*2*channels], uintptr(frames))
		if n >= 0 {
			return int(n) * 2 * channels, nil
		}
		// The stream may have been closed while the read was waiting, in
		// which case the handle is gone and there is nothing to recover.
		p.mu.Lock()
		closed := p.closed
		p.mu.Unlock()
		if closed {
			return 0, fmt.Errorf("the ALSA stream is closed")
		}
		if rc := sndPcmRecover(handle, int(n), 1); rc < 0 {
			return 0, fmt.Errorf("reading from the ALSA device: %s", alsaError(int(n)))
		}
	}
}

// Write writes one block of frames.
//
// The lock is not held across the write, for the same reason the read does
// not hold it: a close that had to wait for the lock behind a write in
// progress would hold the card for the length of a frame.
func (p *audioStream) Write(buf []byte) (int, error) {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return 0, fmt.Errorf("the ALSA stream is closed")
	}
	handle := p.handle
	channels := p.channels
	p.mu.Unlock()

	frames := len(buf) / (2 * channels)
	if frames == 0 {
		return 0, nil
	}

	for {
		n := sndPcmWritei(handle, buf[:frames*2*channels], uintptr(frames))
		if n >= 0 {
			return int(n) * 2 * channels, nil
		}
		p.mu.Lock()
		closed := p.closed
		p.mu.Unlock()
		if closed {
			return 0, fmt.Errorf("the ALSA stream is closed")
		}
		if rc := sndPcmRecover(handle, int(n), 1); rc < 0 {
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
	handle := p.handle
	p.handle = 0

	// The handle is dropped and closed while the lock is held.
	//
	// The lock is what says the card has been released. A close that
	// released the lock first would let a second close return while the
	// card was still held, and the caller waiting for the card to be
	// free would be told it was free before it was.
	//
	// The read does not hold the lock while it waits in the sound card,
	// so this does not wait for it.
	sndPcmDrop(handle)
	sndPcmClose(handle)
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
		return "the device does not accept that format or rate"
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
