package sendspin

import (
	"fmt"
	"net"
	"strings"
	"testing"
	"time"
)

func startPeerPanel(t *testing.T) (*Client, string) {
	t.Helper()

	listener, err := net.Listen("tcp", ":0")
	if err != nil {
		t.Fatalf("no port was free: %v", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()

	client, err := New(Config{
		StorePath: fmt.Sprintf("%s/sendspin.json", t.TempDir()),
		Name:      "Test Panel",
		Instance:  "test-panel",
		Port:      port,

		Unpaired: true,
	})
	if err != nil {
		t.Fatalf("the player could not be made: %v", err)
	}
	if err := client.Start(); err != nil {
		t.Fatalf("the player could not be started: %v", err)
	}
	t.Cleanup(func() { client.Stop("the test is over") })

	return client, fmt.Sprintf("127.0.0.1:%d", port)
}

func TestTheRealServerTakesTheRoles(t *testing.T) {
	client, address := startPeerPanel(t)

	lines, err := runPeer("metadata", address, client.ClientID())
	if err != nil {
		if err == errSkipPeer {
			t.Skip("the python peer is not available")
		}
		t.Fatalf("the peer failed: %v", err)
	}

	var roles string
	for _, line := range lines {
		if strings.HasPrefix(line, "roles=") {
			roles = strings.TrimPrefix(line, "roles=")
		}
	}
	if roles == "" {
		t.Fatalf("the peer did not say what roles it took: %v", lines)
	}
	for _, role := range []string{"player@v1", "metadata@v1", "controller@v1"} {
		if !strings.Contains(roles, role) {
			t.Errorf("the server did not take the %s role, it took %s", role, roles)
		}
	}
}

func TestTheRealServerSaysWhatIsPlaying(t *testing.T) {
	client, address := startPeerPanel(t)

	peer, err := startPeer("metadata", address, client.ClientID())
	if err != nil {
		if err == errSkipPeer {
			t.Skip("the python peer is not available")
		}
		t.Fatalf("the peer could not be started: %v", err)
	}
	defer func() {
		if peer.Process != nil {
			peer.Process.Kill()
			peer.Wait()
		}
	}()

	seen := ""
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		status := client.Status()
		if status.Metadata.Title != "" {
			seen = status.Metadata.Title
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if seen == "" {
		t.Fatal("the panel never heard what was playing")
	}
	if seen != "Suck the Blood" {
		t.Errorf("the title was %q, want %q", seen, "Suck the Blood")
	}

	status := client.Status()
	if status.Metadata.Artist != "The Sleepy Jackson" {
		t.Errorf("the artist was %q, want %q", status.Metadata.Artist, "The Sleepy Jackson")
	}
	if status.Metadata.Album != "Fumbling Towards Fitness" {
		t.Errorf("the album was %q, want %q", status.Metadata.Album, "Fumbling Towards Fitness")
	}
	if status.Metadata.ArtworkURL != "http://192.168.1.20/cover.jpg" {
		t.Errorf("the artwork was %q, want %q", status.Metadata.ArtworkURL, "http://192.168.1.20/cover.jpg")
	}
	if status.Metadata.DurationMs != 195000 {
		t.Errorf("the length was %d, want 195000", status.Metadata.DurationMs)
	}
	if status.Metadata.ProgressMs != 30000 {
		t.Errorf("the position was %d, want 30000", status.Metadata.ProgressMs)
	}
	if status.Metadata.Speed != 1000 {
		t.Errorf("the speed was %d, want 1000", status.Metadata.Speed)
	}
}

func TestTheRealServerStoppingIsHeard(t *testing.T) {
	client, address := startPeerPanel(t)

	peer, err := startPeer("metadata", address, client.ClientID())
	if err != nil {
		if err == errSkipPeer {
			t.Skip("the python peer is not available")
		}
		t.Fatalf("the peer could not be started: %v", err)
	}
	defer func() {
		if peer.Process != nil {
			peer.Process.Kill()
			peer.Wait()
		}
	}()

	played := false
	stopped := false
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		status := client.Status()
		if status.Metadata.Title != "" {
			played = true
		} else if played {
			stopped = true
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !played {
		t.Fatal("the panel never heard what was playing")
	}
	if !stopped {
		t.Fatal("the panel was never told the music stopped")
	}
}
