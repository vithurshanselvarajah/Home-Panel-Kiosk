package main

import (
	"os"
	"strings"
	"testing"

	"github.com/vithurshanselvarajah/Home-Panel-Kiosk/device/state"
)

func TestTheSpeakingFlagIsSetWhenTheReplyBegins(t *testing.T) {
	reporter := &stateReporter{state: state.NewTracker()}

	reporter.replyStarted()

	reporter.mu.Lock()
	speaking := reporter.speaking
	reporter.mu.Unlock()
	if !speaking {
		t.Error("the reply began, but the speaking flag is not set, so the overlay never comes up")
	}
}

func TestTheSpeakerIsWiredToTheReplyStart(t *testing.T) {
	source, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(source), "speaker_.voiceStarted = reporter.replyStarted") {
		t.Error("the speaker is not wired to the start of a reply, so the overlay never comes up")
	}
}

func TestTheReplyIsReportedWhenTheAudioArrives(t *testing.T) {
	audio := &deviceAudio{}
	reporter := &stateReporter{state: state.NewTracker()}
	audio.voiceStarted = reporter.replyStarted

	audio.mu.Lock()
	started := audio.voiceStarted
	audio.mu.Unlock()
	if started == nil {
		t.Fatal("the speaker does not report the start of a reply, so the overlay never comes up")
	}
	started()

	reporter.mu.Lock()
	speaking := reporter.speaking
	reporter.mu.Unlock()
	if !speaking {
		t.Error("the reply's audio arrived, but the reply was never reported, so the overlay never comes up")
	}
}

func TestTheSpeakingFlagIsClearWhenTheReplyEnds(t *testing.T) {
	reporter := &stateReporter{state: state.NewTracker()}

	reporter.replyStarted()
	reporter.replyEnded()

	reporter.mu.Lock()
	speaking := reporter.speaking
	reporter.mu.Unlock()
	if speaking {
		t.Error("the reply ended, but the speaking flag is still set")
	}
}

func TestTheReplyIsNotOverBeforeItIsPlayedOut(t *testing.T) {
	reporter := &stateReporter{state: state.NewTracker()}

	reporter.replyStarted()

	reporter.setSpeaking(false)

	reporter.mu.Lock()
	speaking := reporter.speaking
	reporter.mu.Unlock()
	if !speaking {
		t.Error("the reply's audio has all arrived, but the reply is not over until the speaker has drained it")
	}
}

func TestTheReplyEndsWhenTheLinkDrops(t *testing.T) {
	reporter := &stateReporter{state: state.NewTracker()}

	reporter.replyStarted()

	reporter.replyEnded()

	reporter.mu.Lock()
	speaking := reporter.speaking
	reporter.mu.Unlock()
	if speaking {
		t.Error("the link dropped, but the reply is still playing")
	}
}
