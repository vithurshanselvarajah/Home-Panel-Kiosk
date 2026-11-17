package scorer

import (
	"os"
	"path/filepath"
	"strings"
	"time"
)

func OpenMicro(path string, threshold float32, onCross func(score, threshold float32, at time.Time)) (*Scorer, error) {
	eng, err := newMicroEngine(path)
	if err != nil {
		return nil, err
	}

	s := NewScorer(eng, threshold, onCross)
	s.closer = eng

	s.phrase = DisplayName(strings.TrimSuffix(filepath.Base(path), ".tflite"))
	s.info = eng.Info()
	return s, nil
}

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
