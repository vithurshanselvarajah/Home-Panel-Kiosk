package main

import (
	"context"
	"testing"
	"time"
)

// The output loop's own cleanup must leave the speaker free.
func TestTheOutputLoopLeavesTheSpeakerFree(t *testing.T) {
	ctx := context.Background()
	a := &deviceAudio{backend: &fakeBackend{}, log: testLogger()}
	a.volume = 100
	if err := a.useExternalPlayback(ctx); err != nil {
		t.Fatalf("the playback could not be prepared: %v", err)
	}
	a.ensureOutput()
	time.Sleep(300 * time.Millisecond)
	// The loop is stopped the way it stops itself: the output is closed.
	a.closeOutput()
	time.Sleep(300 * time.Millisecond)

	b := &deviceAudio{backend: newFileBackend("", t.TempDir()+"/s.wav"), log: testLogger()}
	b.volume = 100
	if err := b.useExternalPlayback(ctx); err != nil {
		t.Fatalf("the second playback could not be prepared: %v", err)
	}
	if err := b.startPortedSpeaker(ctx); err != nil {
		t.Fatalf("the second start: %v", err)
	}
	time.Sleep(300 * time.Millisecond)
	b.stopPortedSpeaker()
}
