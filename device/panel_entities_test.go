package main

import "testing"

// TestPanelEntitiesHaveNoDuplicates checks that no two entities say the
// same thing.
//
// A control that says the same thing as another one is not offered:
// the screen switch turns the screen off and on, so there is no separate
// button for waking it or for turning it off, and the view select
// chooses between the dashboard and the slideshow, so there is no
// separate button for the slideshow.
//
// What is left is one control for each thing the panel can do.
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

	// The controls that are wanted are there.
	for _, want := range []string{
		"screen", "brightness", "volume", "view", "dashboard", "display_mode", "exit",
		"drawer_open", "keyboard_visible", "last_activity", "version",
	} {
		if !seen[want] {
			t.Errorf("the entity %q is missing", want)
		}
	}

	// The ones that say the same thing as another are not.
	for _, unwanted := range []string{"wake", "screen_off", "slideshow", "notification", "open_url"} {
		if seen[unwanted] {
			t.Errorf("the entity %q says the same thing as another one", unwanted)
		}
	}
}

// TestPanelServicesAreOffered checks that the commands are services.
//
// A notification and an address are commands with something in them,
// not values that sit there after they have been typed, so they are
// services rather than text boxes.
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

// TestNotificationServiceTakesItsArguments checks that the notification
// service carries everything a notification needs.
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

// TestOpenURLServiceTakesItsArguments checks that the address service
// carries the address.
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
