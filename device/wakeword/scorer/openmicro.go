package scorer

import (
	"os"
	"path/filepath"
	"strings"
	"time"
)

// OpenMicro loads a microWakeWord model.
//
// A microWakeWord model is self-contained: it takes a window of audio and
// says how much of the wake word is in it. So there is no shared mel and
// embedding chain to load, and no ONNX runtime to load either — the model
// and the engine are all there is.
func OpenMicro(path string, threshold float32, onCross func(score, threshold float32, at time.Time)) (*Scorer, error) {
	eng, err := newMicroEngine(path)
	if err != nil {
		return nil, err
	}

	s := NewScorer(eng, threshold, onCross)
	s.closer = eng
	// The phrase is the model's name, as a person says it: the extension is
	// part of the file, not part of the phrase.
	s.phrase = DisplayName(strings.TrimSuffix(filepath.Base(path), ".tflite"))
	s.info = eng.Info()
	return s, nil
}

// MicroModel is the name of a microWakeWord model in a directory.
//
// The models are named for what they are: a .tflite is a microWakeWord
// model, and a .onnx is an openWakeWord one.
func MicroModel(dir string) (string, bool) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", false
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(name, ".tflite") {
			continue
		}
		return strings.TrimSuffix(name, ".tflite"), true
	}
	return "", false
}
