package main

import (
	"testing"

	"github.com/vithurshanselvarajah/Home-Panel-Kiosk/device/esphome"
)

func TestTheVoiceSettingsAreEntities(t *testing.T) {
	registry := esphome.NewRegistry()
	voiceEntities(registry)

	byID := map[string]*esphome.Entity{}
	for _, entity := range registry.All() {
		byID[entity.ObjectID] = entity
	}

	for _, want := range []string{"wake_word_threshold", "wake_word_enabled", "music_ducking_db", "music_enabled", "music_unpaired", "speaker_louder_speech"} {
		if byID[want] == nil {
			t.Errorf("the %s setting is not an entity", want)
		}
	}
	for _, id := range []string{"wake_word_threshold", "wake_word_enabled", "music_ducking_db", "music_enabled", "music_unpaired", "speaker_louder_speech"} {
		entity := byID[id]
		if entity != nil && entity.Category != esphome.EntityCategory_ENTITY_CATEGORY_CONFIG {
			t.Errorf("entity %q has category %v, want configuration", id, entity.Category)
		}
	}

	if byID["startup_volume"] != nil {
		t.Error("the startup volume is offered, the speaker keeps the level it was last at")
	}

	if byID["muted"] != nil {
		t.Error("the mute is offered, the latch can only be released by the button on the unit")
	}

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

	ducking := byID["music_ducking_db"]
	if ducking != nil {
		if ducking.Kind != esphome.KindNumber {
			t.Errorf("the ducking is a %v, it should be a number", ducking.Kind)
		}
		if ducking.Max > 0 {
			t.Errorf("the ducking goes up to %v, it should never be louder", ducking.Max)
		}
	}

	if entity := byID["wake_word_enabled"]; entity != nil && entity.Kind != esphome.KindSwitch {
		t.Errorf("the wake word is a %v, it should be a switch", entity.Kind)
	}
}
