package esphome

import (
	"bytes"
	"net"
	"testing"
)

// TestEncryptedFrameRoundTrip checks that a frame survives the trip
// through the cipher.
//
// The two ends are made with the same code, which is enough to catch a
// frame that is built wrongly: the type, the length and the body all
// have to come back exactly as they went out.
func TestEncryptedFrameRoundTrip(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	key := bytes.Repeat([]byte{7}, 32)
	raw := key

	// The responder is the device. The initiator is the client.
	responder, err := newNoiseConnection(server, raw)
	if err != nil {
		t.Fatal(err)
	}
	initiator, err := newNoiseConnection(client, raw)
	if err != nil {
		t.Fatal(err)
	}
	// The client is the initiator, so its handshake state is the other
	// way round. The library needs to be told.
	initiator.handshake, err = newInitiatorHandshake(raw)
	if err != nil {
		t.Fatal(err)
	}

	done := make(chan error, 1)
	go func() {
		// The server reads the client's handshake and answers it.
		if err := responder.readClientHandshake(); err != nil {
			done <- err
			return
		}
		// Then it reads an encrypted frame.
		frame, err := responder.readEncrypted()
		if err != nil {
			done <- err
			return
		}
		if frame.Type != TypeHelloRequest {
			done <- errWrongType
			return
		}
		if string(frame.Payload) != "hello" {
			done <- errWrongBody
			return
		}
		done <- nil
	}()

	// The client writes its handshake.
	first, _, _, err := initiator.handshake.WriteMessage(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	// The client sends the marker and then the handshake.
	if _, err := client.Write([]byte{0x01, 0x00, 0x00}); err != nil {
		t.Fatal(err)
	}
	header := []byte{0x01, 0x00, byte(len(first) + 1)}
	if _, err := client.Write(header); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Write([]byte{0}); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Write(first); err != nil {
		t.Fatal(err)
	}

	// The server answers: the hello and then the handshake reply.
	go func() {
		// The server hello is written by the caller in the real flow.
	}()

	// Read the server's handshake reply.
	buf := make([]byte, 3)
	if _, err := client.Read(buf[:]); err != nil {
		t.Fatal(err)
	}
	length := int(buf[1])<<8 | int(buf[2])
	payload := make([]byte, length)
	if _, err := client.Read(payload); err != nil {
		t.Fatal(err)
	}
	// The initiator finishes the handshake by reading the server's
	// reply, and that is where its cipher states come from.
	_, send, recv, err := initiator.handshake.ReadMessage(nil, payload[1:])
	if err != nil {
		t.Fatalf("the client could not read the handshake reply: %v", err)
	}
	initiator.send = send
	initiator.recv = recv

	// Now the client sends an encrypted frame.
	if err := initiator.writeEncrypted(TypeHelloRequest, []byte("hello")); err != nil {
		t.Fatal(err)
	}

	if err := <-done; err != nil {
		t.Fatalf("the server side failed: %v", err)
	}
}
