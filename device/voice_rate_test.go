package main

import (
	"testing"

	"github.com/vithurshanselvarajah/Home-Panel-Kiosk/audio"
	"github.com/vithurshanselvarajah/Home-Panel-Kiosk/audio/speaker"
)

func TestTheReplyReachesTheSpeakerAtItsOwnRate(t *testing.T) {

	reply := make([]byte, voiceRate*2)

	converted := audio.NewResampler(voiceRate, voiceRate).Convert(reply)
	if got := len(converted) / 2; got != voiceRate {
		t.Fatalf("the reply is %d samples after conversion, want %d", got, voiceRate)
	}

	up := speaker.VoiceUpsample
	if up != 3 {
		t.Fatalf("the voice path interpolates by %d, want 3", up)
	}

	if got := float64(len(converted)/2) * float64(up) / float64(speaker.Rate); got != 1 {
		t.Fatalf("one second of speech becomes %.2f seconds of audio, want 1", got)
	}
}
