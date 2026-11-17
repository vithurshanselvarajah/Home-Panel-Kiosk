package esphome

import (
	"bytes"
	"io"
	"net"
	"testing"
)

func TestEncryptedFrameRoundTrip(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	key := bytes.Repeat([]byte{7}, 32)
	raw := key

	responder, err := newNoiseConnection(server, raw)
	if err != nil {
		t.Fatal(err)
	}
	initiator, err := newNoiseConnection(client, raw)
	if err != nil {
		t.Fatal(err)
	}

	initiator.handshake, err = newInitiatorHandshake(raw)
	if err != nil {
		t.Fatal(err)
	}

	done := make(chan error, 1)
	go func() {

		var indicator [1]byte
		if _, err := io.ReadFull(server, indicator[:]); err != nil {
			done <- err
			return
		}
		if indicator[0] != 0x01 {
			done <- errWrongIndicator
			return
		}
		responder.pending = indicator[:]

		if err := responder.readClientHandshake(); err != nil {
			done <- err
			return
		}

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

	first, _, _, err := initiator.handshake.WriteMessage(nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	go func() {
		client.Write([]byte{0x01})
		client.Write([]byte{0x00, 0x00})
		header := []byte{0x01, 0x00, byte(len(first) + 1)}
		client.Write(header)
		client.Write([]byte{0})
		client.Write(first)
	}()

	go func() {

	}()

	buf := make([]byte, 3)
	if _, err := client.Read(buf[:]); err != nil {
		t.Fatal(err)
	}
	length := int(buf[1])<<8 | int(buf[2])
	payload := make([]byte, length)
	if _, err := client.Read(payload); err != nil {
		t.Fatal(err)
	}

	_, send, recv, err := initiator.handshake.ReadMessage(nil, payload[1:])
	if err != nil {
		t.Fatalf("the client could not read the handshake reply: %v", err)
	}
	initiator.send = send
	initiator.recv = recv

	if err := initiator.writeEncrypted(TypeHelloRequest, []byte("hello")); err != nil {
		t.Fatal(err)
	}

	if err := <-done; err != nil {
		t.Fatalf("the server side failed: %v", err)
	}
}
