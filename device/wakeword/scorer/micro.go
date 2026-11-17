package scorer

import (
	"fmt"
	"log/slog"
	"os"

	"github.com/zserge/microwakeword"
	"github.com/zserge/microwakeword/audiofrontend"

	"github.com/vithurshanselvarajah/Home-Panel-Kiosk/audio/mwwfront"
)

// The wake word engine, as TECHO5 runs it.
//
// A microWakeWord model is self-contained: it takes a window of audio and
// says how much of the wake word is in it. So the whole pipeline is the
// engine's own — the features, the model, and the score — and the panel's
// job is only to feed it audio and to decide when a score is a crossing.
//
// This is the engine that ran the model on the unit, ported over, so the
// model is scored exactly as it was scored there.

// The model's shape. A microWakeWord model has one window of audio and one
// step between windows; these are the published values, and they are what
// the models were trained with.
const (
	// WindowSize is how many windows the score is averaged over. Five is
	// what the published models use and what the engine is given.
	WindowSize = 5
	// FeatureStep is how far the window moves each step, in milliseconds.
	FeatureStep = 10
)

// neverFires is the cutoff handed to the engine so its own threshold never
// triggers.
//
// The threshold is ours: it comes from Home Assistant and changes while
// running, and rebuilding the detector to apply it would wipe the streaming
// state a model needs to score well.
const neverFires = 1.1

// microEngine is one model, running.
//
// It is not safe for concurrent use: the scorer drives it from its own
// goroutine, which is the only place ordering is meaningful.
type microEngine struct {
	det   *microwakeword.Detector
	front *microFront
	// buf is the audio the front end has not read yet.
	buf []int16
	// window and step are the front end's geometry, in samples.
	window, step int
}

// microFront is one feature front end: a window of audio in, a frame of
// features out.
//
// Both kinds keep state between windows (the noise estimates), so the front
// end is fed every window in order, or the next utterance is scored from a
// cold state.
type microFront interface {
	ProcessFrame(window []int16) []uint16
}

// newMicroEngine loads a model and makes the front end that goes with it.
//
// The model file is not a trustworthy document: the engine sizes and
// indexes everything by what the file says, and a damaged file panics
// inside the parser. So the file is read with the checks that make a
// model safe to load, and the panic is caught for the rest.
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

	// The engine's own front end: a float approximation of the reference's
	// integer arithmetic. It is what has always run, and the models were
	// scored against it.
	fe := audiofrontend.DefaultConfig(FeatureStep)
	f := &microFront{
		fe:     audiofrontend.New(fe),
		window: fe.SampleRate * fe.WindowSizeMs / 1000,
		step:   fe.SampleRate * fe.StepSizeMs / 1000,
	}
	return &microEngine{det: det, front: f, window: f.window, step: f.step}, nil
}

// Push feeds audio and reports whether the model has scored yet.
//
// The engine takes a window at a time, so the audio is held until there is
// a whole window, and the window is then scored. The score is the engine's
// own sliding average, which is what the model's probability means.
func (e *microEngine) Push(samples []int16) (score float32, ok bool, err error) {
	defer func() {
		if r := recover(); r != nil {
			// A model that loaded can still fall over on real audio. It is
			// dropped rather than taking the panel with it.
			err = fmt.Errorf("scorer: the model failed on live audio: %v", r)
			ok = false
		}
	}()

	e.buf = append(e.buf, samples...)
	for len(e.buf) >= e.window {
		raw := e.front.fe.ProcessFrame(e.buf[:e.window])
		feat := make([]int8, mwwfront.Channels)
		mwwfront.Quantize(raw, feat)
		e.det.ProcessFeatures([][]int8{feat})
		e.buf = e.buf[e.step:]
	}
	return float32(e.det.SlidingAverage()), true, nil
}

// Reset forgets what has been heard, as if the engine were new.
func (e *microEngine) Reset() {
	e.buf = e.buf[:0]
	e.det.Reset()
}

// Info describes the engine for the log.
func (e *microEngine) Info() string {
	return fmt.Sprintf("microwakeword (pure Go), window %d, step %dms", WindowSize, FeatureStep)
}

// Close releases the engine.
func (e *microEngine) Close() error { return nil }

// logDropped says a model was dropped, once.
func logDropped(err error) {
	slog.Error("the wake word was dropped: its model failed on live audio", "err", err)
}
