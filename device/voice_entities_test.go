package main

import (
	"testing"

	"github.com/vithurshanselvarajah/Home-Panel-Kiosk/device/esphome"
)

// TestTheVoiceSettingsAreEntities checks that the voice settings are
// things Home Assistant can read and change.
//
// They used to live in the device's own configuration file, which meant
// Home Assistant could neither read them nor change them: the person
// had to edit a file on the machine and restart the device.
func TestTheVoiceSettingsAreEntities(t *testing.T) {
	registry := esphome.NewRegistry()
	voiceEntities(registry)

	byID := map[string]*esphome.Entity{}
	for _, entity := range registry.All() {
		byID[entity.ObjectID] = entity
	}

	for _, want := range []string{"wake_word_threshold", "wake_word_enabled", "music_ducking_db", "muted"} {
		if byID[want] == nil {
			t.Errorf("the %s setting is not an entity", want)
		}
	}

	// The sensitivity is a number between zero and a hundred, because
	// that is how a person reads a percentage.
	threshold := byID["wake_word_threshold"]
	if threshold != nil {
		if threshold.Kind != esphome.KindNumber {
			t.Errorf("the sensitivity is a %v, it should be a number", threshold.Kind)
		}
		if threshold.Min != 0 || threshold.Max != 100 {
			t.Errorf("the sensitivity runs from %v to %v, it should run from 0 to 100",
				threshold.Min, threshold.Max)
		}
	}

	// The ducking is a number of decibels, and it is negative: the music
	// is quieter under a reply, not louder.
	ducking := byID["music_ducking_db"]
	if ducking != nil {
		if ducking.Kind != esphome.KindNumber {
			t.Errorf("the ducking is a %v, it should be a number", ducking.Kind)
		}
		if ducking.Max > 0 {
			t.Errorf("the ducking goes up to %v, it should never be louder", ducking.Max)
		}
	}

	// The wake word and the mute are switches.
	for _, want := range []string{"wake_word_enabled", "muted"} {
		if entity := byID[want]; entity != nil && entity.Kind != esphome.KindSwitch {
			t.Errorf("the %s is a %v, it should be a switch", want, entity.Kind)
		}
	}
}
