package esphome

import (
	"net"
	"sync"
	"testing"
	"time"
)

type linkRecorder struct {
	mu    sync.Mutex
	links []bool
}

func (l *linkRecorder) record(connected bool) {
	l.mu.Lock()
	l.links = append(l.links, connected)
	l.mu.Unlock()
}

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

	first := dialPlain(t, listener.Addr().String())
	defer first.Close()
	waitForConnections(t, server, 1)

	second := dialPlain(t, listener.Addr().String())
	defer second.Close()

	waitForConnections(t, server, 2)

	first.Close()
	time.Sleep(300 * time.Millisecond)

	if recorder.wentDown(200 * time.Millisecond) {
		t.Errorf("the link was reported as down while a connection was still open: %v", recorder.links)
	}

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

func (s *Server) openConnections() int {
	count := 0
	s.connections.Range(func(_, _ any) bool {
		count++
		return true
	})
	return count
}
