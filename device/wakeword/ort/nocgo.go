//go:build !cgo

// Package ort is the ONNX Runtime binding for the wake word.
//
// This file is the build without cgo. The runtime is loaded through cgo —
// it is a C library — so a build with cgo turned off cannot have it, and
// this file says so plainly rather than failing at link time.
//
// The build without cgo is the one that ships: the binaries cross compile
// for arm64 without a C toolchain, and the runtime is installed on the
// device separately and loaded at run time by the cgo build.
//
// So the device program built for distribution has no local wake word in
// it. That is not a fault: the wake word is optional, and the panel
// works without it. A device that wants a local wake word is built with
// cgo, or the runtime is installed and the panel is built to use it.

package ort

import "errors"

// ErrNoCgo says the binary was built without cgo, so it cannot load the
// ONNX Runtime.
var ErrNoCgo = errors.New("ort: this build has no cgo, so it cannot load the onnx runtime")

// Options controls session creation. It is here so the code that builds
// the options compiles either way.
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

// Open cannot open the runtime without cgo.
func Open(libPath string) (*Runtime, error) { return nil, ErrNoCgo }

// Version says there is no runtime.
func (r *Runtime) Version() string { return "none" }

// NewInferer cannot make an inferer without cgo.
func (r *Runtime) NewInferer(m Models, o Options) (*Inferer, error) { return nil, ErrNoCgo }

// Inferer scores audio against the models.
type Inferer struct{}

// Streaming says there is no streaming model.
func (i *Inferer) Streaming() bool { return false }

// StreamStatus says there is no streaming model.
func (i *Inferer) StreamStatus() string { return "none" }

// ResetStream does nothing.
func (i *Inferer) ResetStream() {}

// EmbedStep cannot embed without cgo.
func (i *Inferer) EmbedStep(frames []float32) ([]float32, error) { return nil, ErrNoCgo }

// XNNPACKActive says there is no runtime.
func (i *Inferer) XNNPACKActive() bool { return false }

// Close does nothing.
func (i *Inferer) Close() error { return nil }

// Melspec cannot compute without cgo.
func (i *Inferer) Melspec(samples []float32) ([]float32, int, error) { return nil, 0, ErrNoCgo }

// Embed cannot embed without cgo.
func (i *Inferer) Embed(window []float32) ([]float32, error) { return nil, ErrNoCgo }

// Classify cannot classify without cgo.
func (i *Inferer) Classify(feats []float32) (float32, error) { return 0, ErrNoCgo }
