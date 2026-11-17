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

type fileBackend struct {
	input string

	output string

	mu sync.Mutex

	inputOffset int

	outputFile *os.File

	armed bool

	generation int
}

func newFileBackend(input, output string) *fileBackend {
	return &fileBackend{input: input, output: output}
}

func (b *fileBackend) TurnStarted() {
	b.mu.Lock()
	b.inputOffset = 0
	b.armed = true
	b.generation++
	b.mu.Unlock()
}

func (b *fileBackend) Capture(ctx context.Context, rate int) (io.ReadCloser, error) {
	pcm, err := readWAV(b.input)
	if err != nil {
		return nil, err
	}
	return &fileReader{backend: b, pcm: pcm, rate: rate, started: time.Now()}, nil
}

func (b *fileBackend) Playback(ctx context.Context, rate int) (io.WriteCloser, error) {
	if b.output == "" {
		return nopWriteCloser{}, nil
	}
	b.mu.Lock()
	if b.outputFile != nil {
		file := b.outputFile
		b.mu.Unlock()
		return &fileWriter{file: file}, nil
	}
	b.mu.Unlock()

	file, err := os.OpenFile(b.output, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return nil, err
	}
	b.mu.Lock()
	b.outputFile = file
	b.mu.Unlock()
	return &fileWriter{file: file}, nil
}

func (b *fileBackend) OpenClockedOutput(ctx context.Context, rate, channels int) (audio.ClockedOutput, error) {
	return nil, errNoClock
}

func (b *fileBackend) SelectedAudioDevices() (string, string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.input, b.output
}

func (b *fileBackend) SelectAudioDevices(input, output string) error {
	return nil
}

func (b *fileBackend) ChooseDefaultInput() error {
	return nil
}

var errNoClock = fmt.Errorf("home-panel: there is no sound card")

type nopWriteCloser struct{}

func (nopWriteCloser) Write(p []byte) (int, error) { return len(p), nil }
func (nopWriteCloser) Close() error                { return nil }

type fileReader struct {
	backend *fileBackend
	pcm     []byte

	rate int

	delivered int64

	started time.Time

	generation int
}

func (r *fileReader) Read(p []byte) (int, error) {
	r.backend.mu.Lock()
	offset := r.backend.inputOffset
	armed := r.backend.armed
	generation := r.backend.generation
	r.backend.mu.Unlock()

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

		for i := range p {
			p[i] = 0
		}
		r.delivered += samples
	}

	if r.rate > 0 {
		due := time.Duration(r.delivered) * time.Second / time.Duration(r.rate)
		wait := due - time.Since(r.started)
		if wait > 0 {
			time.Sleep(wait)
		}
	}
	return len(p), nil
}

func (r *fileReader) Close() error { return nil }

type fileWriter struct {
	file *os.File
}

func (w *fileWriter) Write(p []byte) (int, error) {
	return w.file.Write(p)
}

func (w *fileWriter) Close() error {
	return w.file.Sync()
}

func readWAV(path string) ([]byte, error) {
	if path == "" {
		return nil, nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	pcm, err := wavSamples(raw)
	if err != nil {
		return nil, fmt.Errorf("home-panel: %s: %w", path, err)
	}
	return pcm, nil
}

func wavSamples(raw []byte) ([]byte, error) {
	if len(raw) < 44 {
		return nil, fmt.Errorf("it is not a wav file")
	}
	for i := 12; i+8 < len(raw); i++ {
		if string(raw[i:i+4]) != "data" {
			continue
		}
		size := binary.LittleEndian.Uint32(raw[i+4 : i+8])
		start := i + 8

		if size == 0xFFFFFFFF || int(size) > len(raw)-start {
			size = uint32(len(raw) - start)
		}
		return raw[start : start+int(size)], nil
	}
	return nil, fmt.Errorf("it has no samples in it")
}
