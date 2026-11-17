package main

import (
	"github.com/vithurshanselvarajah/Home-Panel-Kiosk/device/esphome"
)

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

	registry.Add(&esphome.Entity{
		Kind:     esphome.KindSwitch,
		Name:     "Voice · Wake word",
		ObjectID: "wake_word_enabled",
		Icon:     "mdi:message-voice",
		Category: esphome.EntityCategory_ENTITY_CATEGORY_CONFIG,
		State:    esphome.EntityValue{Kind: esphome.KindSwitch, Bool: true},
	})

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

	registry.Add(&esphome.Entity{
		Kind:     esphome.KindSwitch,
		Name:     "Sound · Vendor tuning",
		ObjectID: "speaker_tuning",
		Icon:     "mdi:tune",
		Category: esphome.EntityCategory_ENTITY_CATEGORY_CONFIG,
		State:    esphome.EntityValue{Kind: esphome.KindSwitch, Bool: true},
	})
	registry.Add(&esphome.Entity{
		Kind:     esphome.KindNumber,
		Name:     "Sound · Amplifier gain",
		ObjectID: "speaker_gain_db",
		Icon:     "mdi:volume-high",
		Category: esphome.EntityCategory_ENTITY_CATEGORY_CONFIG,
		Min:      0,
		Max:      6,
		Step:     1,
		Unit:     "dB",
		State:    esphome.EntityValue{Kind: esphome.KindNumber, Number: 0},
	})
	registry.Add(&esphome.Entity{
		Kind:     esphome.KindSwitch,
		Name:     "Sound · Stereo",
		ObjectID: "speaker_stereo",
		Icon:     "mdi:speaker",
		Category: esphome.EntityCategory_ENTITY_CATEGORY_CONFIG,
		State:    esphome.EntityValue{Kind: esphome.KindSwitch, Bool: false},
	})
	registry.Add(&esphome.Entity{
		Kind:     esphome.KindNumber,
		Name:     "Sound · Bass",
		ObjectID: "speaker_bass_db",
		Icon:     "mdi:music-note",
		Category: esphome.EntityCategory_ENTITY_CATEGORY_CONFIG,
		Min:      -6,
		Max:      6,
		Step:     0.5,
		Unit:     "dB",
		State:    esphome.EntityValue{Kind: esphome.KindNumber, Number: 0},
	})
	registry.Add(&esphome.Entity{
		Kind:     esphome.KindNumber,
		Name:     "Sound · Treble",
		ObjectID: "speaker_treble_db",
		Icon:     "mdi:music-note",
		Category: esphome.EntityCategory_ENTITY_CATEGORY_CONFIG,
		Min:      -6,
		Max:      6,
		Step:     0.5,
		Unit:     "dB",
		State:    esphome.EntityValue{Kind: esphome.KindNumber, Number: 0},
	})
	registry.Add(&esphome.Entity{
		Kind:     esphome.KindSwitch,
		Name:     "Sound · Clearer speech",
		ObjectID: "speaker_clearer_speech",
		Icon:     "mdi:message-voice",
		Category: esphome.EntityCategory_ENTITY_CATEGORY_CONFIG,
		State:    esphome.EntityValue{Kind: esphome.KindSwitch, Bool: false},
	})
	registry.Add(&esphome.Entity{
		Kind:     esphome.KindSwitch,
		Name:     "Sound · Louder top",
		ObjectID: "speaker_louder_top",
		Icon:     "mdi:volume-up",
		Category: esphome.EntityCategory_ENTITY_CATEGORY_CONFIG,
		State:    esphome.EntityValue{Kind: esphome.KindSwitch, Bool: false},
	})
	registry.Add(&esphome.Entity{
		Kind:     esphome.KindSwitch,
		Name:     "Sound · Louder speech",
		ObjectID: "speaker_louder_speech",
		Icon:     "mdi:message-processing",
		Category: esphome.EntityCategory_ENTITY_CATEGORY_CONFIG,
		State:    esphome.EntityValue{Kind: esphome.KindSwitch, Bool: false},
	})

	registry.Add(&esphome.Entity{
		Kind:     esphome.KindSwitch,
		Name:     "Microphone · Echo cancellation",
		ObjectID: "mic_cancel",
		Icon:     "mdi:echo-off",
		Category: esphome.EntityCategory_ENTITY_CATEGORY_CONFIG,
		State:    esphome.EntityValue{Kind: esphome.KindSwitch, Bool: true},
	})
	registry.Add(&esphome.Entity{
		Kind:     esphome.KindSwitch,
		Name:     "Microphone · Automatic gain",
		ObjectID: "mic_leveling",
		Icon:     "mdi:autorenew",
		Category: esphome.EntityCategory_ENTITY_CATEGORY_CONFIG,
		State:    esphome.EntityValue{Kind: esphome.KindSwitch, Bool: true},
	})
	registry.Add(&esphome.Entity{
		Kind:     esphome.KindSwitch,
		Name:     "Microphone · Noise reduction",
		ObjectID: "mic_denoise",
		Icon:     "mdi:waveform",
		Category: esphome.EntityCategory_ENTITY_CATEGORY_CONFIG,
		State:    esphome.EntityValue{Kind: esphome.KindSwitch, Bool: false},
	})
	registry.Add(&esphome.Entity{
		Kind:     esphome.KindNumber,
		Name:     "Microphone · Sensitivity",
		ObjectID: "mic_sensitivity_db",
		Icon:     "mdi:ear-hearing",
		Category: esphome.EntityCategory_ENTITY_CATEGORY_CONFIG,
		Min:      0,
		Max:      30,
		Step:     1,
		Unit:     "dB",
		State:    esphome.EntityValue{Kind: esphome.KindNumber, Number: 8},
	})
}
