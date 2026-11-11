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

// TestProvisioningKeyIsUsedWhenTheDeviceHasNone checks the handshake a
// client does when the device has no key.
//
// Home Assistant asks for the device's details before it has the key,
// and it does that over an encrypted connection made with the
// provisioning key. The point of it is that the real key never crosses
// the network in plain text: the client learns the name of the device
// and finds out that a key is needed.
//
// So a device with no key answers the handshake with the provisioning
// key, which is all zeros.
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

	// The client uses the provisioning key, which is what Home
	// Assistant does when it finds a device that has none.
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

// TestPlaintextIsRefusedWhenTheDeviceHasAKey checks that a device with
// a key does not speak in plain text.
//
// The key is there so that nothing is said in plain text. A client that
// starts in plain text is told so rather than being left waiting: the
// answer is a frame with the encrypted indicator, which is what the
// client reads as "this device needs a key".
func TestPlaintextIsRefusedWhenTheDeviceHasAKey(t *testing.T) {
	// The reply is the one a real ESPHome device gives. It was taken
	// from one, so the bytes have to match.
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
	// A plaintext hello request.
	body := []byte{0x0a, 0x0e, 'H', 'o', 'm', 'e', ' ', 'A', 's', 's', 'i', 's', 't', 'a', 'n', 't', 0x10, 0x01, 0x18, 0x10}
	frame := []byte{0x00, byte(len(body)), 0x01}
	frame = append(frame, body...)
	if _, err := client.Write(frame); err != nil {
		t.Fatal(err)
	}
	// The refusal comes back on the connection.
	reply := make([]byte, len(want))
	client.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, err := io.ReadFull(client, reply); err != nil {
		t.Fatalf("the reply could not be read: %v", err)
	}
	if !bytes.Equal(reply, want) {
		t.Errorf("the reply is %x, it should be %x", reply, want)
	}
}

// TestPlaintextIsRefusedByTheServer checks the same thing through the
// server rather than through the frame.
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

	// The client speaks in plain text.
	lines, err := runPeer("list_entities", listener.Addr().String(), "")
	if err == nil {
		joined := strings.Join(lines, "\n")
		if strings.Contains(joined, "name:home-panel") {
			t.Fatalf("the device answered in plain text although it has a key: %v", lines)
		}
	}
}

// TestHandshakeFailureReplyMatchesESPHome checks the reply the device
// gives when the client does not know the key.
//
// The bytes were taken from a real ESPHome device, so the client reads
// them the same way. The reply is what tells the client that the key is
// wrong rather than that the device has gone.
func TestHandshakeFailureReplyMatchesESPHome(t *testing.T) {
	// A client that knows the wrong key.
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

	// The client uses the wrong key and reads what comes back.
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
