package main

import (
	"github.com/vithurshanselvarajah/Home-Panel-Kiosk/device/esphome"
)

func panelEntities(capabilities PanelCapabilities) *esphome.Registry {
	registry := esphome.NewRegistry()

	if capabilities.CanControlScreen {
		registry.Add(&esphome.Entity{
			Kind:     esphome.KindSwitch,
			Name:     "Screen",
			ObjectID: "screen",
			Icon:     "mdi:monitor",
			State:    esphome.EntityValue{Kind: esphome.KindSwitch, Bool: true},
		})
	}
	if capabilities.HasBacklight {
		registry.Add(&esphome.Entity{
			Kind:     esphome.KindNumber,
			Name:     "Brightness",
			ObjectID: "brightness",
			Icon:     "mdi:brightness-6",

			Min:         1,
			Max:         100,
			Step:        1,
			DeviceClass: "brightness",
			State:       esphome.EntityValue{Kind: esphome.KindNumber, Number: 60},
		})
	}
	if capabilities.HasSlideshow {
		registry.Add(&esphome.Entity{
			Kind:     esphome.KindSelect,
			Name:     "View",
			ObjectID: "view",
			Icon:     "mdi:monitor-dashboard",
			Options:  []string{"dashboard", "slideshow"},
			State:    esphome.EntityValue{Kind: esphome.KindSelect, Text: "dashboard"},
		})
	}
	registry.Add(&esphome.Entity{
		Kind: esphome.KindNumber, Name: "Display · Idle to slideshow", ObjectID: "idle_slideshow_minutes",
		Icon: "mdi:timer-sand", Min: 0, Max: 1440, Step: 1,
		Category: esphome.EntityCategory_ENTITY_CATEGORY_CONFIG,
		State:    esphome.EntityValue{Kind: esphome.KindNumber, Number: 5},
	})
	registry.Add(&esphome.Entity{
		Kind: esphome.KindNumber, Name: "Display · Slideshow before screen off", ObjectID: "slideshow_off_minutes",
		Icon: "mdi:monitor-off", Min: 0, Max: 1440, Step: 1,
		Category: esphome.EntityCategory_ENTITY_CATEGORY_CONFIG,
		State:    esphome.EntityValue{Kind: esphome.KindNumber, Number: 15},
	})
	registry.Add(&esphome.Entity{
		Kind: esphome.KindSwitch, Name: "Display · Touch keyboard", ObjectID: "touch_keyboard_enabled",
		Icon: "mdi:keyboard", Category: esphome.EntityCategory_ENTITY_CATEGORY_CONFIG,
		State: esphome.EntityValue{Kind: esphome.KindSwitch, Bool: true},
	})
	registry.Add(&esphome.Entity{
		Kind:     esphome.KindButton,
		Name:     "Return to Home",
		ObjectID: "dashboard",
		Icon:     "mdi:home",
	})
	registry.Add(&esphome.Entity{
		Kind:     esphome.KindSelect,
		Name:     "Display mode",
		ObjectID: "display_mode",
		Icon:     "mdi:monitor",
		Category: esphome.EntityCategory_ENTITY_CATEGORY_CONFIG,
		Options:  []string{"fullscreen", "window"},
		State:    esphome.EntityValue{Kind: esphome.KindSelect, Text: "fullscreen"},
	})
	registry.Add(&esphome.Entity{
		Kind:     esphome.KindButton,
		Name:     "Exit",
		ObjectID: "exit",
		Icon:     "mdi:power",
	})

	registry.Add(&esphome.Entity{
		Kind:        esphome.KindBinarySensor,
		Name:        "Drawer open",
		ObjectID:    "drawer_open",
		Icon:        "mdi:dock-left",
		DeviceClass: "opening",
		State:       esphome.EntityValue{Kind: esphome.KindBinarySensor, Bool: false},
	})
	registry.Add(&esphome.Entity{
		Kind:        esphome.KindBinarySensor,
		Name:        "Keyboard visible",
		ObjectID:    "keyboard_visible",
		Icon:        "mdi:keyboard",
		DeviceClass: "opening",
		State:       esphome.EntityValue{Kind: esphome.KindBinarySensor, Bool: false},
	})

	registry.Add(&esphome.Entity{
		Kind:        esphome.KindTextSensor,
		Name:        "Last activity",
		ObjectID:    "last_activity",
		Icon:        "mdi:gesture-tap",
		DeviceClass: "timestamp",
		State:       esphome.EntityValue{Kind: esphome.KindTextSensor, Text: ""},
	})

	registry.Add(&esphome.Entity{
		Kind:     esphome.KindTextSensor,
		Name:     "Version",
		ObjectID: "version",
		Icon:     "mdi:information-outline",
		State:    esphome.EntityValue{Kind: esphome.KindTextSensor, Text: ""},
	})
	return registry
}

type PanelCapabilities struct {
	CanControlScreen bool

	HasBacklight bool

	HasSlideshow bool
}
