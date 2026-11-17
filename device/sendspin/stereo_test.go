package sendspin

import (
	"testing"
	"time"
)

func TestTheAdvertisedFormatIsStereo(t *testing.T) {
	if outChannels != 2 {
		t.Fatalf("the device asks for %d channels, want 2", outChannels)
	}
}

func TestTheDecoderKeepsBothChannels(t *testing.T) {
	d, err := newDecoder(streamFormat{Codec: "pcm", SampleRate: outRate, Channels: 2, BitDepth: outBits})
	if err != nil {
		t.Fatalf("the decoder refused a stereo stream: %v", err)
	}
	payload := []byte{
		0xe8, 0x03, 0x18, 0xfc,
		0xd0, 0x07, 0x30, 0xf8,
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

func TestThePlacementGivesTheFramesAskedFor(t *testing.T) {
	p := newPlacement(syncedFilter())
	p.open()

	p.position = func() (uint64, time.Duration) { return 0, 0 }
	p.latency = func() time.Duration { return 0 }

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

func syncedFilter() *timeFilter {
	f := newTimeFilter()

	f.update(0, 100, 1_000_000)
	f.update(0, 100, 2_000_000)
	return f
}

func TestTheAdvertisedBufferIsThirtySeconds(t *testing.T) {
	if bufferSeconds != 30 {
		t.Fatalf("the device advertises %d seconds of buffer, want 30", bufferSeconds)
	}

	want := 30 * outRate * outChannels * outBits / 8
	if bufferCapacity != want {
		t.Errorf("the buffer capacity is %d bytes, want %d", bufferCapacity, want)
	}

	if holdMax < 2*bufferSeconds*outRate {
		t.Errorf("the queue is bounded at %d seconds, but the server may queue %d", holdMax/outRate, 2*bufferSeconds)
	}
}
