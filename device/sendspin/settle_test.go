package sendspin

import (
	"testing"
	"time"
)

func TestTheFirstChunkPlaysOnArrival(t *testing.T) {
	p := newPlacement(syncedFilter())
	p.open()

	p.position = func() (uint64, time.Duration) { return 0, 0 }
	p.latency = func() time.Duration { return 0 }

	p.write(nowUs()-1_000_000, []int16{1000, -1000})

	p.mu.Lock()
	anchor := p.frame
	p.mu.Unlock()

	if anchor > outRate/10 {
		t.Fatalf("the anchor is %d frames, want it at the card: the chunk was placed in the future", anchor)
	}
}

func TestALateChunkIsStillPlaced(t *testing.T) {
	p := newPlacement(syncedFilter())
	p.open()
	p.position = func() (uint64, time.Duration) { return 0, 0 }
	p.latency = func() time.Duration { return 0 }

	p.write(nowUs()-2_000_000, []int16{1000, -1000})

	if p.stats().Backwards != 0 {
		t.Fatalf("the late chunk was dropped: %d", p.stats().Backwards)
	}
	p.mu.Lock()
	queued := len(p.pcm)
	p.mu.Unlock()
	if queued == 0 {
		t.Fatal("the late chunk was not queued")
	}
}

func TestTheAnchorWaitsForTheSpeaker(t *testing.T) {
	p := newPlacement(syncedFilter())
	p.open()

	started := false
	p.started = func() bool { return started }
	p.position = func() (uint64, time.Duration) { return 0, 0 }
	p.latency = func() time.Duration { return 0 }

	p.write(nowUs(), []int16{1000, -1000, 2000, -2000})

	p.mu.Lock()
	anchored := p.anchored
	held := len(p.pending)
	p.mu.Unlock()

	if anchored {
		t.Fatal("the anchor was laid before the speaker started")
	}
	if held != 1 {
		t.Fatalf("%d chunks are held, want 1", held)
	}

	started = true
	p.write(nowUs()+1000, []int16{3000, -3000, 4000, -4000})

	p.mu.Lock()
	anchored = p.anchored
	held = len(p.pending)
	queued := len(p.pcm)
	p.mu.Unlock()

	if !anchored {
		t.Fatal("the anchor was not laid once the speaker started")
	}
	if held != 0 {
		t.Fatalf("%d chunks are still held, want none", held)
	}
	if queued == 0 {
		t.Fatal("nothing is queued: the chunk that was held was lost")
	}
}

func TestTheHeldChunkIsPlayedWhole(t *testing.T) {
	p := newPlacement(syncedFilter())
	p.open()

	started := false
	p.started = func() bool { return started }
	p.position = func() (uint64, time.Duration) { return 0, 0 }
	p.latency = func() time.Duration { return 0 }

	const frames = outRate / 10
	first := make([]int16, frames*outChannels)
	for i := range first {
		first[i] = int16(i)
	}

	p.write(nowUs()-100*1000, first)

	started = true
	p.write(nowUs(), []int16{3000, -3000, 4000, -4000})

	p.mu.Lock()
	queued := len(p.pcm) / outChannels
	late := p.late
	dropped := p.dropped
	p.mu.Unlock()

	if late != 0 {
		t.Errorf("%d chunks were dropped as late, the chunk that was held was never played", late)
	}
	if dropped != 0 {
		t.Errorf("%d chunks were dropped", dropped)
	}
	if queued < frames {
		t.Errorf("%d frames are queued, want at least %d: the chunk that was held lost its beginning",
			queued, frames)
	}
}

func TestAPausedServerIsNotPlayingMusic(t *testing.T) {
	s := &session{filter: newTimeFilter()}

	s.serverSaid = true
	s.serverPlaying = true
	if !s.playingMusic() {
		t.Fatal("the server says it is playing, and it does not")
	}

	s.serverPlaying = false
	if s.playingMusic() {
		t.Error("the server is paused, and it says it is playing music")
	}

	s.serverPlaying = true
	if !s.playingMusic() {
		t.Error("the server is playing again, and it does not say so")
	}
}

func TestAPauseAskedForHereIsNotYetAPause(t *testing.T) {
	s := &session{filter: newTimeFilter()}
	s.serverSaid = true
	s.serverPlaying = true

	s.asked.Store("pause")
	if s.playingMusic() {
		t.Error("the server is still playing, and it says it is not")
	}
}

func TestAServerThatHasNotSpokenIsNotPlayingMusic(t *testing.T) {
	s := &session{filter: newTimeFilter()}
	if s.playingMusic() {
		t.Error("the server has not said anything, and it says it is playing music")
	}
}
