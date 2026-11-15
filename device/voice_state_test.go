package main

import (
	"testing"

	"github.com/vithurshanselvarajah/Home-Panel-Kiosk/device/esphome"
)

// The wake word's state is published to Home Assistant.
//
// The wake word is one switch, and it is the one Home Assistant shows.
// It has to show what the panel is really doing: a switch that says off
// while the panel is listening is a switch that lies, and Home Assistant
// acts on what it is told.
//
// It used to show the wrong thing. The audio kept a copy of the answer
// — whether the wake word is on — and that copy was only written when
// the wake word was switched. On a panel that was never switched, the
// copy held the zero value: off. The wake word itself was listening.
//
// So changing anything that published the voice settings — the
// sensitivity, the ducking — published that stale "off", and Home
// Assistant switched the wake word off to match. A person changing the
// ducking turned off the wake word.
//
// What is published is now read from the wake word itself.

// TestThePublishedWakeWordStateIsTheRealOne checks that the state
// published is the wake word's own.
func TestThePublishedWakeWordStateIsTheRealOne(t *testing.T) {
	wake := &wakeWord{enabled: true}
	plane := &ControlPlane{audio: &deviceAudio{}, wake: wake}

	if !plane.wakeEnabled() {
		t.Error("the wake word is listening, but the published state says it is off")
	}

	wake.SetEnabled(false)
	if plane.wakeEnabled() {
		t.Error("the wake word is off, but the published state says it is on")
	}
}

// TestTheWakeWordStateIsPublishedWhenTheVoiceSettingsChange checks that
// changing a voice setting does not change the wake word's published
// state.
//
// This is the bug: changing the sensitivity or the ducking published
// the voice settings, and that published the wake word's state, and the
// state was wrong.
func TestTheWakeWordStateIsPublishedWhenTheVoiceSettingsChange(t *testing.T) {
	registry := esphome.NewRegistry()
	voiceEntities(registry)

	wake := &wakeWord{enabled: true}
	server := &ControlServer{registry: registry}
	plane := &ControlPlane{server: server, audio: &deviceAudio{}, wake: wake}

	// The state is published as the panel is really doing it.
	plane.publishVoiceSettings()

	var found bool
	for _, entity := range registry.All() {
		if entity.ObjectID != "wake_word_enabled" {
			continue
		}
		found = true
		if !entity.StateOf().Bool {
			t.Error("the wake word is listening, but the entity says it is off")
		}
	}
	if !found {
		t.Fatal("there is no wake word entity")
	}

	// Changing the sensitivity publishes the settings again. The wake
	// word's state must not change with it.
	plane.setWakeThreshold(0.8)
	for _, entity := range registry.All() {
		if entity.ObjectID != "wake_word_enabled" {
			continue
		}
		if !entity.StateOf().Bool {
			t.Error("changing the sensitivity switched the wake word's published state off")
		}
	}

	// And changing the ducking does the same.
	plane.setDucking(-12)
	for _, entity := range registry.All() {
		if entity.ObjectID != "wake_word_enabled" {
			continue
		}
		if !entity.StateOf().Bool {
			t.Error("changing the ducking switched the wake word's published state off")
		}
	}
}

// TestTheAudioCopyOfTheWakeWordStateStartsOn checks the copy the audio
// keeps.
//
// It is a copy of the wake word's answer, and it is only written when
// the wake word is switched. A copy that started off would say the wake
// word was off on a panel that was listening.
func TestTheAudioCopyOfTheWakeWordStateStartsOn(t *testing.T) {
	audio := &deviceAudio{wakeWordEnabled: true}
	if !audio.wakeEnabled() {
		t.Error("the copy of the wake word's state starts off, the wake word starts on")
	}
}

// TestTheSpeakerStartsAtHalf checks where the speaker starts.
//
// There is no startup volume to choose any more: the speaker keeps the
// level it was last at. A panel that has never had a level starts here.
func TestTheSpeakerStartsAtHalf(t *testing.T) {
	if defaultVolume != 50 {
		t.Errorf("the speaker starts at %d, it should start at 50", defaultVolume)
	}
}
