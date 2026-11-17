package main

import "testing"

func TestTheVolumeFollowerIsToldThePanelVolume(t *testing.T) {
	level := 42
	got := 0
	called := 0

	server := NewControlServer(ControlOptions{Socket: t.TempDir() + "/c.sock"})
	server.SetVolumeFollower(func(v int) { got = v; called++ })

	server.SetState(ControlState{Volume: &level})

	if called != 1 {
		t.Errorf("the follower was called %d times, want 1", called)
	}
	if got != 42 {
		t.Errorf("the follower was given %d, want 42", got)
	}
}

func TestAnUnknownVolumeIsNotFollowed(t *testing.T) {
	called := 0
	server := NewControlServer(ControlOptions{Socket: t.TempDir() + "/c.sock"})
	server.SetVolumeFollower(func(int) { called++ })

	server.SetState(ControlState{})

	if called != 0 {
		t.Errorf("the follower was called %d times for an unknown volume, want 0", called)
	}
}
