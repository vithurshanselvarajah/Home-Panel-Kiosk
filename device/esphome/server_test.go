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

// TestProjectNameHasBothParts checks the shape of the project name.
//
// Home Assistant takes the project name apart to show the manufacturer
// and the model, and it expects two parts joined by a dot. A name with
// no dot in it is a name it cannot read, and the device is set up
// without any entities at all when it cannot read it.
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

// TestCommandDoesNotSetTheState checks that a command does not set the
// state of the entity.
//
// The state is what the device is doing, and the command is what Home
// Assistant asked for. The two are the same most of the time, but not
// always: the panel may be in a window when Home Assistant asks for a
// fullscreen, because the panel cannot change its own window at once.
//
// Setting the state from the command would mean the device's own report
// was ignored, because the value would be the same and a change that
// is the same is not a change. The state would then never move.
func TestCommandDoesNotSetTheState(t *testing.T) {
	registry := NewRegistry()
	entity := registry.Add(&Entity{
		Kind: KindSelect, Name: "Display mode", ObjectID: "display_mode",
		Options: []string{"fullscreen", "window"},
		State:   EntityValue{Kind: KindSelect, Text: "fullscreen"},
	})

	changed := 0
	registry.OnChange(func(*Entity) { changed++ })

	// Home Assistant asks for the window.
	if err := registry.HandleCommand(entity.Key, EntityValue{Kind: KindSelect, Text: "window"}); err != nil {
		t.Fatalf("the command was refused: %v", err)
	}
	// The state is still what the device is doing.
	if got := entity.StateOf(); got.Text != "fullscreen" {
		t.Errorf("the state is %q, the command should not have changed it", got.Text)
	}
	if changed != 0 {
		t.Errorf("the change was published %d times, it should not have been published at all", changed)
	}

	// The device reports what it is actually doing.
	registry.SetState(entity.Key, EntityValue{Kind: KindSelect, Text: "window"})
	if got := entity.StateOf(); got.Text != "window" {
		t.Errorf("the state is %q, it should be window", got.Text)
	}
	if changed != 1 {
		t.Errorf("the change was published %d times, it should have been published once", changed)
	}
}

// TestTheDevicePingsSoTheConnectionSurvives checks that the device says
// something now and then.
//
// Home Assistant waits for the device to say something and drops the
// connection after a minute of silence. So a panel that only listens is
// a panel that is reconnected every minute, and everything it is doing
// is interrupted every minute.
//
// The peer is aioesphomeapi's own client, so the ping is the one Home
// Assistant would see.
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
