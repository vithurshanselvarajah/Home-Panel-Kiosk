package main

import (
	"testing"

	"github.com/vithurshanselvarajah/Home-Panel-Kiosk/audio"
	"github.com/vithurshanselvarajah/Home-Panel-Kiosk/audio/speaker"
)

// The reply must reach the speaker at the rate its voice path takes.
//
// The speaker's voice path takes 16 kHz mono and interpolates it up to the
// card's own rate itself. A reply that was converted up first was
// interpolated twice, and heard at three times the speed it was spoken at.
func TestTheReplyReachesTheSpeakerAtItsOwnRate(t *testing.T) {
	// A second of speech, as Home Assistant sends it.
	reply := make([]byte, voiceRate*2)

	// What the reply goes through: the queue keeps it at the rate it
	// arrived at.
	converted := audio.NewResampler(voiceRate, voiceRate).Convert(reply)
	if got := len(converted) / 2; got != voiceRate {
		t.Fatalf("the reply is %d samples after conversion, want %d", got, voiceRate)
	}

	// The speaker's voice path interpolates it up by its own factor.
	up := speaker.VoiceUpsample
	if up != 3 {
		t.Fatalf("the voice path interpolates by %d, want 3", up)
	}
	// So the card receives one second of audio for one second of speech.
	if got := float64(len(converted)/2) * float64(up) / float64(speaker.Rate); got != 1 {
		t.Fatalf("one second of speech becomes %.2f seconds of audio, want 1", got)
	}
}
