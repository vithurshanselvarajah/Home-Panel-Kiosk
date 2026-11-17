package wakeword

import (
	"errors"
	"fmt"
	"time"
)

const (
	SampleRate = 16000

	ChunkSamples = 1280

	MelBins = 32

	MelContextSamples = 480

	MelWindow = 76

	MelStep = 8

	MelBufMax = 970

	melScale  = 10.0
	melOffset = 2.0

	FeatDim = 96

	FeatWindow = 16

	FeatBufMax = 120

	rawBufMax = SampleRate * 10
)

const ScoreSpan = time.Duration((FeatWindow-1)*MelStep+MelWindow) * 10 * time.Millisecond

type Inferer interface {
	Melspec(samples []float32) (mel []float32, frames int, err error)
	Embed(window []float32) ([]float32, error)
	Classify(feats []float32) (float32, error)
}

type StreamEmbedder interface {
	Streaming() bool
	EmbedStep(frames []float32) ([]float32, error)
	ResetStream()
}

const streamPrime = (MelWindow + MelStep - 1) / MelStep

type Detector struct {
	inf Inferer

	raw       []int16
	remainder []int16
	accum     int

	mel     []float32
	feat    []float32
	scratch []float32

	stream   StreamEmbedder
	melTotal int
	streamAt int
	step     []float32
}

func New(inf Inferer) *Detector {
	if inf == nil {
		panic("wakeword: nil Inferer")
	}
	mel := make([]float32, MelWindow*MelBins)
	for i := range mel {
		mel[i] = 1.0
	}
	d := &Detector{
		inf:      inf,
		mel:      mel,
		scratch:  make([]float32, MelWindow*MelBins),
		melTotal: MelWindow,
		streamAt: -1,
		step:     make([]float32, MelStep*MelBins),
	}
	d.stream, _ = inf.(StreamEmbedder)
	return d
}

func (d *Detector) Ready() bool { return len(d.feat)/FeatDim >= FeatWindow }

func (d *Detector) Reset() {
	d.raw = d.raw[:0]
	d.remainder = nil
	d.accum = 0
	d.feat = nil
	d.mel = make([]float32, MelWindow*MelBins)
	for i := range d.mel {
		d.mel[i] = 1.0
	}
	d.melTotal = MelWindow
	d.streamAt = -1
}

func (d *Detector) LastEmbedding() []float32 {
	if len(d.feat) < FeatDim {
		return nil
	}
	return append([]float32(nil), d.feat[len(d.feat)-FeatDim:]...)
}

var ErrNotReady = errors.New("wakeword: fewer than 16 embeddings buffered")

func (d *Detector) Push(samples []int16) (n int, err error) {
	if len(d.remainder) > 0 {
		samples = append(append([]int16(nil), d.remainder...), samples...)
		d.remainder = nil
	}

	if d.accum+len(samples) >= ChunkSamples {
		if rem := (d.accum + len(samples)) % ChunkSamples; rem != 0 {
			d.remainder = append([]int16(nil), samples[len(samples)-rem:]...)
			samples = samples[:len(samples)-rem]
		}
	}
	d.bufferRaw(samples)
	d.accum += len(samples)

	if d.accum < ChunkSamples || d.accum%ChunkSamples != 0 {
		return 0, nil
	}

	chunks := d.accum / ChunkSamples
	if err := d.melspec(d.accum); err != nil {
		return 0, err
	}

	melFrames := len(d.mel) / MelBins
	for i := chunks - 1; i >= 0; i-- {
		end := melFrames - MelStep*i
		start := end - MelWindow
		if start < 0 || end > melFrames {

			continue
		}
		emb, err := d.embed(start, end)
		if err != nil {
			return 0, fmt.Errorf("wakeword: embed: %w", err)
		}
		if len(emb) != FeatDim {
			return 0, fmt.Errorf("wakeword: embed returned %d values, want %d", len(emb), FeatDim)
		}
		d.feat = append(d.feat, emb...)
		n++
	}
	if max := FeatBufMax * FeatDim; len(d.feat) > max {
		d.feat = append(d.feat[:0], d.feat[len(d.feat)-max:]...)
	}
	d.accum = 0
	return n, nil
}

func (d *Detector) embed(start, end int) ([]float32, error) {
	if d.stream != nil && d.stream.Streaming() {
		if emb, err := d.embedStream(end); err == nil {
			return emb, nil
		}
		d.streamAt = -1
	}
	copy(d.scratch, d.mel[start*MelBins:end*MelBins])
	return d.inf.Embed(d.scratch)
}

func (d *Detector) embedStream(end int) ([]float32, error) {
	abs := d.melTotal - (len(d.mel)/MelBins - end)
	if d.streamAt != abs-MelStep {
		d.stream.ResetStream()
		d.streamAt = -1
		for k := streamPrime - 1; k >= 1; k-- {
			if _, err := d.stream.EmbedStep(d.stepFrames(end - MelStep*k)); err != nil {
				return nil, err
			}
		}
	}
	emb, err := d.stream.EmbedStep(d.stepFrames(end))
	if err != nil {
		return nil, err
	}
	if len(emb) != FeatDim {
		return nil, fmt.Errorf("wakeword: streaming embed returned %d values, want %d", len(emb), FeatDim)
	}
	d.streamAt = abs
	return emb, nil
}

func (d *Detector) stepFrames(end int) []float32 {
	for i := range d.step {
		d.step[i] = 0
	}
	from := end - MelStep
	skip := 0
	if from < 0 {
		skip, from = -from, 0
	}
	copy(d.step[skip*MelBins:], d.mel[from*MelBins:end*MelBins])
	return d.step
}

func (d *Detector) Score() (float32, error) {
	if !d.Ready() {
		return 0, ErrNotReady
	}
	feats := d.feat[len(d.feat)-FeatWindow*FeatDim:]
	s, err := d.inf.Classify(feats)
	if err != nil {
		return 0, fmt.Errorf("wakeword: classify: %w", err)
	}
	return s, nil
}

func (d *Detector) bufferRaw(samples []int16) {
	d.raw = append(d.raw, samples...)
	if len(d.raw) > rawBufMax {
		d.raw = append(d.raw[:0], d.raw[len(d.raw)-rawBufMax:]...)
	}
}

func (d *Detector) melspec(nSamples int) error {
	want := nSamples + MelContextSamples
	if want > len(d.raw) {
		want = len(d.raw)
	}
	src := d.raw[len(d.raw)-want:]

	in := make([]float32, len(src))
	for i, s := range src {

		in[i] = float32(s)
	}

	out, frames, err := d.inf.Melspec(in)
	if err != nil {
		return fmt.Errorf("wakeword: melspec: %w", err)
	}
	if frames*MelBins != len(out) {
		return fmt.Errorf("wakeword: melspec returned %d values for %d frames", len(out), frames)
	}
	for _, v := range out {
		d.mel = append(d.mel, v/melScale+melOffset)
	}
	d.melTotal += frames
	if max := MelBufMax * MelBins; len(d.mel) > max {
		d.mel = append(d.mel[:0], d.mel[len(d.mel)-max:]...)
	}
	return nil
}
