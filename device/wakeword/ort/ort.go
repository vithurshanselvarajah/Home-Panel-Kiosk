//go:build onnxruntime

package ort

/*
#cgo CFLAGS: -I${SRCDIR}/include -O2
#cgo LDFLAGS: -lonnxruntime

#include "shim.h"
*/
import "C"

import (
	"errors"
	"fmt"
	"math"
	"sync"
	"unsafe"

	"github.com/vithurshanselvarajah/Home-Panel-Kiosk/device/wakeword"
)

type Options struct {
	Threads int

	XNNPACK bool

	AllowSpinning bool
}

func DefaultOptions() Options {
	return Options{Threads: 1, XNNPACK: true, AllowSpinning: false}
}

type Runtime struct {
	rt      C.em_runtime
	version string
}

func Open(libPath string) (*Runtime, error) {
	openMu.Lock()
	defer openMu.Unlock()

	if r := runtimes[libPath]; r != nil {
		return r, nil
	}

	cPath := C.CString(libPath)
	defer C.free(unsafe.Pointer(cPath))

	r := &Runtime{}
	if err := goErr(C.em_ort_open(cPath, &r.rt)); err != nil {
		return nil, fmt.Errorf("ort: open %s: %w", libPath, err)
	}
	r.version = C.GoString(C.em_ort_version(&r.rt))
	runtimes[libPath] = r
	return r, nil
}

var (
	openMu   sync.Mutex
	runtimes = map[string]*Runtime{}
)

func (r *Runtime) Version() string { return r.version }

type Models struct {
	Melspec         string
	Embedding       string
	Classifier      string
	EmbeddingStream string
}

type model struct {
	m    C.em_model
	name string

	out []float32
}

func (r *Runtime) load(path, name string, o Options) (*model, error) {
	if path == "" {
		return nil, fmt.Errorf("ort: no path for the %s model", name)
	}
	cPath := C.CString(path)
	defer C.free(unsafe.Pointer(cPath))

	md := &model{name: name}
	err := goErr(C.em_model_load(&r.rt, cPath, C.int(o.Threads), cBool(o.XNNPACK),
		cBool(o.AllowSpinning), &md.m))
	if err != nil {
		return nil, fmt.Errorf("ort: load %s model %s: %w", name, path, err)
	}
	return md, nil
}

type Inferer struct {
	mel, emb, cls *model

	xnnpack bool

	stream    *streamModel
	streamOff string
}

type streamModel struct {
	s     C.em_stream
	state []float32
	emb   []float32
}

func (r *Runtime) NewInferer(m Models, o Options) (*Inferer, error) {
	if o.Threads < 1 {
		return nil, fmt.Errorf("ort: Threads must be at least 1, got %d", o.Threads)
	}
	inf := &Inferer{}
	var err error
	if inf.mel, err = r.load(m.Melspec, "melspectrogram", o); err != nil {
		return nil, err
	}

	inf.xnnpack = inf.mel.m.xnnpack != 0
	if inf.emb, err = r.load(m.Embedding, "embedding", o); err != nil {
		inf.Close()
		return nil, err
	}
	if inf.cls, err = r.load(m.Classifier, "classifier", o); err != nil {
		inf.Close()
		return nil, err
	}
	inf.openStream(r, m.EmbeddingStream, o)
	return inf, nil
}

func (i *Inferer) openStream(r *Runtime, path string, o Options) {
	if path == "" {
		i.streamOff = "no streaming model given"
		return
	}
	cPath := C.CString(path)
	defer C.free(unsafe.Pointer(cPath))
	sm := &streamModel{emb: make([]float32, wakeword.FeatDim)}
	if err := goErr(C.em_stream_load(&r.rt, cPath, C.int(o.Threads), cBool(o.XNNPACK),
		cBool(o.AllowSpinning), &sm.s)); err != nil {
		i.streamOff = "load: " + err.Error()
		return
	}
	sm.state = make([]float32, int(sm.s.total))
	i.stream = sm
	if err := i.verifyStream(); err != nil {
		C.em_stream_free(&sm.s)
		i.stream = nil
		i.streamOff = "self-check: " + err.Error()
		return
	}
	i.ResetStream()
}

const streamCheckSteps = 12

func (i *Inferer) verifyStream() error {
	const step = wakeword.MelStep * wakeword.MelBins
	mel := make([]float32, streamCheckSteps*step)
	seed := uint64(0x9E3779B97F4A7C15)
	for k := range mel {
		seed = seed*6364136223846793005 + 1442695040888963407

		mel[k] = float32((seed>>33)%2000000)/1000000*6 - 6
	}
	i.ResetStream()
	compared := 0
	for j := 0; j < streamCheckSteps; j++ {
		got, err := i.EmbedStep(mel[j*step : (j+1)*step])
		if err != nil {
			return err
		}
		end := (j + 1) * wakeword.MelStep
		if end < wakeword.MelWindow || j < streamCheckSteps-3 {
			continue
		}
		window := mel[(end-wakeword.MelWindow)*wakeword.MelBins : end*wakeword.MelBins]
		want, err := i.Embed(window)
		if err != nil {
			return err
		}
		var worst, scale float64
		for k := range want {
			if d := math.Abs(float64(got[k]) - float64(want[k])); d > worst {
				worst = d
			}
			if a := math.Abs(float64(want[k])); a > scale {
				scale = a
			}
		}

		if worst > 1e-3*math.Max(scale, 1) {
			return fmt.Errorf("differs from the stock model by %.3g on values up to %.3g", worst, scale)
		}
		compared++
	}
	if compared == 0 {
		return errors.New("compared nothing")
	}
	return nil
}

