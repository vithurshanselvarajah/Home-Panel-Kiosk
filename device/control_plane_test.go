package main

import (
	"context"
	"log/slog"
	"net"
	"os"
	"strings"
	"testing"
	"time"
)

func TestControlPlaneStarts(t *testing.T) {
	if testing.Short() {
		t.Skip("the control plane opens sockets")
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	socket := t.TempDir() + "/control.sock"
	plane := startControlPlane(context.Background(), log, 0, "", "home-panel-test", "Home Panel Test", socket, nil, nil, nil)
	defer plane.Close()

	if plane.listener == nil {
		t.Fatal("the API socket was not opened")
	}
	if plane.server == nil {
		t.Fatal("the control socket was not opened")
	}

	conn, err := netDial(socket)
	if err != nil {
		t.Fatalf("the control socket could not be reached: %v", err)
	}
	conn.Close()

	if plane.api.Registry() == nil {
		t.Fatal("there is no registry")
	}
	if len(plane.api.Registry().All()) == 0 {
		t.Fatal("there are no entities")
	}
	time.Sleep(100 * time.Millisecond)
}

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
