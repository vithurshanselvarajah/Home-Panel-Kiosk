package esphome

import (
	"errors"
	"net"
	"strings"
	"testing"
)

// TestServerListsEntities checks that a client is told what the device
// offers.
//
// The peer is aioesphomeapi, the library Home Assistant uses, so the
// list is checked against the same code that will read it in anger.
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
	// The kinds have to be right too: a number that is sent as a switch
	// would be read as a switch and set to the wrong thing.
	for _, want := range []string{"SwitchInfo", "NumberInfo", "SelectInfo", "TextInfo", "ButtonInfo"} {
		if !strings.Contains(joined, want) {
			t.Errorf("the entity kind %q was not what was expected: %v", want, lines)
		}
	}
}

// TestServerPushesAndAppliesState checks the round trip.
//
// The client asks for the state, the device sends it, and then the
// client changes an entity and the device sees the change. The last
// part is the one that matters: it is how Home Assistant controls the
// panel.
func TestServerPushesAndAppliesState(t *testing.T) {
	registry := NewRegistry()
	screen := registry.Add(&Entity{Kind: KindSwitch, Name: "Screen", ObjectID: "screen"})
	registry.SetState(screen.Key, EntityValue{Kind: KindSwitch, Bool: true})

	// The command the client sends has to reach the entity.
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

	// The state was pushed.
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "state:SwitchState:True") {
		t.Errorf("the state was not pushed: %v", lines)
	}
	// The command reached the entity.
	if got.Kind != KindSwitch || !got.Bool {
		t.Errorf("the command did not reach the entity: %+v", got)
	}
}
