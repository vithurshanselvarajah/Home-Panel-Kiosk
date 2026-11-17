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

// DefaultDir is where the wake word models live on the panel.
//
// Overridable with HOME_PANEL_WAKEWORD_DIR, so the same binary can be
// pointed at a scratch directory while iterating.
//
// The models are not part of the panel: they are a few megabytes, they
// change far less often than the application, and a model is chosen by the
// person who installs it rather than by the build. The ONNX Runtime itself
// IS part of the panel — it is linked in — so there is nothing else to
// install.
const DefaultDir = "/usr/share/home-panel/wakeword"

// Dir returns the configured model directory.
func Dir() string {
	if d := os.Getenv("HOME_PANEL_WAKEWORD_DIR"); d != "" {
		return d
	}
	return DefaultDir
}

// ModelStem turns a configured wake word model into the classifier filename
// stem.
//
// The value is either a bare name ("hey_jarvis") or a file path to a custom
// model ("/models/custom.onnx"). The rule is: strip to the filename stem only
// when the value ends in ".onnx", otherwise pass it through untouched.
//
// The untouched case is the whole point, and getting it wrong is not
// hypothetical — an earlier version used filepath.Ext, which sees ".1" as the
// extension of "hey_jarvis_v0.1" and went looking for hey_jarvis_v0.onnx. A
// version suffix is not a file extension.
func ModelStem(model string) string {
	name := strings.TrimSpace(model)
	if !strings.HasSuffix(name, ".onnx") {
		return name
	}
	return strings.TrimSuffix(filepath.Base(name), ".onnx")
}

// Open loads ONNX Runtime and the three models for the given wake word, and
// starts a Scorer.
//
// A missing library or model returns an error and is NOT a failure of the
// panel: the local wake word is off until someone installs them, the caller
// logs it once and carries on without it. That is the whole reason the
// runtime is dlopen'd rather than linked.
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
	// Checked up front so the log names the missing file. ORT's own error for a
	// bad path mentions neither which of the three it was nor what was expected
	// there, and this is a path people will get wrong while installing by hand.
	for what, p := range map[string]string{
		"melspectrogram model": models.Melspec,
		"embedding model":      models.Embedding,
		"classifier model":     models.Classifier,
	} {
		if _, err := os.Stat(p); err != nil {
			return nil, fmt.Errorf("scorer: %s not installed at %s", what, p)
		}
	}

	// The streaming embedding is optional (#893): without the file, or with
	// one that fails its self-check, the stock model scores as before.
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

// DisplayName turns a model name into the phrase a person says.
//
// The openWakeWord models carry a version suffix in their filename —
// "hey_jarvis_v0.1" — which is a packaging convention and not part of the
// wake word. The phrase is what somebody says, and what Home Assistant
// shows in its picker.
//
// It is worth being exact about why this matters: Home Assistant compares
// the phrase on a pipeline start against the state of its wake-word select
// entity, whose options are the names that were advertised. So the phrase
// the panel announces and the name it advertises have to be the same
// string, or Home Assistant matches neither and falls back to the first
// pipeline.
//
// One function produces both, so the two cannot drift.
func DisplayName(model string) string {
	name := ModelStem(model)
	// The suffix is a version, not part of the name.
	name = versionSuffix.ReplaceAllString(name, "")
	// The underscores are where the spaces go.
	name = strings.ReplaceAll(name, "_", " ")
	return strings.TrimSpace(name)
}

// versionSuffix is the version a model filename carries.
var versionSuffix = regexp.MustCompile(`_v\d+(\.\d+)*$`)

// EmbeddingStreamFile is the streaming embedding model's name in Dir(). It
// must match the streaming embedding model the panel installs
// it.
const EmbeddingStreamFile = "embedding_stream.onnx"

// StreamFileSig identifies the streaming model file as it is now: empty when
// absent, otherwise its size and modification time. The caller rebuilds the
// scorer when this changes, which is how a model installed while the device
// is running comes into use without a restart. A file that fails its
// self-check keeps its signature, so it is tried once and not on every push.
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
