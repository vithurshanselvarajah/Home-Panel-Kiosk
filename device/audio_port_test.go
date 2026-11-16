package main

import (
	"testing"
	"time"

	"github.com/vithurshanselvarajah/Home-Panel-Kiosk/audio/speaker"
)

// The music source must answer the speaker's pull.
//
// The speaker's write loop pulls from the Source on its own goroutine, and
// the Source asks the placement for the audio that goes with the frames. If
// the Source waits for anything the write loop holds, the loop stops for
// good: the speaker writes silence and the music never plays.
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
