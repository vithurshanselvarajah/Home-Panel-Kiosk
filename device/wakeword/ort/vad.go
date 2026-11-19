//go:build onnxruntime

package ort

/*
#include "shim.h"
*/
import "C"

import (
	"fmt"
	"unsafe"
)

const VADChunk = 640

const vadState = 128

type VAD struct {
	md *model
}

type VADStream struct {
	v    *VAD
	h, c [vadState]float32
}

func (r *Runtime) NewVAD(path string, o Options) (*VAD, error) {
	md, err := r.load(path, "vad", o)
	if err != nil {
		return nil, err
	}
	return &VAD{md: md}, nil
}

func (v *VAD) XNNPACKActive() bool { return v.md.m.xnnpack != 0 }

func (v *VAD) Stream() *VADStream { return &VADStream{v: v} }

func (s *VADStream) Prob(samples []float32) (float32, error) {
	var sum float32
	n := 0
	for i := 0; i+VADChunk <= len(samples); i += VADChunk {
		var p C.float
		err := goErr(C.em_vad_run(&s.v.md.m, (*C.float)(unsafe.Pointer(&samples[i])), VADChunk,
			(*C.float)(unsafe.Pointer(&s.h[0])), (*C.float)(unsafe.Pointer(&s.c[0])), &p))
		if err != nil {
			return 0, fmt.Errorf("ort: run vad: %w", err)
		}
		sum += float32(p)
		n++
	}
	if n == 0 {
		return 0, nil
	}
	return sum / float32(n), nil
}

func (v *VAD) Close() { C.em_model_free(&v.md.m) }
