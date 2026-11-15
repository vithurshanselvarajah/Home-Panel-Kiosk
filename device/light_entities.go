package main

import (
	"github.com/vithurshanselvarajah/Home-Panel-Kiosk/device/esphome"
)

// The light sensor and the automatic brightness.
//
// The sensor is the room's light, in lux. It is read, not written: it
// is what the room is, and nothing anyone does changes it.
//
// The switch is whether the screen follows the room. With it on, the
// brightness setting is a ceiling and the room's light decides how much
// of that ceiling the screen gets. With it off, the brightness setting
// is the screen's brightness.
//
// The board may have no light sensor at all — the sensor is on some
// units and not on others — so the entities are only offered when there
// is one to read.
func lightEntities(registry *esphome.Registry, hasSensor bool) {
	if !hasSensor {
		return
	}
	registry.Add(&esphome.Entity{
		Kind:     esphome.KindSensor,
		Name:     "Light",
		ObjectID: "light_lux",
		Icon:     "mdi:brightness-5",
		Unit:     "lx",
		// The device class is illuminance, so Home Assistant shows it
		// as a light reading and lets it be used in its own rules.
		DeviceClass: "illuminance",
		State:       esphome.EntityValue{Kind: esphome.KindSensor, Number: 0},
	})
	registry.Add(&esphome.Entity{
		Kind:     esphome.KindSwitch,
		Name:     "Screen · Automatic brightness",
		ObjectID: "auto_brightness",
		Icon:     "mdi:brightness-auto",
		Category: esphome.EntityCategory_ENTITY_CATEGORY_CONFIG,
		State:    esphome.EntityValue{Kind: esphome.KindSwitch, Bool: false},
	})
}
