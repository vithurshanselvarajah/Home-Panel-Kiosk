package main

import (
	"context"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestControlPlaneStarts checks that the control plane comes up: the
// API socket, the announcement, and the control socket.
func TestControlPlaneStarts(t *testing.T) {
	if testing.Short() {
		t.Skip("the control plane opens sockets")
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	socket := t.TempDir() + "/control.sock"
	plane := startControlPlane(context.Background(), log, 0, "", "home-panel-test", "Home Panel Test", socket, nil)
	defer plane.Close()

	if plane.listener == nil {
		t.Fatal("the API socket was not opened")
	}
	if plane.server == nil {
		t.Fatal("the control socket was not opened")
	}
	// The socket is there and can be connected to.
	conn, err := netDial(socket)
	if err != nil {
		t.Fatalf("the control socket could not be reached: %v", err)
	}
	conn.Close()

	// The panel's entities are registered.
	if plane.api.Registry() == nil {
		t.Fatal("there is no registry")
	}
	if len(plane.api.Registry().All()) == 0 {
		t.Fatal("there are no entities")
	}
	time.Sleep(100 * time.Millisecond)
}

// TestPanelAddressesAreOnTheNetwork checks that the address the panel
// is announced at is one on the network.
//
// The panel is announced on one interface, and it has to be the one
// that reaches the outside world. A machine has more interfaces than
// that: a bridge for each container network, and sometimes a tunnel.
// Home Assistant keeps the device under the address it was given, so
// an address on one of the others is an address it will keep failing
// on.
func TestPanelAddressesAreOnTheNetwork(t *testing.T) {
	addresses := panelAddresses()
	if len(addresses) == 0 {
		t.Skip("this machine has no address on a network")
	}
	outward := defaultRouteInterface()
	for _, address := range addresses {
		if address.IsLoopback() {
			t.Errorf("the loopback address %s was chosen", address)
		}
		if address.IsLinkLocalUnicast() {
			t.Errorf("the link-local address %s was chosen", address)
		}
	}
	// Every address has to be on the interface that carries the default
	// route, when there is one.
	if outward == "" {
		return
	}
	found, err := net.InterfaceByName(outward)
	if err != nil {
		t.Skipf("the default route interface %s could not be read: %v", outward, err)
	}
	addrs, err := found.Addrs()
	if err != nil {
		t.Skip("the addresses of the default route interface could not be read")
	}
	onTheNetwork := map[string]bool{}
	for _, addr := range addrs {
		ip, _, err := net.ParseCIDR(addr.String())
		if err != nil {
			continue
		}
		onTheNetwork[ip.String()] = true
	}
	for _, address := range addresses {
		if !onTheNetwork[address.String()] {
			t.Errorf("the address %s is not on the default route interface %s", address, outward)
		}
	}
}

// TestPanelMACIsStableAndUnique checks the address that identifies the
// panel.
//
// Home Assistant keeps the device under this address, so it has to be
// the same every time the panel starts. And it has to be different for
// every panel, because two panels that share it are one panel to Home
// Assistant and the second one is refused.
func TestPanelMACIsStableAndUnique(t *testing.T) {
	first := panelMAC("home-panel")
	second := panelMAC("home-panel")
	if first != second {
		t.Errorf("the address changed between runs: %s and %s", first, second)
	}
	other := panelMAC("another-panel")
	if first == other {
		t.Errorf("two panels have the same address: %s", first)
	}
	// The address has to look like one, because Home Assistant treats it
	// as one.
	parts := strings.Split(first, ":")
	if len(parts) != 6 {
		t.Fatalf("the address %q does not look like an address", first)
	}
	for _, part := range parts {
		if len(part) != 2 {
			t.Fatalf("the address %q does not look like an address", first)
		}
	}
}

// TestPanelMACIsOnTheAnnouncedInterface checks that the address belongs to
// the interface the panel announces itself on.
//
// The panel announces on the interface that carries the default route,
// because that is the one Home Assistant can reach it on. The address has
// to be that interface's: a panel that announced on one interface and
// answered with another's address is a device whose identity changes
// when the machine's interfaces are enumerated in a different order.
//
// The last three bytes are mixed from the name, so only the first three
// are compared — those are the interface's.
func TestPanelMACIsOnTheAnnouncedInterface(t *testing.T) {
	name := defaultRouteInterface()
	if name == "" {
		t.Skip("no interface carries the default route")
	}
	raw, err := os.ReadFile(filepath.Join("/sys/class/net", name, "address"))
	if err != nil {
		t.Skipf("the address of %s could not be read: %v", name, err)
	}
	interfaceMAC := strings.TrimSpace(string(raw))

	got := panelMAC("home-panel")
	if !strings.HasPrefix(got, interfaceMAC[:8]) {
		t.Errorf("the address %s is not on %s (%s)", got, name, interfaceMAC)
	}
}
