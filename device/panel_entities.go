package main

import (
	"github.com/vithurshanselvarajah/Home-Panel-Kiosk/device/esphome"
)

// The panel's entities.
//
// These are what Home Assistant sees when it looks at the kiosk. Each
// one is the kind of thing it actually is, because the protocol has no
// way to describe anything else: a switch for the screen, a number for
// the brightness, a select for the view, and so on.
//
// The panel used to publish twelve controls to a broker. Some of them
// said the same thing twice, and some of them were a text box where a
// command belongs, so the set is smaller now.
//
// A control that says the same thing as another is not offered. The
// screen switch turns the screen off and on, so there is no separate
// button for waking it and no separate button for turning it off. The
// view select chooses between the dashboard and the slideshow, so there
// is no separate button for the slideshow.
//
// A notification and an address are not controls. They are commands
// with an argument, and they are offered as services rather than as
// text boxes: a text box is a value that sits there after it has been
// typed, and a command is not a value.

// panelEntities makes the entities the panel offers.
//
// The screen and the brightness and the view are only offered when the
// machine can do them: a panel with no backlight has no brightness
// control, and one with no slideshow has no view to choose. A control
// that cannot do anything is not offered at all, because a switch that
// does nothing is worse than no switch.
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
			Kind:        esphome.KindNumber,
			Name:        "Brightness",
			ObjectID:    "brightness",
			Icon:        "mdi:brightness-6",
			Min:         0,
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
	// The volume of the room.
	//
	// It is a number because it is a measurement that is set, and the
	// panel's own drawer sets it too.
	registry.Add(&esphome.Entity{
		Kind:        esphome.KindNumber,
		Name:        "Volume",
		ObjectID:    "volume",
		Icon:        "mdi:volume-high",
		Min:         0,
		Max:         100,
		Step:        1,
		DeviceClass: "volume",
		State:       esphome.EntityValue{Kind: esphome.KindNumber, Number: 60},
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
		Options:  []string{"fullscreen", "window"},
		State:    esphome.EntityValue{Kind: esphome.KindSelect, Text: "fullscreen"},
	})
	registry.Add(&esphome.Entity{
		Kind:     esphome.KindButton,
		Name:     "Exit",
		ObjectID: "exit",
		Icon:     "mdi:power",
	})
	// What the panel is doing, as sensors.
	//
	// The controls already carry the mode, the display mode, the
	// brightness and the volume, so there is no sensor for those. What
	// is left is what can be seen but not set from Home Assistant: the
	// drawer being open, the keyboard being up, and the screen being
	// off.
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
		DeviceClass: "problem",
		State:       esphome.EntityValue{Kind: esphome.KindBinarySensor, Bool: false},
	})
	// The last time somebody did something on the panel.
	//
	// It is a text sensor because a moment in time is not a number,
	// and the protocol's sensor carries a number and nothing else.
	registry.Add(&esphome.Entity{
		Kind:        esphome.KindTextSensor,
		Name:        "Last activity",
		ObjectID:    "last_activity",
		Icon:        "mdi:gesture-tap",
		DeviceClass: "timestamp",
		State:       esphome.EntityValue{Kind: esphome.KindTextSensor, Text: ""},
	})
	// The version of the application.
	registry.Add(&esphome.Entity{
		Kind:     esphome.KindTextSensor,
		Name:     "Version",
		ObjectID: "version",
		Icon:     "mdi:information-outline",
		State:    esphome.EntityValue{Kind: esphome.KindTextSensor, Text: ""},
	})
	return registry
}

// PanelCapabilities are the things the machine can do.
//
// They are worked out on the machine rather than configured, because a
// control that cannot work is not offered.
type PanelCapabilities struct {
	// CanControlScreen says whether the screen can be turned off.
	CanControlScreen bool
	// HasBacklight says whether there is a brightness to set.
	HasBacklight bool
	// HasSlideshow says whether there is a slideshow to show.
	HasSlideshow bool
}
