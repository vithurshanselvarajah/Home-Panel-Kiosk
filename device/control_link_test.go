package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net"
	"testing"
	"time"
)

func TestCameraCaptureRoundTripsAcrossThePanelSocket(t *testing.T) {
	server := NewControlServer(ControlOptions{Socket: t.TempDir() + "/control.sock"})
	if err := server.Listen(); err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	conn, err := net.Dial("unix", server.socket)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	readLine(t, conn)

	type capture struct {
		data []byte
		err  error
	}
	result := make(chan capture, 1)
	go func() { data, err := server.CaptureCamera(context.Background()); result <- capture{data, err} }()
	command := readLine(t, conn)
	if command.Action != "camera_capture" || command.CameraRequestID == "" {
		t.Fatalf("unexpected camera request: %+v", command)
	}
	response, err := json.Marshal(ControlMessage{Action: "camera_frame", CameraRequestID: command.CameraRequestID, CameraData: base64.StdEncoding.EncodeToString([]byte{0xff, 0xd8, 0xff})})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Write(append(response, '\n')); err != nil {
		t.Fatal(err)
	}
	got := <-result
	if got.err != nil {
		t.Fatal(got.err)
	}
	if string(got.data) != string([]byte{0xff, 0xd8, 0xff}) {
		t.Fatalf("unexpected camera bytes: %v", got.data)
	}
}

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

// TestTheTransportIsAskedFor checks that the panel's transport buttons
// reach the device program.
//
// The transport belongs to the music server and the panel has the
// buttons, so the request travels over the socket.
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

	// The link message comes first, and it is not the answer to this.
	readLine(t, conn)

	// The panel presses play.
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

	// And it seeks.
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
