package esphome

import (
	"errors"
	"net"
	"strings"
	"testing"
)

func TestServerListsEntities(t *testing.T) {
	registry := NewRegistry()
	registry.Add(&Entity{Kind: KindSwitch, Name: "Screen", ObjectID: "screen", Icon: "mdi:monitor"})
	registry.Add(&Entity{Kind: KindNumber, Name: "Brightness", ObjectID: "brightness", Min: 0, Max: 100, Step: 1})
	registry.Add(&Entity{Kind: KindSelect, Name: "View", ObjectID: "view", Options: []string{"dashboard", "slideshow"}})
	registry.Add(&Entity{Kind: KindText, Name: "Notification", ObjectID: "notification"})
	registry.Add(&Entity{Kind: KindButton, Name: "Wake", ObjectID: "wake", DeviceClass: "restart"})

	server := NewServer(ServerOptions{
		Name:         "home-panel",
		FriendlyName: "Home Panel",
		MAC:          "aabbccddeeff",
		Version:      "0.0.2",
		Registry:     registry,
	})

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go server.Listen(listener)

	lines, err := runPeer("list_entities", listener.Addr().String(), "")
	if err != nil {
		if errors.Is(err, errSkipPeer) {
			t.Skip("the python peer is not available")
		}
		t.Fatalf("the client could not read the entities: %v", err)
	}

	var entities []string
	for _, line := range lines {
		if strings.HasPrefix(line, "entity:") {
			entities = append(entities, strings.TrimPrefix(line, "entity:"))
		}
	}
	if len(entities) != 5 {
		t.Fatalf("the client was told about %d entities, it should be 5: %v", len(entities), lines)
	}
	joined := strings.Join(entities, " ")
	for _, want := range []string{"Screen", "Brightness", "View", "Notification", "Wake"} {
		if !strings.Contains(joined, want) {
			t.Errorf("the entity %q was not listed: %v", want, lines)
		}
	}

	for _, want := range []string{"SwitchInfo", "NumberInfo", "SelectInfo", "TextInfo", "ButtonInfo"} {
		if !strings.Contains(joined, want) {
			t.Errorf("the entity kind %q was not what was expected: %v", want, lines)
		}
	}
}

func TestServerPushesAndAppliesState(t *testing.T) {
	registry := NewRegistry()
	screen := registry.Add(&Entity{Kind: KindSwitch, Name: "Screen", ObjectID: "screen"})
	registry.SetState(screen.Key, EntityValue{Kind: KindSwitch, Bool: true})

	var got EntityValue
	screen.OnCommand(func(value EntityValue) { got = value })

	server := NewServer(ServerOptions{
		Name: "home-panel", FriendlyName: "Home Panel", MAC: "aabbccddeeff",
		Version: "0.0.2", Registry: registry,
	})
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go server.Listen(listener)

	lines, err := runPeer("states", listener.Addr().String(), "")
	if err != nil {
		if errors.Is(err, errSkipPeer) {
			t.Skip("the python peer is not available")
		}
		t.Fatalf("the peer failed: %v", err)
	}

	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "state:SwitchState:True") {
		t.Errorf("the state was not pushed: %v", lines)
	}

	if got.Kind != KindSwitch || !got.Bool {
		t.Errorf("the command did not reach the entity: %+v", got)
	}
}

func TestProjectNameHasBothParts(t *testing.T) {
	server := NewServer(ServerOptions{
		Name: "home-panel", FriendlyName: "Home Panel", MAC: "aabbccddeeff",
		Version: "0.0.2", Registry: NewRegistry(),
	})
	info := server.deviceInfo()
	parts := strings.Split(info.ProjectName, ".")
	if len(parts) != 2 {
		t.Fatalf("the project name %q has %d parts, it should have 2", info.ProjectName, len(parts))
	}
	if parts[0] == "" || parts[1] == "" {
		t.Fatalf("the project name %q has an empty part", info.ProjectName)
	}
}

func TestCommandDoesNotSetTheState(t *testing.T) {
	registry := NewRegistry()
	entity := registry.Add(&Entity{
		Kind: KindSelect, Name: "Display mode", ObjectID: "display_mode",
		Options: []string{"fullscreen", "window"},
		State:   EntityValue{Kind: KindSelect, Text: "fullscreen"},
	})

	changed := 0
	registry.OnChange(func(*Entity) { changed++ })

	if err := registry.HandleCommand(entity.Key, EntityValue{Kind: KindSelect, Text: "window"}); err != nil {
		t.Fatalf("the command was refused: %v", err)
	}

	if got := entity.StateOf(); got.Text != "fullscreen" {
		t.Errorf("the state is %q, the command should not have changed it", got.Text)
	}
	if changed != 0 {
		t.Errorf("the change was published %d times, it should not have been published at all", changed)
	}

	registry.SetState(entity.Key, EntityValue{Kind: KindSelect, Text: "window"})
	if got := entity.StateOf(); got.Text != "window" {
		t.Errorf("the state is %q, it should be window", got.Text)
	}
	if changed != 1 {
		t.Errorf("the change was published %d times, it should have been published once", changed)
	}
}

func TestTheDevicePingsSoTheConnectionSurvives(t *testing.T) {
	server := NewServer(ServerOptions{
		Name: "home-panel", FriendlyName: "Home Panel", MAC: "aabbccddeeff",
		Version: "0.0.2", Registry: NewRegistry(),
	})
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go server.Listen(listener)

	lines, err := runPeer("ping", listener.Addr().String(), "")
	if err != nil {
		if errors.Is(err, errSkipPeer) {
			t.Skip("the python peer is not available")
		}
		t.Fatalf("the peer could not be run: %v", err)
	}
	for _, line := range lines {
		if line == "pinged" {
			return
		}
	}
	t.Fatalf("the device did not ping: %v", lines)
}
