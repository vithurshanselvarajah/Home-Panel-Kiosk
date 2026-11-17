package esphome

import (
	"errors"
	"net"
	"strings"
	"testing"
)

func TestPlainTextIsAllowedWithoutAKey(t *testing.T) {
	server := NewServer(ServerOptions{
		Name: "home-panel", FriendlyName: "Home Panel", MAC: "aabbccddeeff",
		Version: "0.0.2", Registry: NewRegistry(),
	})
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go server.Listen(listener)

	lines, err := runPeer("list_entities", listener.Addr().String(), "")
	if err != nil {
		if errors.Is(err, errSkipPeer) {
			t.Skip("the python peer is not available")
		}
		t.Fatalf("the client could not talk to the device: %v", err)
	}
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "name:home-panel") {
		t.Fatalf("the device did not answer in plain text: %v", lines)
	}
}

func TestAPlainTextClientIsRefusedWhenThereIsAKey(t *testing.T) {
	server := NewServer(ServerOptions{
		Name: "home-panel", FriendlyName: "Home Panel", MAC: "aabbccddeeff",
		Version: "0.0.2", Registry: NewRegistry(),
		EncryptionKey: provisioningKey,
	})
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go server.Listen(listener)

	lines, err := runPeer("list_entities", listener.Addr().String(), "")
	if err == nil {
		joined := strings.Join(lines, "\n")
		if strings.Contains(joined, "name:home-panel") {
			t.Fatalf("the device answered in plain text although it has a key: %v", lines)
		}
	}
}
