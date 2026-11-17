package esphome

import (
	"bytes"
	"encoding/base64"
	"errors"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

func TestProvisioningKeyIsUsedWhenTheDeviceHasNone(t *testing.T) {
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

	lines, err := runPeer("list_entities", listener.Addr().String(), provisioningKey)
	if err != nil {
		if errors.Is(err, errSkipPeer) {
			t.Skip("the python peer is not available")
		}
		t.Fatalf("the peer failed: %v", err)
	}
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "name:home-panel") {
		t.Fatalf("the device did not give its name over the provisioning key: %v", lines)
	}
}

func TestPlaintextIsRefusedWhenTheDeviceHasAKey(t *testing.T) {

	want := []byte{
		0x01, 0x00, 0x13, 0x01,
		'B', 'a', 'd', ' ', 'i', 'n', 'd', 'i', 'c', 'a', 't', 'o', 'r', ' ', 'b', 'y', 't', 'e',
	}
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

	client, err := net.Dial("tcp", listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	body := []byte{0x0a, 0x0e, 'H', 'o', 'm', 'e', ' ', 'A', 's', 's', 'i', 's', 't', 'a', 'n', 't', 0x10, 0x01, 0x18, 0x10}
	frame := []byte{0x00, byte(len(body)), 0x01}
	frame = append(frame, body...)
	if _, err := client.Write(frame); err != nil {
		t.Fatal(err)
	}

	reply := make([]byte, len(want))
	client.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, err := io.ReadFull(client, reply); err != nil {
		t.Fatalf("the reply could not be read: %v", err)
	}
	if !bytes.Equal(reply, want) {
		t.Errorf("the reply is %x, it should be %x", reply, want)
	}
}

func TestPlaintextIsRefusedByTheServer(t *testing.T) {
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

func TestHandshakeFailureReplyMatchesESPHome(t *testing.T) {

	wrong := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{1}, 32))

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	server := NewServer(ServerOptions{
		Name: "home-panel", FriendlyName: "Home Panel", MAC: "aabbccddeeff",
		Version: "0.0.2", Registry: NewRegistry(),
		EncryptionKey: provisioningKey,
	})
	go server.Listen(listener)

	lines, err := runPeer("handshake_failure", listener.Addr().String(), wrong)
	if err != nil {
		if errors.Is(err, errSkipPeer) {
			t.Skip("the python peer is not available")
		}
		t.Fatalf("the peer failed: %v", err)
	}
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "Handshake MAC failure") {
		t.Fatalf("the failure was not reported the way ESPHome reports it: %v", lines)
	}
}
