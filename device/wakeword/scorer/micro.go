package scorer

import (
	"fmt"
	"os"

	"github.com/zserge/microwakeword"
	"github.com/zserge/microwakeword/audiofrontend"

	"github.com/vithurshanselvarajah/Home-Panel-Kiosk/audio/mwwfront"
)

const (
	WindowSize = 5

	FeatureStep = 10
)

const neverFires = 1.1

type microEngine struct {
	det   *microwakeword.Detector
	front *microFront

	window, step int
}

type featureFront interface {
	ProcessFrame(window []int16) []uint16
}

type microFront struct {
	fe           featureFront
	buf          []int16
	window, step int
}

func newMicroEngine(path string) (e *microEngine, err error) {
	defer func() {
		if r := recover(); r != nil {
			e = nil
			err = fmt.Errorf("scorer: loading %s: damaged model: %v", path, r)
		}
	}()

	if _, err := os.Stat(path); err != nil {
		return nil, fmt.Errorf("scorer: %w", err)
	}

	cfg := microwakeword.Config{
		ModelPath:         path,
		ProbabilityCutoff: neverFires,
		SlidingWindowSize: WindowSize,
		FeaturesStepMs:    FeatureStep,
	}
	det, err := microwakeword.NewDetector(cfg)
	if err != nil {
		return nil, fmt.Errorf("scorer: loading %s: %w", path, err)
	}

	fc := audiofrontend.DefaultConfig(FeatureStep)
	f := &microFront{
		fe:     audiofrontend.New(fc),
		window: fc.SampleRate * fc.WindowSizeMs / 1000,
		step:   fc.SampleRate * fc.StepSizeMs / 1000,
	}
	return &microEngine{det: det, front: f, window: f.window, step: f.step}, nil
}

func (e *microEngine) Push(samples []int16) (score float32, ok bool, err error) {
	defer func() {
		if r := recover(); r != nil {

			err = fmt.Errorf("scorer: the model failed on live audio: %v", r)
			ok = false
		}
	}()

	e.front.buf = append(e.front.buf, samples...)
	for len(e.front.buf) >= e.front.window {
		raw := e.front.fe.ProcessFrame(e.front.buf[:e.front.window])
		feat := make([]int8, mwwfront.Channels)
		mwwfront.Quantize(raw, feat)
		e.det.ProcessFeatures([][]int8{feat})
		e.front.buf = e.front.buf[e.front.step:]
	}
	return float32(e.det.SlidingAverage()), true, nil
}

func (e *microEngine) Reset() {
	e.front.buf = e.front.buf[:0]
	e.det.Reset()
}

func (e *microEngine) Info() string {
	return fmt.Sprintf("microwakeword (pure Go), window %d, step %dms", WindowSize, FeatureStep)
}

func (e *microEngine) Close() error { return nil }
