package sendspin

import (
	"testing"
	"time"
)

// The device must ask for the format the speaker takes.
//
// The speaker takes interleaved stereo frames. A player that advertised
// mono and wrote each mono sample into both halves of a frame left the
// reader with a frame whose two halves were the same sample, so the reader
// heard it twice and played it at double speed.
func TestTheAdvertisedFormatIsStereo(t *testing.T) {
	if outChannels != 2 {
		t.Fatalf("the device asks for %d channels, want 2", outChannels)
	}
}

// The decoder must produce interleaved stereo, so the samples it hands on
// are the ones the server sent, in the order they were sent.
func TestTheDecoderKeepsBothChannels(t *testing.T) {
	d, err := newDecoder(streamFormat{Codec: "pcm", SampleRate: outRate, Channels: 2, BitDepth: outBits})
	if err != nil {
		t.Fatalf("the decoder refused a stereo stream: %v", err)
	}
	payload := []byte{
		0xe8, 0x03, 0x18, 0xfc, // 1000, -1000
		0xd0, 0x07, 0x30, 0xf8, // 2000, -2000
	}
	got, err := d.decode(nil, payload)
	if err != nil {
		t.Fatalf("the decoder failed: %v", err)
	}
	want := []int16{1000, -1000, 2000, -2000}
	if len(got) != len(want) {
		t.Fatalf("the decoder gave %d samples, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("sample %d is %d, want %d (the whole frame is %v)", i, got[i], want[i], got)
		}
	}
}

// The placement must hand the speaker the audio for the frames it asks for.
//
// The frame index is the timeline: the audio was placed on it when it
// arrived, and the speaker takes it from there. So the frames the speaker
// asks for are the frames it gets.
func TestThePlacementGivesTheFramesAskedFor(t *testing.T) {
	p := newPlacement(syncedFilter())
	p.open()

	// The placement's position: the speaker is at frame zero and the
	// output has no latency, so a chunk stamped now lands at frame zero.
	p.position = func() (uint64, time.Duration) { return 0, 0 }
	p.latency = func() time.Duration { return 0 }

	// A chunk of two frames: 1000,-1000 and 2000,-2000.
	p.write(nowUs(), []int16{1000, -1000, 2000, -2000})

	out := make([]int16, 4)
	p.Render(0, out)

	want := []int16{1000, -1000, 2000, -2000}
	for i := range want {
		if out[i] != want[i] {
			t.Fatalf("sample %d is %d, want %d (the whole frame is %v)", i, out[i], want[i], out)
		}
	}
}

// syncedFilter is a time filter that has converged, which is what the
// placement needs before it will anchor.
func syncedFilter() *timeFilter {
	f := newTimeFilter()
	// The filter takes a measurement at a time that moves forward; the
	// first one cannot be at zero, which is where it starts.
	f.update(0, 100, 1_000_000)
	f.update(0, 100, 2_000_000)
	return f
}
