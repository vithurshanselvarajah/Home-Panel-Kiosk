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
// The MQTT version of this published the same controls as discovery
// messages with a Jinja template for the state and another one for the
// command. These are the same controls without the templates.

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
		registry.Add(&esphome.Entity{
			Kind:        esphome.KindButton,
			Name:        "Wake screen",
			ObjectID:    "wake",
			Icon:        "mdi:monitor",
			DeviceClass: "restart",
		})
		registry.Add(&esphome.Entity{
			Kind:     esphome.KindButton,
			Name:     "Turn screen off",
			ObjectID: "screen_off",
			Icon:     "mdi:monitor-off",
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
		registry.Add(&esphome.Entity{
			Kind:     esphome.KindButton,
			Name:     "Open slideshow",
			ObjectID: "slideshow",
			Icon:     "mdi:image-multiple",
		})
	}
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
		Kind:     esphome.KindText,
		Name:     "Send notification",
		ObjectID: "notification",
		Icon:     "mdi:message-text",
	})
	registry.Add(&esphome.Entity{
		Kind:     esphome.KindText,
		Name:     "Open display URL",
		ObjectID: "open_url",
		Icon:     "mdi:link-variant",
	})
	registry.Add(&esphome.Entity{
		Kind:     esphome.KindButton,
		Name:     "Exit",
		ObjectID: "exit",
		Icon:     "mdi:power",
	})
	// The state of the panel itself.
	//
	// It is a text sensor because it is read and not written: the panel
	// reports what it is doing and Home Assistant cannot tell it to do
	// something else through it.
	//
	// It is a text sensor rather than a number because the state is a
	// set of values rather than one measurement, and the protocol's
	// sensor carries a number and nothing else.
	registry.Add(&esphome.Entity{
		Kind:     esphome.KindTextSensor,
		Name:     "Panel state",
		ObjectID: controlStateObjectID,
		Icon:     "mdi:tablet-dashboard",
		State:    esphome.EntityValue{Kind: esphome.KindTextSensor, Text: "{}"},
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
