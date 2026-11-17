package main

import (
	"testing"
	"time"

	"github.com/vithurshanselvarajah/Home-Panel-Kiosk/audio/speaker"
)

func TestTheMusicSourceAnswersThePull(t *testing.T) {
	m := &musicSource{}
	out := make([]int16, speaker.PeriodFrames()*speaker.Channels)

	done := make(chan struct{})
	go func() {
		defer close(done)
		m.Render(0, out)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the music source did not answer the speaker's pull")
	}
}
