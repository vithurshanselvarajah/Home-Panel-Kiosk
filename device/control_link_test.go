package main

import (
	"encoding/json"
	"net"
	"testing"
	"time"
)

func TestTheLinkStateIsReported(t *testing.T) {
	registry := panelEntities(PanelCapabilities{
		CanControlScreen: true,
		HasBacklight:     true,
		HasSlideshow:     true,
	})
	server := NewControlServer(ControlOptions{
		Socket:   t.TempDir() + "/control.sock",
		Registry: registry,
	})
	if err := server.Listen(); err != nil {
		t.Fatalf("the control socket could not be opened: %v", err)
	}
	defer server.Close()

	conn, err := net.Dial("unix", server.socket)
	if err != nil {
		t.Fatalf("the panel could not reach the control socket: %v", err)
	}
	defer conn.Close()

	server.SetConnected(false)

	line := readLine(t, conn)
	if line.Action != "link" {
		t.Fatalf("the panel was told %q, it should have been told about the link", line.Action)
	}
	if line.Connected {
		t.Errorf("the panel was told the link is up, it is not")
	}

	server.SetConnected(true)
	line = readLine(t, conn)
	if line.Action != "link" {
		t.Fatalf("the panel was told %q, it should have been told about the link", line.Action)
	}
	if !line.Connected {
		t.Errorf("the panel was told the link is down, it is up")
	}
}

func readLine(t *testing.T, conn net.Conn) ControlMessage {
	t.Helper()
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	buf := make([]byte, 4096)
	n, err := conn.Read(buf)
	if err != nil {
		t.Fatalf("the panel could not be reached: %v", err)
	}
	var message ControlMessage
	if err := json.Unmarshal(buf[:n], &message); err != nil {
		t.Fatalf("the panel was told something that is not a message: %v", err)
	}
	return message
}

func TestTheTransportIsAskedFor(t *testing.T) {
	registry := panelEntities(PanelCapabilities{
		CanControlScreen: true,
	})
	server := NewControlServer(ControlOptions{
		Socket:   t.TempDir() + "/control.sock",
		Registry: registry,
	})
	if err := server.Listen(); err != nil {
		t.Fatalf("the control socket could not be opened: %v", err)
	}
	defer server.Close()

	asked := make(chan TransportCommand, 4)
	server.SetTransportHandler(func(command TransportCommand) {
		asked <- command
	})

	conn, err := net.Dial("unix", server.socket)
	if err != nil {
		t.Fatalf("the panel could not reach the control socket: %v", err)
	}
	defer conn.Close()

	readLine(t, conn)

	if _, err := conn.Write([]byte(`{"action":"transport","command":{"action":"play"}}` + "\n")); err != nil {
		t.Fatalf("the panel could not send the command: %v", err)
	}

	select {
	case command := <-asked:
		if command.Action != "play" {
			t.Errorf("the command was %q, it should have been play", command.Action)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the transport command never arrived")
	}

	if _, err := conn.Write([]byte(`{"action":"transport","command":{"action":"seek","positionMs":45000}}` + "\n")); err != nil {
		t.Fatalf("the panel could not send the command: %v", err)
	}

	select {
	case command := <-asked:
		if command.Action != "seek" {
			t.Errorf("the command was %q, it should have been seek", command.Action)
		}
		if command.PositionMs != 45000 {
			t.Errorf("the position was %d, it should have been 45000", command.PositionMs)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the seek never arrived")
	}
}
