//go:build !cgo

package ort

import "errors"

var ErrNoCgo = errors.New("ort: this build has no cgo, so it cannot load the onnx runtime")

type Options struct {
	Threads       int
	XNNPACK       bool
	AllowSpinning bool
}

func DefaultOptions() Options {
	return Options{Threads: 1, XNNPACK: true}
}

type Models struct {
	Melspec         string
	Embedding       string
	Classifier      string
	EmbeddingStream string
}

type Runtime struct{}

func Open(libPath string) (*Runtime, error) { return nil, ErrNoCgo }

func (r *Runtime) Version() string { return "none" }

func (r *Runtime) NewInferer(m Models, o Options) (*Inferer, error) { return nil, ErrNoCgo }

type Inferer struct{}

func (i *Inferer) Streaming() bool { return false }

func (i *Inferer) StreamStatus() string { return "none" }

func (i *Inferer) ResetStream() {}

func (i *Inferer) EmbedStep(frames []float32) ([]float32, error) { return nil, ErrNoCgo }

func (i *Inferer) XNNPACKActive() bool { return false }

func (i *Inferer) Close() error { return nil }

func (i *Inferer) Melspec(samples []float32) ([]float32, int, error) { return nil, 0, ErrNoCgo }

func (i *Inferer) Embed(window []float32) ([]float32, error) { return nil, ErrNoCgo }

func (i *Inferer) Classify(feats []float32) (float32, error) { return 0, ErrNoCgo }
