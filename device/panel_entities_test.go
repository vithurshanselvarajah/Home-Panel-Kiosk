package main

import (
	"testing"

	"github.com/vithurshanselvarajah/Home-Panel-Kiosk/device/esphome"
)

func TestPanelEntitiesHaveNoDuplicates(t *testing.T) {
	registry := panelEntities(PanelCapabilities{
		CanControlScreen: true,
		HasBacklight:     true,
		HasSlideshow:     true,
	})

	seen := map[string]bool{}
	for _, entity := range registry.All() {
		if seen[entity.ObjectID] {
			t.Errorf("the entity %q is offered twice", entity.ObjectID)
		}
		seen[entity.ObjectID] = true
	}

	for _, want := range []string{
		"screen", "brightness", "view", "dashboard", "display_mode", "exit",
		"idle_slideshow_minutes", "slideshow_off_minutes", "touch_keyboard_enabled",
		"drawer_open", "keyboard_visible", "last_activity", "version",
	} {
		if !seen[want] {
			t.Errorf("the entity %q is missing", want)
		}
	}
	for _, id := range []string{"display_mode", "idle_slideshow_minutes", "slideshow_off_minutes", "touch_keyboard_enabled"} {
		entity := entityByID(registry, id)
		if entity == nil {
			t.Errorf("configuration entity %q is missing", id)
			continue
		}
		if entity.Category != esphome.EntityCategory_ENTITY_CATEGORY_CONFIG {
			t.Errorf("entity %q has category %v, want configuration", id, entity.Category)
		}
	}

	for _, unwanted := range []string{"wake", "screen_off", "slideshow", "notification", "open_url", "volume"} {
		if seen[unwanted] {
			t.Errorf("the entity %q says the same thing as another one", unwanted)
		}
	}
}

func entityByID(registry *esphome.Registry, id string) *esphome.Entity {
	for _, entity := range registry.All() {
		if entity.ObjectID == id {
			return entity
		}
	}
	return nil
}

func TestTheBinarySensorsSayOpenAndClosed(t *testing.T) {
	registry := panelEntities(PanelCapabilities{
		CanControlScreen: true,
		HasBacklight:     true,
		HasSlideshow:     true,
	})
	for _, entity := range registry.All() {
		if entity.Kind != esphome.KindBinarySensor {
			continue
		}
		if entity.DeviceClass != "opening" {
			t.Errorf("the sensor %q has the device class %q, it should be \"opening\"",
				entity.ObjectID, entity.DeviceClass)
		}
	}
}

func TestPanelServicesAreOffered(t *testing.T) {
	services := panelServices(func(ControlCommand) {})

	names := map[string]bool{}
	for _, service := range services.All() {
		names[service.Name] = true
	}
	for _, want := range []string{"send_notification", "open_url"} {
		if !names[want] {
			t.Errorf("the service %q is missing", want)
		}
	}
	if len(services.All()) != 2 {
		t.Errorf("there are %d services, there should be 2", len(services.All()))
	}
}

func TestNotificationServiceTakesItsArguments(t *testing.T) {
	var got ControlCommand
	services := panelServices(func(command ControlCommand) { got = command })

	for _, service := range services.All() {
		if service.Name != "send_notification" {
			continue
		}
		err := service.Call(map[string]any{
			"message":  "Package at the door",
			"title":    "Delivery",
			"duration": 12000,
		})
		if err != nil {
			t.Fatalf("the service was refused: %v", err)
		}
	}
	if got.Message != "Package at the door" {
		t.Errorf("the message is %q", got.Message)
	}
	if got.Title != "Delivery" {
		t.Errorf("the title is %q", got.Title)
	}
	if got.Duration != 12000 {
		t.Errorf("the duration is %d", got.Duration)
	}
}

func TestOpenURLServiceTakesItsArguments(t *testing.T) {
	var got ControlCommand
	services := panelServices(func(command ControlCommand) { got = command })

	for _, service := range services.All() {
		if service.Name != "open_url" {
			continue
		}
		err := service.Call(map[string]any{
			"url": "http://192.168.1.20:8080/stream",
		})
		if err != nil {
			t.Fatalf("the service was refused: %v", err)
		}
	}
	if got.URL != "http://192.168.1.20:8080/stream" {
		t.Errorf("the address is %q", got.URL)
	}
}
