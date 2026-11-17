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

func TestTheStateFileIsWrittenUnderPressure(t *testing.T) {
	directory := t.TempDir()
	file := filepath.Join(directory, "state.json")

	var logged bytes.Buffer
	var loggedMu sync.Mutex
	reporter := &stateReporter{
		log:  slog.New(slog.NewTextHandler(&lockedWriter{buffer: &logged, mu: &loggedMu}, &slog.HandlerOptions{Level: slog.LevelError})),
		file: file,
	}

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

	loggedMu.Lock()
	errors := logged.String()
	loggedMu.Unlock()
	if strings.Contains(errors, "the state could not be published") {
		t.Errorf("the state file was not published cleanly:\n%s", errors)
	}

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

type lockedWriter struct {
	buffer *bytes.Buffer
	mu     *sync.Mutex
}

func (w *lockedWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buffer.Write(p)
}
