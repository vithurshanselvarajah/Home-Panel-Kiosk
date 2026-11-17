package scorer

import (
	"github.com/vithurshanselvarajah/Home-Panel-Kiosk/device/wakeword"
)

// owwEngine is an openWakeWord model: the mel and embedding chain, and a
// small classifier that reads a window of embeddings.
//
// The chain is shared between the models, so a second wake word costs a
// classifier and not a second front end.
type owwEngine struct {
	det *wakeword.Detector
}

func newOWWEngine(inf wakeword.Inferer) *owwEngine {
	return &owwEngine{det: wakeword.New(inf)}
}

// Push feeds audio and reports the score, and whether the model has scored
// yet.
func (e *owwEngine) Push(samples []int16) (float32, bool, error) {
	if _, err := e.det.Push(samples); err != nil {
		return 0, false, err
	}
	if !e.det.Ready() {
		return 0, false, nil
	}
	score, err := e.det.Score()
	if err != nil {
		return 0, false, err
	}
	return score, true, nil
}

// Reset forgets what has been heard, as if the model were new.
func (e *owwEngine) Reset() { e.det.Reset() }
