package main

import (
	"github.com/vithurshanselvarajah/Home-Panel-Kiosk/device/esphome"
)

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
