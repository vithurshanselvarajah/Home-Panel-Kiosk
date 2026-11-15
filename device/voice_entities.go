package main

import (
	"github.com/vithurshanselvarajah/Home-Panel-Kiosk/device/esphome"
)

// The voice settings, as Home Assistant sees them.
//
// They used to live in the device's own configuration file, which meant
// Home Assistant could not read them and could not change them: the
// person had to edit a file on the machine and restart the device.
//
// As entities they are read and written the way everything else is. The
// wake word's threshold is a number, the ducking is a number, the wake
// word on and off is a switch.
//
// The settings that are not here are the ones that are not settings:
// the microphone's gain and the voice activity detection's levels are
// the device's own business, and the music player's name is a name
// rather than a setting.
//
// Two things that used to be here are not.
//
// The startup volume is gone: the speaker keeps the level it was last
// at, so there is nothing to start up with. A panel that has never had
// a level starts at fifty.
//
// The mute is gone: it is the hardware latch, and the latch is one-way
// from software — the button on the unit sets it and only the button
// releases it. A switch that cannot be turned back on is not a control.
//
// The voice settings are added to the panel's entities rather than
// replacing them, because they are about the same device and Home
// Assistant shows them together.
func voiceEntities(registry *esphome.Registry) {
	registry.Add(&esphome.Entity{
		Kind: esphome.KindSwitch, Name: "Music · Music Assistant", ObjectID: "music_enabled",
		Icon: "mdi:music", Category: esphome.EntityCategory_ENTITY_CATEGORY_CONFIG,
		State: esphome.EntityValue{Kind: esphome.KindSwitch, Bool: true},
	})
	registry.Add(&esphome.Entity{
		Kind: esphome.KindSwitch, Name: "Music · Allow unpaired servers", ObjectID: "music_unpaired",
		Icon: "mdi:lan-disconnect", Category: esphome.EntityCategory_ENTITY_CATEGORY_CONFIG,
		State: esphome.EntityValue{Kind: esphome.KindSwitch, Bool: false},
	})
	// The wake word's threshold.
	//
	// It is the score the wake word has to reach to fire, as a
	// percentage. A panel in a noisy room wants a higher one.
	registry.Add(&esphome.Entity{
		Kind:     esphome.KindNumber,
		Name:     "Voice · Wake word sensitivity",
		ObjectID: "wake_word_threshold",
		Icon:     "mdi:ear-hearing",
		Category: esphome.EntityCategory_ENTITY_CATEGORY_CONFIG,
		Min:      0,
		Max:      100,
		Step:     1,
		Unit:     "%",
		State:    esphome.EntityValue{Kind: esphome.KindNumber, Number: 50},
	})

	// The wake word on and off.
	registry.Add(&esphome.Entity{
		Kind:     esphome.KindSwitch,
		Name:     "Voice · Wake word",
		ObjectID: "wake_word_enabled",
		Icon:     "mdi:message-voice",
		Category: esphome.EntityCategory_ENTITY_CATEGORY_CONFIG,
		State:    esphome.EntityValue{Kind: esphome.KindSwitch, Bool: true},
	})

	// How far the music is turned down under a reply.
	//
	// It is in decibels and it is negative, so the music is quieter
	// than it would be rather than louder. Zero leaves the music alone.
	registry.Add(&esphome.Entity{
		Kind:     esphome.KindNumber,
		Name:     "Voice · Music ducking",
		ObjectID: "music_ducking_db",
		Icon:     "mdi:volume-low",
		Category: esphome.EntityCategory_ENTITY_CATEGORY_CONFIG,
		Min:      -60,
		Max:      0,
		Step:     1,
		Unit:     "dB",
		State:    esphome.EntityValue{Kind: esphome.KindNumber, Number: -6},
	})
}
