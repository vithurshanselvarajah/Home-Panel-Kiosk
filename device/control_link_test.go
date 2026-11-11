package main

import (
	"encoding/json"
	"net"
	"testing"
	"time"
)

// TestTheLinkStateIsReported checks that the panel is told whether Home
// Assistant is talking to the device program.
//
// The panel shows this in the drawer, and it cannot find out for itself:
// the socket between the panel and the device program is on this
// machine and is up whatever Home Assistant is doing.
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

	// The panel connects and waits to be told.
	conn, err := net.Dial("unix", server.socket)
	if err != nil {
		t.Fatalf("the panel could not reach the control socket: %v", err)
	}
	defer conn.Close()

	// Nothing has happened, so the link is down.
	server.SetConnected(false)
	// The panel is told.
	line := readLine(t, conn)
	if line.Action != "link" {
		t.Fatalf("the panel was told %q, it should have been told about the link", line.Action)
	}
	if line.Connected {
		t.Errorf("the panel was told the link is up, it is not")
	}

	// Home Assistant arrives.
	server.SetConnected(true)
	line = readLine(t, conn)
	if line.Action != "link" {
		t.Fatalf("the panel was told %q, it should have been told about the link", line.Action)
	}
	if !line.Connected {
		t.Errorf("the panel was told the link is down, it is up")
	}
}

// readLine reads one message from the panel's end of the socket.
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
