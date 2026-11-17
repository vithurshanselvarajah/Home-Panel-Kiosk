package main

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/vithurshanselvarajah/Home-Panel-Kiosk/device/wakeword/gate"
	"github.com/vithurshanselvarajah/Home-Panel-Kiosk/device/wakeword/scorer"
)

type wakeWord struct {
	scorer *scorer.Scorer

	gate *gate.Gate

	mu sync.Mutex

	model string

	enabled bool

	threshold float64

	onCross func(phrase string)
}

func openWakeWord(log *slog.Logger) (*wakeWord, error) {
	dir := scorer.Dir()

	if model, ok := scorer.MicroModel(dir); ok {
		return openMicroWakeWord(log, dir, model)
	}

	models, err := findModels(dir)
	if err != nil {
		return nil, err
	}

	wake := &wakeWord{
		gate:      gate.New(gate.Options{}),
		enabled:   true,
		threshold: defaultWakeThreshold,
	}

	model := models[0]
	opened, err := scorer.Open(filepath.Join(dir, model+".onnx"), float32(defaultWakeThreshold), wake.crossed)
	if err != nil {
		return nil, err
	}
	wake.scorer = opened
	wake.model = model
	for _, ignored := range models[1:] {
		log.Warn("an extra wake word model is ignored; only one is supported", "model", ignored)
	}

	log.Info("the local wake word is ready", "model", wake.model, "phrase", wake.scorer.Phrase())
	return wake, nil
}

func openMicroWakeWord(log *slog.Logger, dir, model string) (*wakeWord, error) {
	wake := &wakeWord{
		gate:      gate.New(gate.Options{}),
		enabled:   true,
		threshold: defaultWakeThreshold,
	}

	opened, err := scorer.OpenMicro(filepath.Join(dir, model+".tflite"),
		float32(defaultWakeThreshold), wake.crossed)
	if err != nil {
		return nil, err
	}
	wake.scorer = opened
	wake.model = model

	log.Info("the local wake word is ready", "model", wake.model, "phrase", wake.scorer.Phrase(),
		"engine", opened.Info())
	return wake, nil
}

const defaultWakeThreshold = 0.5

func findModels(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("the wake word directory %s could not be read: %w", dir, err)
	}

	var models []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(name, ".onnx") {
			continue
		}
		stem := strings.TrimSuffix(name, ".onnx")
		switch stem {
		case "melspectrogram", "embedding_model", "embedding_stream":

			continue
		}
		models = append(models, stem)
	}

	if len(models) == 0 {
		return nil, fmt.Errorf("no wake word model was found in %s", dir)
	}
	return models, nil
}

func (w *wakeWord) Push(pcm []byte, at time.Time) {
	if w == nil || w.scorer == nil {
		return
	}
	if !w.gate.Allow(pcm, at) {
		return
	}
	w.scorer.PushBytesAt(pcm, at)
}

func (w *wakeWord) Reset() {
	if w == nil {
		return
	}
	w.gate.Reset()
	if w.scorer != nil {
		w.scorer.Reset()
	}
}

func (w *wakeWord) crossed(score, threshold float32, at time.Time) {
	if w == nil {
		return
	}
	w.mu.Lock()
	enabled := w.enabled
	phrase := w.scorer.Phrase()
	w.mu.Unlock()

	if !enabled {
		return
	}
	if w.onCross != nil {
		w.onCross(phrase)
	}
}

func (w *wakeWord) SetEnabled(enabled bool) {
	if w == nil {
		return
	}
	w.mu.Lock()
	w.enabled = enabled
	w.mu.Unlock()
}

func (w *wakeWord) Threshold() float64 {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.threshold
}

func (w *wakeWord) SetThreshold(threshold float64) {
	if w == nil || w.scorer == nil {
		return
	}
	if threshold <= 0 {
		return
	}
	w.mu.Lock()
	w.threshold = threshold
	w.mu.Unlock()
	w.scorer.SetThreshold(float32(threshold))
}

func (w *wakeWord) SetBargeThreshold(threshold float64, active func() bool) {
	if w == nil || w.scorer == nil {
		return
	}
	w.scorer.SetBargeThreshold(float32(threshold), active)
}

func (w *wakeWord) Enabled() bool {
	if w == nil {
		return false
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.enabled
}

func (w *wakeWord) Ready() bool {
	return w != nil && w.scorer != nil
}

func (w *wakeWord) Phrase() string {
	if w == nil || w.scorer == nil {
		return ""
	}
	return w.scorer.Phrase()
}

func (w *wakeWord) Model() string {
	if w == nil {
		return ""
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.model
}

func (w *wakeWord) Amplitude() int {
	if w == nil {
		return 0
	}
	return w.gate.Amplitude()
}

func (w *wakeWord) Close() {
	if w == nil || w.scorer == nil {
		return
	}
	w.scorer.Close()
}
