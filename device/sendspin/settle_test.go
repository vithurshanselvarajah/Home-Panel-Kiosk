package sendspin

import (
	"testing"
	"time"
)

// The first chunk of a stream must play on arrival.
//
// The server stamps a chunk with the moment it was sent, not the moment it
// is to be played, so the first chunk of a stream is already a moment old
// by the time it arrives. The anchor is laid from that chunk's timestamp,
// and an anchor laid from a stale timestamp is in the future: the audio is
// then a second ahead of the card, and the drift correction spends the next
// few seconds pulling it back a second at a time.
//
// That is the jitter at the start of a track, and it is gone when the
// offset is clamped at zero: the audio plays on arrival, as it does.
func TestTheFirstChunkPlaysOnArrival(t *testing.T) {
	p := newPlacement(syncedFilter())
	p.open()

	// The card is at frame zero and the output has no latency.
	p.position = func() (uint64, time.Duration) { return 0, 0 }
	p.latency = func() time.Duration { return 0 }

	// A chunk stamped a second ago: the server sent it a second ago.
	p.write(nowUs()-1_000_000, []int16{1000, -1000})

	p.mu.Lock()
	anchor := p.frame
	p.mu.Unlock()

	// The anchor must be at the card, not a second ahead of it.
	if anchor > outRate/10 {
		t.Fatalf("the anchor is %d frames, want it at the card: the chunk was placed in the future", anchor)
	}
}

// A chunk that is already late must not be dropped.
//
// The audio the server sends may arrive after the moment it was meant for,
// and it still belongs where it was meant to go.
func TestALateChunkIsStillPlaced(t *testing.T) {
	p := newPlacement(syncedFilter())
	p.open()
	p.position = func() (uint64, time.Duration) { return 0, 0 }
	p.latency = func() time.Duration { return 0 }

	// A chunk stamped two seconds ago.
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

// The anchor must not be laid before the speaker has started.
//
// A speaker that has not written to the card has no position and no
// latency reading, so an anchor laid against it is the send-ahead alone:
// the audio is a moment ahead of a card that has not started, and the
// drift correction spends the first seconds of the track pulling it back.
//
// The chunks that arrive in the meantime are held rather than dropped, and
// placed once the anchor is laid.
func TestTheAnchorWaitsForTheSpeaker(t *testing.T) {
	p := newPlacement(syncedFilter())
	p.open()

	started := false
	p.started = func() bool { return started }
	p.position = func() (uint64, time.Duration) { return 0, 0 }
	p.latency = func() time.Duration { return 0 }

	// The first chunk arrives before the speaker has started.
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

	// The speaker starts, and the next chunk lays the anchor and releases
	// the one that was held.
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

// The chunk that was held must be played whole.
//
// The anchor is laid when the speaker starts, and the chunks that arrived
// before it are released then. If the anchor is laid against the chunk
// that is arriving now, the waiting chunks are older than the anchor, and
// the anchor puts them before the card: they are dropped as late, and the
// track starts with a gap.
//
// This is the jitter at the start of a track: it happens on a fresh boot
// and after a pause, which are the two times the speaker is not running
// when the first chunk arrives.
func TestTheHeldChunkIsPlayedWhole(t *testing.T) {
	p := newPlacement(syncedFilter())
	p.open()

	started := false
	p.started = func() bool { return started }
	p.position = func() (uint64, time.Duration) { return 0, 0 }
	p.latency = func() time.Duration { return 0 }

	// The first chunk arrives before the speaker has started. It is a
	// hundred milliseconds of audio.
	const frames = outRate / 10
	first := make([]int16, frames*outChannels)
	for i := range first {
		first[i] = int16(i)
	}

	// It is stamped with the moment it was sent, which is a hundred
	// milliseconds before the next one.
	p.write(nowUs()-100*1000, first)

	// The speaker starts, and the next chunk arrives.
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

// A server that is connected but paused is not playing music.
//
// The decoder is kept across the end of a stream, so a server that has
// been paused still has one. A command sent to it pauses nothing, and
// the music Home Assistant is playing carries on: the person pressed
// pause and the music did not stop.
func TestAPausedServerIsNotPlayingMusic(t *testing.T) {
	s := &session{filter: newTimeFilter()}

	// The server has said it is playing.
	s.serverSaid = true
	s.serverPlaying = true
	if !s.playingMusic() {
		t.Fatal("the server says it is playing, and it does not")
	}

	// It is paused now.
	s.serverPlaying = false
	if s.playingMusic() {
		t.Error("the server is paused, and it says it is playing music")
	}

	// And playing again.
	s.serverPlaying = true
	if !s.playingMusic() {
		t.Error("the server is playing again, and it does not say so")
	}
}

// A pause asked for here is a request, not a pause.
//
// The server has been told to stop and has not said so yet. The music is
// still the server's to play, so a command sent to it is not swallowed.
func TestAPauseAskedForHereIsNotYetAPause(t *testing.T) {
	s := &session{filter: newTimeFilter()}
	s.serverSaid = true
	s.serverPlaying = true

	// The panel asks for a pause.
	s.asked.Store("pause")
	if s.playingMusic() {
		t.Error("the server is still playing, and it says it is not")
	}
}

// A server that has not said anything is not playing music.
//
// A server that has never answered has never said it is playing, and a
// command sent to it pauses nothing.
func TestAServerThatHasNotSpokenIsNotPlayingMusic(t *testing.T) {
	s := &session{filter: newTimeFilter()}
	if s.playingMusic() {
		t.Error("the server has not said anything, and it says it is playing music")
	}
}