func (i *Inferer) Streaming() bool { return i.stream != nil }

func (i *Inferer) StreamStatus() string {
	if i.stream != nil {
		return "on"
	}
	return i.streamOff
}

func (i *Inferer) ResetStream() {
	if i.stream == nil {
		return
	}
	for k := range i.stream.state {
		i.stream.state[k] = 0
	}
}

func (i *Inferer) EmbedStep(frames []float32) ([]float32, error) {
	sm := i.stream
	if sm == nil {
		return nil, errors.New("ort: no streaming embedding model")
	}
	if want := wakeword.MelStep * wakeword.MelBins; len(frames) != want {
		return nil, fmt.Errorf("ort: embed step wants %d values, got %d", want, len(frames))
	}
	err := goErr(C.em_stream_run(&sm.s, (*C.float)(unsafe.Pointer(&frames[0])),
		(*C.float)(unsafe.Pointer(&sm.state[0])), (*C.float)(unsafe.Pointer(&sm.emb[0]))))
	if err != nil {
		return nil, fmt.Errorf("ort: run streaming embedding: %w", err)
	}
	return sm.emb, nil
}

func (i *Inferer) XNNPACKActive() bool { return i.xnnpack }

func (i *Inferer) Close() error {
	for _, m := range []*model{i.mel, i.emb, i.cls} {
		if m != nil {
			C.em_model_free(&m.m)
		}
	}
	i.mel, i.emb, i.cls = nil, nil, nil
	if i.stream != nil {
		C.em_stream_free(&i.stream.s)
		i.stream = nil
		i.streamOff = "closed"
	}
	return nil
}

var ErrClosed = errors.New("ort: inferer is closed")

func (i *Inferer) Melspec(samples []float32) ([]float32, int, error) {
	if i.mel == nil {
		return nil, 0, ErrClosed
	}
	if len(samples) == 0 {
		return nil, 0, errors.New("ort: melspec called with no samples")
	}
	shape := [2]C.int64_t{1, C.int64_t(len(samples))}
	out, err := i.run(i.mel, samples, shape[:])
	if err != nil {
		return nil, 0, err
	}
	if len(out)%wakeword.MelBins != 0 {
		return nil, 0, fmt.Errorf("ort: melspec produced %d values, not a multiple of %d bins",
			len(out), wakeword.MelBins)
	}
	return out, len(out) / wakeword.MelBins, nil
}

func (i *Inferer) Embed(window []float32) ([]float32, error) {
	if i.emb == nil {
		return nil, ErrClosed
	}
	if want := wakeword.MelWindow * wakeword.MelBins; len(window) != want {
		return nil, fmt.Errorf("ort: embed wants %d values, got %d", want, len(window))
	}
	shape := [4]C.int64_t{1, wakeword.MelWindow, wakeword.MelBins, 1}
	out, err := i.run(i.emb, window, shape[:])
	if err != nil {
		return nil, err
	}
	if len(out) != wakeword.FeatDim {
		return nil, fmt.Errorf("ort: embed produced %d values, want %d", len(out), wakeword.FeatDim)
	}
	return out, nil
}

func (i *Inferer) Classify(feats []float32) (float32, error) {
	if i.cls == nil {
		return 0, ErrClosed
	}
	if want := wakeword.FeatWindow * wakeword.FeatDim; len(feats) != want {
		return 0, fmt.Errorf("ort: classify wants %d values, got %d", want, len(feats))
	}
	shape := [3]C.int64_t{1, wakeword.FeatWindow, wakeword.FeatDim}
	out, err := i.run(i.cls, feats, shape[:])
	if err != nil {
		return 0, err
	}
	if len(out) != 1 {
		return 0, fmt.Errorf("ort: classify produced %d values, want 1", len(out))
	}
	return out[0], nil
}

func (i *Inferer) run(m *model, in []float32, shape []C.int64_t) ([]float32, error) {
	var (
		outPtr *C.float
		outN   C.size_t
	)
	err := goErr(C.em_model_run(&m.m,
		(*C.float)(unsafe.Pointer(&in[0])), C.size_t(len(in)),
		&shape[0], C.size_t(len(shape)),
		&outPtr, &outN))
	if err != nil {
		return nil, fmt.Errorf("ort: run %s: %w", m.name, err)
	}
	if outPtr == nil {
		return nil, fmt.Errorf("ort: run %s: no output", m.name)
	}
	defer C.free(unsafe.Pointer(outPtr))
	m.out = append(m.out[:0], unsafe.Slice((*float32)(unsafe.Pointer(outPtr)), int(outN))...)
	return m.out, nil
}

func goErr(msg *C.char) error {
	if msg == nil {
		return nil
	}
	defer C.free(unsafe.Pointer(msg))
	return errors.New(C.GoString(msg))
}

func cBool(b bool) C.int {
	if b {
		return 1
	}
	return 0
}

var _ wakeword.Inferer = (*Inferer)(nil)
