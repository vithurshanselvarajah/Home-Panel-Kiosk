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
// word on and off is a switch, and the panel's mute is a switch.
//
// The settings that are not here are the ones that are not settings:
// the microphone's gain and the voice activity detection's levels are
// the device's own business, and the music player's name is a name
// rather than a setting.
//
// The voice settings are added to the panel's entities rather than
// replacing them, because they are about the same device and Home
// Assistant shows them together.
func voiceEntities(registry *esphome.Registry) {
	// The wake word's threshold.
	//
	// It is the score the wake word has to reach to fire, as a
	// percentage. A panel in a noisy room wants a higher one.
	registry.Add(&esphome.Entity{
		Kind:     esphome.KindNumber,
		Name:     "Wake word sensitivity",
		ObjectID: "wake_word_threshold",
		Icon:     "mdi:ear-hearing",
		Min:      0,
		Max:      100,
		Step:     1,
		Unit:     "%",
		State:    esphome.EntityValue{Kind: esphome.KindNumber, Number: 50},
	})

	// The wake word on and off.
	registry.Add(&esphome.Entity{
		Kind:     esphome.KindSwitch,
		Name:     "Wake word",
		ObjectID: "wake_word_enabled",
		Icon:     "mdi:message-voice",
		State:    esphome.EntityValue{Kind: esphome.KindSwitch, Bool: true},
	})

	// How far the music is turned down under a reply.
	//
	// It is in decibels and it is negative, so the music is quieter
	// than it would be rather than louder. Zero leaves the music alone.
	registry.Add(&esphome.Entity{
		Kind:     esphome.KindNumber,
		Name:     "Music ducking",
		ObjectID: "music_ducking_db",
		Icon:     "mdi:volume-low",
		Min:      -60,
		Max:      0,
		Step:     1,
		Unit:     "dB",
		State:    esphome.EntityValue{Kind: esphome.KindNumber, Number: -6},
	})

	// The panel's own mute switch.
	registry.Add(&esphome.Entity{
		Kind:     esphome.KindSwitch,
		Name:     "Mute",
		ObjectID: "muted",
		Icon:     "mdi:volume-off",
		State:    esphome.EntityValue{Kind: esphome.KindSwitch, Bool: false},
	})
}
