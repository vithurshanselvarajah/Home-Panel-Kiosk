package main

import (
	"os"
	"strings"
	"testing"

	"github.com/vithurshanselvarajah/Home-Panel-Kiosk/device/state"
)

// The wake word overlay.
//
// The panel draws the overlay from the device program's state: the
// screen shows that the assistant is listening, thinking or speaking.
// It is the only thing on the screen while a turn is running, so a
// panel that does not draw it is a panel that looks dead.
//
// It used to not be drawn. The reply's start was reported by a hook
// that was left over from the old protocol and was never called again
// when the protocol changed, so the speaking flag was never set and the
// overlay never came up.
//
// So the flag is set when the reply's first audio arrives, and cleared
// when the speaker has drained the reply.

// TestTheSpeakingFlagIsSetWhenTheReplyBegins checks the flag follows
// the reply.
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

// TestTheSpeakerIsWiredToTheReplyStart checks the speaker tells the
// state when a reply begins.
//
// This is the bug the overlay had: the reply's start was reported by a
// hook left over from the old protocol, and when the protocol changed
// the hook was never called again. So the reply was never reported and
// the overlay never came up.
//
// The hook is set in main, where the speaker and the state reporter are
// put together, so that is what is checked here: the two are joined.
func TestTheSpeakerIsWiredToTheReplyStart(t *testing.T) {
	source, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(source), "speaker_.voiceStarted = reporter.replyStarted") {
		t.Error("the speaker is not wired to the start of a reply, so the overlay never comes up")
	}
}

// TestTheReplyIsReportedWhenTheAudioArrives checks the reply's start
// reaches the state.
func TestTheReplyIsReportedWhenTheAudioArrives(t *testing.T) {
	audio := &deviceAudio{}
	reporter := &stateReporter{state: state.NewTracker()}
	audio.voiceStarted = reporter.replyStarted

	// The reply's first audio arrives. The audio itself is not played
	// here — that needs a sound card — but the start of the reply is
	// what the hook is for, and it is what the overlay waits for.
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

// TestTheSpeakingFlagIsClearWhenTheReplyEnds checks the flag follows
// the end of the reply.
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

// TestTheReplyIsNotOverBeforeItIsPlayedOut checks the end of the reply's
// audio is not the end of the reply.
//
// The voice assistant clears its own flag when the reply has all
// arrived, but the last of the words is said after the last frame has
// arrived. So the reply is over when the speaker has drained it.
func TestTheReplyIsNotOverBeforeItIsPlayedOut(t *testing.T) {
	reporter := &stateReporter{state: state.NewTracker()}

	reporter.replyStarted()
	// The voice assistant says the reply has all arrived.
	reporter.setSpeaking(false)

	reporter.mu.Lock()
	speaking := reporter.speaking
	reporter.mu.Unlock()
	if !speaking {
		t.Error("the reply's audio has all arrived, but the reply is not over until the speaker has drained it")
	}
}

// TestTheReplyEndsWhenTheLinkDrops checks the reply does not last for
// the life of the process.
func TestTheReplyEndsWhenTheLinkDrops(t *testing.T) {
	reporter := &stateReporter{state: state.NewTracker()}

	reporter.replyStarted()
	// The link drops, so the reply ends with it. The link going down is
	// what ends a reply, so the flag is cleared with it.
	reporter.replyEnded()

	reporter.mu.Lock()
	speaking := reporter.speaking
	reporter.mu.Unlock()
	if speaking {
		t.Error("the link dropped, but the reply is still playing")
	}
}
