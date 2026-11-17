package esphome

import (
	"net"
	"sync"
	"testing"
	"time"
)

// The link to Home Assistant.
//
// The panel shows whether Home Assistant is talking to it, and the only
// thing that knows is the server: the socket between the panel and the
// device program is on this machine and is up whatever Home Assistant
// is doing.
//
// There can be more than one connection at a time. Home Assistant
// connects, and the panel's own discovery probe connects as well, and
// the probe goes away again.
//
// So the link has to be reported while *any* connection is open. A
// report that said "disconnected" when one of several connections
// closed would say the panel was disconnected while Home Assistant was
// talking to it.

// linkRecorder remembers what the link was reported as.
type linkRecorder struct {
	mu    sync.Mutex
	links []bool
}

// record is called when the link changes.
func (l *linkRecorder) record(connected bool) {
	l.mu.Lock()
	l.links = append(l.links, connected)
	l.mu.Unlock()
}

// wentDown says whether the link was reported as down.
//
// The report is a moment of truth, so the check waits for it rather
// than taking it as it comes: the report happens as the connection
// closes, which is not the same moment as the test's check.
func (l *linkRecorder) wentDown(within time.Duration) bool {
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		l.mu.Lock()
		links := append([]bool(nil), l.links...)
		l.mu.Unlock()
		for _, link := range links {
			if !link {
				return true
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	return false
}

// TestTheLinkSurvivesAConnectionGoingAway checks that the link stays up
// while another connection is open.
//
// The panel's discovery probe connects and goes away, and the panel
// must not say it is disconnected because of it.
func TestTheLinkSurvivesAConnectionGoingAway(t *testing.T) {
	server := NewServer(ServerOptions{
		Name:         "home-panel",
		FriendlyName: "Home Panel",
		MAC:          "aabbccddeeff",
	})
	recorder := &linkRecorder{}
	server.OnConnect(recorder.record)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go server.Listen(listener)

	// Two connections, the way there are two when Home Assistant is
	// talking and the panel is being discovered.
	first := dialPlain(t, listener.Addr().String())
	defer first.Close()
	waitForConnections(t, server, 1)

	second := dialPlain(t, listener.Addr().String())
	defer second.Close()
	// The second connection is waited for by the number of connections
	// and not by the link, because the link is already up and waiting
	// for it would say nothing about the second connection.
	waitForConnections(t, server, 2)

	// The first one goes away. The second is still there, so the link
	// is still up.
	first.Close()
	time.Sleep(300 * time.Millisecond)

	// The link has to still be up. The reports made after the second
	// connection are the ones that matter: the first connection's
	// closing must not have been reported.
	if recorder.wentDown(200 * time.Millisecond) {
		t.Errorf("the link was reported as down while a connection was still open: %v", recorder.links)
	}

	// The last one goes away too, and now the link really is down.
	second.Close()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		recorder.mu.Lock()
		links := append([]bool(nil), recorder.links...)
		recorder.mu.Unlock()
		if len(links) > 0 && !links[len(links)-1] {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Errorf("the link was never reported as down: %v", recorder.links)
}

// waitForConnections waits for the server to have so many connections.
//
// The link is not enough to wait on, because the link is already up
// when the second connection is on its way.
func waitForConnections(t *testing.T, server *Server, want int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if server.openConnections() == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("the server never had %d connections open", want)
}

// dialPlain opens a connection and says it speaks in plain text.
//
// The server reads the first byte to know which conversation it is, so
// a connection that says nothing is refused before it is a connection.
func dialPlain(t *testing.T, address string) net.Conn {
	t.Helper()
	conn, err := net.Dial("tcp", address)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Write([]byte{0x00}); err != nil {
		t.Fatal(err)
	}
	return conn
}

// openConnections is how many connections the server has open.
//
// It is here rather than in the server because it is only wanted by the
// test, and a method on the server that only the test uses is a method
// the server does not need.
func (s *Server) openConnections() int {
	count := 0
	s.connections.Range(func(_, _ any) bool {
		count++
		return true
	})
	return count
}
