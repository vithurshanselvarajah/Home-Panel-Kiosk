package main

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// TestTheStateFileIsWrittenUnderPressure checks that the state file is
// written when several things change at once.
//
// The state is published from three places: the tracker, when something
// changes; the ticker, every hundredth of a second; and the music
// player's status, as often as it changes. They all write the same file,
// and a writer that shares its temporary file with another writer loses
// it: one of them renames it away and the other finds nothing there.
func TestTheStateFileIsWrittenUnderPressure(t *testing.T) {
	directory := t.TempDir()
	file := filepath.Join(directory, "state.json")

	// The errors are caught rather than printed, so the test fails when
	// the race happens instead of passing with a screenful of them.
	var logged bytes.Buffer
	var loggedMu sync.Mutex
	reporter := &stateReporter{
		log:  slog.New(slog.NewTextHandler(&lockedWriter{buffer: &logged, mu: &loggedMu}, &slog.HandlerOptions{Level: slog.LevelError})),
		file: file,
	}

	// Many writers at once, as happens when the music is playing and the
	// level is moving.
	var group sync.WaitGroup
	for writer := 0; writer < 8; writer++ {
		group.Add(1)
		go func() {
			defer group.Done()
			for write := 0; write < 50; write++ {
				reporter.writeFile(State{Connection: "connected", Volume: write})
			}
		}()
	}
	group.Wait()

	// Nothing was lost on the way.
	loggedMu.Lock()
	errors := logged.String()
	loggedMu.Unlock()
	if strings.Contains(errors, "the state could not be published") {
		t.Errorf("the state file was not published cleanly:\n%s", errors)
	}

	// The file is there and it is whole.
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("the state file could not be read: %v", err)
	}
	var state State
	if err := json.Unmarshal(data, &state); err != nil {
		t.Fatalf("the state file is not whole: %v", err)
	}
	if state.Connection != "connected" {
		t.Errorf("the connection was %q, want connected", state.Connection)
	}
}

// lockedWriter is a buffer that several goroutines can write to.
type lockedWriter struct {
	buffer *bytes.Buffer
	mu     *sync.Mutex
}

func (w *lockedWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buffer.Write(p)
}
