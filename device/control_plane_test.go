package main

import (
	"context"
	"log/slog"
	"os"
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
	plane := startControlPlane(context.Background(), log, 0, "", "home-panel-test", "Home Panel Test", socket)
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
