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

// wakeWord is the panel's local wake word.
//
// It is the reason the room stays in the room: the panel scores the
// microphone itself and sends nothing until the wake word fires, so the
// audio that leaves the machine is the few seconds of the command and
// nothing else.
//
// The gate is what makes it affordable. Scoring every frame of every
// second would cost about a third of a core for ever, and the panel is
// on a device with two gigabytes of memory and four cores it is sharing
// with a browser. So the audio is measured before it is scored, and only
// the frames loud enough to be somebody talking reach the scorer.
type wakeWord struct {
	// scorer scores the audio that the gate lets through.
	scorer *scorer.Scorer
	// gate decides which frames are worth scoring.
	gate *gate.Gate

	// mu guards the settings.
	mu sync.Mutex
	// model is the model that is loaded.
	model string
	// enabled says whether the wake word is switched on.
	enabled bool
	// threshold is the score the wake word has to reach.
	threshold float64

	// onCross is called when the wake word fires.
	onCross func(phrase string)
}

// openWakeWord loads the wake word.
//
// It returns an error when the models or the runtime are not installed,
// which is an ordinary condition rather than a fault: the panel works
// without a local wake word, and home assistant can still ask it to
// speak.
//
// The model and the runtime are installed by hand, out of band. The
// runtime is twelve megabytes and the models are a few more, and they
// change far less often than the application does.
func openWakeWord(log *slog.Logger) (*wakeWord, error) {
	dir := scorer.Dir()

	// The models are looked for in the directory, and the classifier is
	// whichever one is there. A panel with one model has one wake word.
	models, err := findModels(dir)
	if err != nil {
		return nil, err
	}

	wake := &wakeWord{
		gate:      gate.New(gate.Options{}),
		enabled:   true,
		threshold: defaultWakeThreshold,
	}

	// The scorer is opened with the first model, and the others are
	// loaded as well so that a panel with two models hears both.
	for _, model := range models {
		opened, err := scorer.Open(filepath.Join(dir, model+".onnx"), float32(defaultWakeThreshold), wake.crossed)
		if err != nil {
			if wake.scorer == nil {
				return nil, err
			}
			// One model loaded is enough to carry on. A second that will
			// not load is a problem with that model, not with the panel.
			log.Warn("a wake word model could not be loaded", "model", model, "error", err)
			continue
		}
		if wake.scorer == nil {
			wake.scorer = opened
			wake.model = model
		} else {
			log.Info("a second wake word model was loaded", "model", model)
		}
	}

	if wake.scorer == nil {
		return nil, fmt.Errorf("no wake word model could be loaded from %s", dir)
	}

	log.Info("the local wake word is ready", "model", wake.model, "phrase", wake.scorer.Phrase())
	return wake, nil
}

// defaultWakeThreshold is the score the wake word has to reach.
//
// It is the openWakeWord default, and it is deliberately not lowered:
// a wake word that fires on the television is worse than one that misses
// a quiet person, because a panel that talks to itself is a panel nobody
// trusts.
const defaultWakeThreshold = 0.5

// findModels lists the wake word models in a directory.
//
// The melspectrogram and embedding models are the shared parts of the
// pipeline and are not wake words themselves, so they are not offered.
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
			// The shared parts of the pipeline, not wake words.
			continue
		}
		models = append(models, stem)
	}

	if len(models) == 0 {
		return nil, fmt.Errorf("no wake word model was found in %s", dir)
	}
	return models, nil
}

// Push gives the wake word a block of captured audio.
//
// The gate measures it and only lets it through when the room is worth
// listening to, so a silent room costs almost nothing.
func (w *wakeWord) Push(pcm []byte, at time.Time) {
	if w == nil || w.scorer == nil {
		return
	}
	if !w.gate.Allow(pcm, at) {
		return
	}
	w.scorer.PushBytesAt(pcm, at)
}

// Reset forgets what the wake word has heard.
//
// The detector's score is computed from the last two seconds of audio, so
// the audio of the turn that just finished is still in it. Without a
// reset the reply is scored as part of the next wake word, and the reply
// is loud — it is the panel talking — so the panel can hear itself.
//
// It is called when the microphone is opened again, because a gap in the
// audio is a gap in what the detector saw.
func (w *wakeWord) Reset() {
	if w == nil {
		return
	}
	w.gate.Reset()
	if w.scorer != nil {
		w.scorer.Reset()
	}
}

// crossed is called when the scorer sees the wake word.
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

// SetEnabled switches the wake word on or off.
//
// Home Assistant's picker does this, and so does the panel's own
// settings. Home Assistant wins when it has written, because it is the
// one that shows the picker.
func (w *wakeWord) SetEnabled(enabled bool) {
	if w == nil {
		return
	}
	w.mu.Lock()
	w.enabled = enabled
	w.mu.Unlock()
}

// SetThreshold sets the score the wake word has to reach.
// Threshold is the score the wake word has to reach.
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

// SetBargeThreshold sets the score the wake word has to reach while the
// speaker is playing.
//
// The echo of the panel's own voice is about twenty-five decibels louder
// at the microphone than the person talking over it, so a person who
// interrupts is heard far more quietly than they would be in a quiet
// room. The bar is dropped while the panel is speaking so that a person
// can interrupt it.
//
// It is dropped and not removed: the bar still has to be cleared twice
// in a row, so a click or a consonant in passing speech does not cut
// the reply off.
func (w *wakeWord) SetBargeThreshold(threshold float64, active func() bool) {
	if w == nil || w.scorer == nil {
		return
	}
	w.scorer.SetBargeThreshold(float32(threshold), active)
}

// Enabled says whether the wake word is switched on.
func (w *wakeWord) Enabled() bool {
	if w == nil {
		return false
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.enabled
}

// Ready says whether the wake word can run.
func (w *wakeWord) Ready() bool {
	return w != nil && w.scorer != nil
}

// Phrase returns the name of the wake word, as a person says it.
func (w *wakeWord) Phrase() string {
	if w == nil || w.scorer == nil {
		return ""
	}
	return w.scorer.Phrase()
}

// Model returns the name of the model that is loaded.
func (w *wakeWord) Model() string {
	if w == nil {
		return ""
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.model
}

// Amplitude returns the level of the last frame, as a percentage.
//
// It is the level meter, and it is the gate's measurement rather than
// the scorer's: the meter should move when somebody talks, and the gate
// is what notices that.
func (w *wakeWord) Amplitude() int {
	if w == nil {
		return 0
	}
	return w.gate.Amplitude()
}

// Close stops the wake word.
func (w *wakeWord) Close() {
	if w == nil || w.scorer == nil {
		return
	}
	w.scorer.Close()
}
