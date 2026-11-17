package scorer

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/vithurshanselvarajah/Home-Panel-Kiosk/device/wakeword/ort"
)

const DefaultDir = "/usr/share/home-panel/wakeword"

func Dir() string {
	if d := os.Getenv("HOME_PANEL_WAKEWORD_DIR"); d != "" {
		return d
	}
	return DefaultDir
}

func ModelStem(model string) string {
	name := strings.TrimSpace(model)
	if !strings.HasSuffix(name, ".onnx") {
		return name
	}
	return strings.TrimSuffix(filepath.Base(name), ".onnx")
}

func Open(model string, threshold float32, onCross func(score, threshold float32, at time.Time)) (*Scorer, error) {
	dir := Dir()
	stem := ModelStem(model)
	if stem == "" {
		return nil, fmt.Errorf("scorer: no wake word model configured")
	}

	models := ort.Models{
		Melspec:    filepath.Join(dir, "melspectrogram.onnx"),
		Embedding:  filepath.Join(dir, "embedding_model.onnx"),
		Classifier: filepath.Join(dir, stem+".onnx"),
	}

	for what, p := range map[string]string{
		"melspectrogram model": models.Melspec,
		"embedding model":      models.Embedding,
		"classifier model":     models.Classifier,
	} {
		if _, err := os.Stat(p); err != nil {
			return nil, fmt.Errorf("scorer: %s not installed at %s", what, p)
		}
	}

	if p := filepath.Join(dir, EmbeddingStreamFile); fileExists(p) {
		models.EmbeddingStream = p
	}

	rt, err := ort.Open("onnxruntime")
	if err != nil {
		return nil, fmt.Errorf("scorer: %w", err)
	}
	inf, err := rt.NewInferer(models, ort.DefaultOptions())
	if err != nil {
		return nil, fmt.Errorf("scorer: %w", err)
	}

	s := NewScorer(newOWWEngine(inf), threshold, onCross)
	s.closer = inf
	s.phrase = DisplayName(stem)
	s.info = fmt.Sprintf("onnxruntime %s, model %s, xnnpack=%v, streaming embedding: %s",
		rt.Version(), stem, inf.XNNPACKActive(), inf.StreamStatus())
	return s, nil
}

func DisplayName(model string) string {
	name := ModelStem(model)

	name = versionSuffix.ReplaceAllString(name, "")

	name = strings.ReplaceAll(name, "_", " ")
	return strings.TrimSpace(name)
}

var versionSuffix = regexp.MustCompile(`_v\d+(\.\d+)*$`)

const EmbeddingStreamFile = "embedding_stream.onnx"

func StreamFileSig() string {
	st, err := os.Stat(filepath.Join(Dir(), EmbeddingStreamFile))
	if err != nil || st.IsDir() {
		return ""
	}
	return fmt.Sprintf("%d@%d", st.Size(), st.ModTime().UnixNano())
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}
