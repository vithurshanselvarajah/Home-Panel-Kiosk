package scorer

import (
	"github.com/vithurshanselvarajah/Home-Panel-Kiosk/device/wakeword"
)

type owwEngine struct {
	det *wakeword.Detector
}

func newOWWEngine(inf wakeword.Inferer) *owwEngine {
	return &owwEngine{det: wakeword.New(inf)}
}

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

func (e *owwEngine) Reset() { e.det.Reset() }
