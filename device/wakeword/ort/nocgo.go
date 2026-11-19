//go:build !onnxruntime || !cgo

package ort

import "errors"

// This file is the build without the ONNX Runtime.
//
// The binding is a C library, so it needs cgo and the library itself at link
// time. A build without the runtime cannot have it, and this file says so
// plainly rather than failing at link time.
//
// The API here is the same as the one in the binding, so everything that
// calls the package compiles either way. A caller that asks for a runtime
// gets ErrNoCgo, which the device program treats as "there is no local wake
// word": the panel still works, and Home Assistant does the detection.
//
// The build with the runtime is the one that ships, and it is selected with
// the onnxruntime tag.

var ErrNoCgo = errors.New("ort: this build has no cgo, so it cannot load the onnx runtime")

// ErrClosed says the inferer is closed. It is the same error the binding
// returns, so a caller can test for it either way.
var ErrClosed = errors.New("ort: inferer is closed")

// VADChunk is the number of samples the voice activity detector reads at a
// time.
const VADChunk = 640

// Options controls session creation. It is here so the code that builds the
// options compiles either way.
type Options struct {
	Threads       int
	XNNPACK       bool
	AllowSpinning bool
}

// DefaultOptions returns the options the runtime is created with.
func DefaultOptions() Options {
	return Options{Threads: 1, XNNPACK: true}
}

// Models names the files the pipeline is made of.
type Models struct {
	Melspec         string
	Embedding       string
	Classifier      string
	EmbeddingStream string
}

// Runtime is the ONNX Runtime.
type Runtime struct{}

// Open cannot open the runtime without it.
func Open(libPath string) (*Runtime, error) { return nil, ErrNoCgo }

// Version says there is no runtime.
func (r *Runtime) Version() string { return "none" }

// NewInferer cannot make an inferer without the runtime.
func (r *Runtime) NewInferer(m Models, o Options) (*Inferer, error) { return nil, ErrNoCgo }

// NewVAD cannot make a detector without the runtime.
func (r *Runtime) NewVAD(path string, o Options) (*VAD, error) { return nil, ErrNoCgo }

// Inferer scores audio against the models.
type Inferer struct{}

// Streaming says there is no streaming model.
func (i *Inferer) Streaming() bool { return false }

// StreamStatus says there is no streaming model.
func (i *Inferer) StreamStatus() string { return "none" }

// ResetStream does nothing.
func (i *Inferer) ResetStream() {}

// EmbedStep cannot embed without the runtime.
func (i *Inferer) EmbedStep(frames []float32) ([]float32, error) { return nil, ErrNoCgo }

// XNNPACKActive says there is no runtime.
func (i *Inferer) XNNPACKActive() bool { return false }

// Close does nothing.
func (i *Inferer) Close() error { return nil }

// Melspec cannot compute without the runtime.
func (i *Inferer) Melspec(samples []float32) ([]float32, int, error) { return nil, 0, ErrNoCgo }

// Embed cannot embed without the runtime.
func (i *Inferer) Embed(window []float32) ([]float32, error) { return nil, ErrNoCgo }

// Classify cannot classify without the runtime.
func (i *Inferer) Classify(feats []float32) (float32, error) { return 0, ErrNoCgo }

// VAD is the voice activity detector.
type VAD struct{}

// XNNPACKActive says there is no runtime.
func (v *VAD) XNNPACKActive() bool { return false }

// Stream returns a detector stream.
func (v *VAD) Stream() *VADStream { return &VADStream{} }

// Close does nothing.
func (v *VAD) Close() {}

// VADStream carries the state of one stream through the detector.
type VADStream struct{}

// Prob cannot detect without the runtime.
func (s *VADStream) Prob(samples []float32) (float32, error) { return 0, ErrNoCgo }
