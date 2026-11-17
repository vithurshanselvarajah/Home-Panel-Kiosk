package main

import (
	"testing"

	"github.com/vithurshanselvarajah/Home-Panel-Kiosk/device/esphome"
)

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

func TestTheWakeWordStateIsPublishedWhenTheVoiceSettingsChange(t *testing.T) {
	registry := esphome.NewRegistry()
	voiceEntities(registry)

	wake := &wakeWord{enabled: true}
	server := &ControlServer{registry: registry}
	plane := &ControlPlane{server: server, audio: &deviceAudio{}, wake: wake}

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

	plane.setWakeThreshold(0.8)
	for _, entity := range registry.All() {
		if entity.ObjectID != "wake_word_enabled" {
			continue
		}
		if !entity.StateOf().Bool {
			t.Error("changing the sensitivity switched the wake word's published state off")
		}
	}

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

func TestTheAudioCopyOfTheWakeWordStateStartsOn(t *testing.T) {
	audio := &deviceAudio{wakeWordEnabled: true}
	if !audio.wakeEnabled() {
		t.Error("the copy of the wake word's state starts off, the wake word starts on")
	}
}

func TestTheSpeakerStartsAtHalf(t *testing.T) {
	if defaultVolume != 50 {
		t.Errorf("the speaker starts at %d, it should start at 50", defaultVolume)
	}
}

func TestTheLouderSpeechIsPublishedAsItIs(t *testing.T) {
	registry := esphome.NewRegistry()
	voiceEntities(registry)

	server := &ControlServer{registry: registry}
	plane := &ControlPlane{server: server, audio: &deviceAudio{}}

	plane.OnLouderSpeech(func(on bool) {
		plane.audio.setLouderSpeech(on)
	})
	plane.SetAudioSettings()

	for _, entity := range registry.All() {
		if entity.ObjectID != "speaker_louder_speech" {
			continue
		}
		if entity.StateOf().Bool {
			t.Error("the Louder speech switch says it is on, and it is off")
		}
	}

	plane.setLouderSpeech(true)
	if !louderSpeechOn.Load() {
		t.Error("the Louder speech switch was turned on, and the device did not change")
	}
	plane.SetAudioSettings()
	for _, entity := range registry.All() {
		if entity.ObjectID != "speaker_louder_speech" {
			continue
		}
		if !entity.StateOf().Bool {
			t.Error("the Louder speech switch is on, and the entity says it is off")
		}
	}

	plane.setLouderSpeech(false)
	plane.SetAudioSettings()
	for _, entity := range registry.All() {
		if entity.ObjectID != "speaker_louder_speech" {
			continue
		}
		if entity.StateOf().Bool {
			t.Error("the Louder speech switch is off, and the entity says it is on")
		}
	}
}
