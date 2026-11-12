package main

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"github.com/vithurshanselvarajah/Home-Panel-Kiosk/audio"
)

// The file audio backend.
//
// The sound card is replaced by files: the microphone is a recording
// and the speaker is a file. It is how the panel is tested without a
// room and without a card, and how a recording is played into the real
// pipeline — the microphone really is the recording, so what goes to
// Home Assistant is what the recording says.
//
// It is also how the panel can be demonstrated: a recording of a
// question, and the panel answers it.

// fileBackend is a sound card made of files.
type fileBackend struct {
	// input is the file the microphone plays.
	input string
	// output is the file the speaker writes to.
	output string
	// mu guards the offsets, because the microphone and the speaker are
	// read and written from different goroutines.
	mu sync.Mutex
	// inputOffset is how far into the recording the microphone is.
	inputOffset int
	// armed says the recording is to be played.
	//
	// The microphone is quiet until the recording is armed, which is
	// when a turn starts. The panel's microphone is open for as long as
	// the panel is switched on — the wake word is what listens to the
	// room — so a recording that was played from the start would be
	// over before the turn began.
	armed bool
	// generation counts the times the recording has been armed.
	//
	// The reader watches it, so it knows when the recording starts
	// again and can start its clock again with it.
	generation int
}

// newFileBackend makes a backend that plays a recording and writes the
// speaker's audio to a file.
//
// An empty input means silence, which is a microphone in a quiet room.
// An empty output means the speaker is thrown away.
func newFileBackend(input, output string) *fileBackend {
	return &fileBackend{input: input, output: output}
}

// TurnStarted starts the recording again.
//
// It is called when the turn starts, so the recording is said once per
// turn. Said on its own it would go round and round, and Home
// Assistant would hear one long question and answer it as nonsense.
//
// It is called when the turn starts and not when it is asked for,
// because the frames that are read before the turn are not sent to
// Home Assistant: the room is the room's until somebody says
// something. A recording that started earlier would have its
// beginning read and thrown away, and the question would come out
// without its first word.
func (b *fileBackend) TurnStarted() {
	b.mu.Lock()
	b.inputOffset = 0
	b.armed = true
	b.generation++
	b.mu.Unlock()
}

// The recording is played in real time, so the samples are handed over
// at the rate the microphone runs at.

// Capture opens the microphone.
//
// The recording is played in real time, at the rate the microphone
// runs at. That matters: a microphone delivers its samples as the
// seconds pass, and a recording that was delivered as fast as the disk
// could read it would be over before the turn had begun.
func (b *fileBackend) Capture(ctx context.Context, rate int) (io.ReadCloser, error) {
	pcm, err := readWAV(b.input)
	if err != nil {
		return nil, err
	}
	return &fileReader{backend: b, pcm: pcm, rate: rate, started: time.Now()}, nil
}

// Playback opens the speaker.
func (b *fileBackend) Playback(ctx context.Context, rate int) (io.WriteCloser, error) {
	if b.output == "" {
		return nopWriteCloser{}, nil
	}
	file, err := os.OpenFile(b.output, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return nil, err
	}
	return &fileWriter{file: file}, nil
}

// OpenClockedOutput opens a speaker that reports when each period
// reaches the sound card.
//
// There is no sound card, so there is nothing to be clocked against.
// The speaker is opened without the clock, and the reply is finished
// when the queue is empty rather than when the card has played it.
func (b *fileBackend) OpenClockedOutput(ctx context.Context, rate, channels int) (audio.ClockedOutput, error) {
	return nil, errNoClock
}

// SelectedAudioDevices reports the devices in use.
func (b *fileBackend) SelectedAudioDevices() (string, string) {
	return b.input, b.output
}

// SelectAudioDevices chooses the devices.
func (b *fileBackend) SelectAudioDevices(input, output string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.input = input
	b.output = output
	return nil
}

// errNoClock says there is no sound card to be clocked against.
var errNoClock = fmt.Errorf("home-panel: there is no sound card")

// nopWriteCloser throws the audio away.
type nopWriteCloser struct{}

func (nopWriteCloser) Write(p []byte) (int, error) { return len(p), nil }
func (nopWriteCloser) Close() error                { return nil }

// fileReader plays the recording, and is quiet the rest of the time.
//
// The microphone is a room: it delivers its samples as the seconds
// pass, at the rate it runs at. A recording delivered as fast as the
// disk could read it would be over before the turn began, and would
// sound like one burst of noise rather than like words.
//
// So the reader has one clock, and everything is paced against it: the
// recording and the silence alike.
type fileReader struct {
	backend *fileBackend
	pcm     []byte
	// rate is how many samples a second the microphone runs at.
	rate int
	// delivered is how many samples have been handed over.
	//
	// It is the clock: the samples are handed over at the rate the
	// microphone runs at, so the recording takes as long as it is and
	// the silence takes as long as it is.
	delivered int64
	// started is when the clock started.
	started time.Time
	// generation is the pass of the recording the reader is on.
	generation int
}

// Read plays the recording, or the silence.
func (r *fileReader) Read(p []byte) (int, error) {
	r.backend.mu.Lock()
	offset := r.backend.inputOffset
	armed := r.backend.armed
	generation := r.backend.generation
	r.backend.mu.Unlock()

	// The recording has started again, so the clock does too.
	//
	// The frames that were read before the recording started again are
	// dropped. They are the tail of the room before the turn, and home
	// assistant's speech recognition hears them as part of the
	// question — so a question can come out as the tail of whatever was
	// in the room when the wake word fired.
	if generation != r.generation {
		r.generation = generation
		r.delivered = 0
		r.started = time.Now()
	}

	samples := int64(len(p) / 2)
	if armed && offset < len(r.pcm) {
		n := copy(p, r.pcm[offset:])
		r.backend.mu.Lock()
		r.backend.inputOffset += n
		r.backend.mu.Unlock()
		r.delivered += int64(n / 2)
	} else {
		// The room is quiet.
		for i := range p {
			p[i] = 0
		}
		r.delivered += samples
	}

	// The samples are handed over at the rate the microphone runs at.
	if r.rate > 0 {
		due := time.Duration(r.delivered) * time.Second / time.Duration(r.rate)
		wait := due - time.Since(r.started)
		if wait > 0 {
			time.Sleep(wait)
		}
	}
	return len(p), nil
}

// Close does nothing.
func (r *fileReader) Close() error { return nil }

// fileWriter writes the speaker's audio to the file.
type fileWriter struct {
	file *os.File
}

// Write writes the audio.
func (w *fileWriter) Write(p []byte) (int, error) {
	return w.file.Write(p)
}

// Close closes the file.
func (w *fileWriter) Close() error {
	return w.file.Close()
}

// readWAV reads the samples out of a wav file.
//
// The file is sixteen kilohertz, mono, sixteen bit, which is what the
// microphone produces. A file that is not is converted by the caller.
func readWAV(path string) ([]byte, error) {
	if path == "" {
		return nil, nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(raw) < 44 {
		return nil, fmt.Errorf("home-panel: %s is not a wav file", path)
	}
	for i := 12; i+8 < len(raw); i++ {
		if string(raw[i:i+4]) != "data" {
			continue
		}
		size := binary.LittleEndian.Uint32(raw[i+4 : i+8])
		start := i + 8
		// A streamed wav file has a size of all ones, which is not a
		// size at all.
		if size == 0xFFFFFFFF || int(size) > len(raw)-start {
			size = uint32(len(raw) - start)
		}
		return raw[start : start+int(size)], nil
	}
	return nil, fmt.Errorf("home-panel: %s has no samples in it", path)
}
