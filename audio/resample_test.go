package audio

import (
	"testing"
)

// TestTheResamplerMakesTheSamplesRight checks that the conversion
// produces the right number of samples.
//
// The reply arrives at 16 kHz and is played at 48 kHz, so three samples
// come out for every one that goes in. A reply that was not converted
// would be played three times too fast.
func TestTheResamplerMakesTheSamplesRight(t *testing.T) {
	r := NewResampler(16000, 48000)

	// One hundred samples at 16 kHz.
	in := make([]byte, 200)
	for i := 0; i < 100; i++ {
		in[i*2] = byte(i)
		in[i*2+1] = 0
	}

	out := append(r.Convert(in), r.Flush()...)
	if len(out) != 600 {
		t.Fatalf("the conversion produced %d bytes, want 600", len(out))
	}
}

// TestTheResamplerKeepsTheSamples checks that the conversion carries the
// audio through.
//
// A ramp goes up by one for every sample, so the converted audio has to
// go up by one for every three samples. A conversion that dropped the
// audio or scaled it would not.
func TestTheResamplerKeepsTheSamples(t *testing.T) {
	r := NewResampler(16000, 48000)

	in := make([]byte, 200)
	for i := 0; i < 100; i++ {
		in[i*2] = byte(i)
		in[i*2+1] = 0
	}

	out := append(r.Convert(in), r.Flush()...)

	// The first sample is the first one that went in.
	if got := int16(uint16(out[0]) | uint16(out[1])<<8); got != 0 {
		t.Errorf("the first sample was %d, want 0", got)
	}
	// The last input is 99, and the last output holds it.
	if got := int16(uint16(out[len(out)-2]) | uint16(out[len(out)-1])<<8); got != 99 {
		t.Errorf("the last sample was %d, want 99", got)
	}
}

// TestTheResamplerIsContinuousAcrossChunks checks that a reply converted
// one chunk at a time is the same as one converted in one go.
//
// The reply arrives in chunks. A conversion that started afresh on every
// chunk would put a step at every boundary, and a step is a click.
func TestTheResamplerIsContinuousAcrossChunks(t *testing.T) {
	in := make([]byte, 200)
	for i := 0; i < 100; i++ {
		in[i*2] = byte(i)
		in[i*2+1] = 0
	}

	single := NewResampler(16000, 48000)
	whole := append(single.Convert(in), single.Flush()...)

	piece := NewResampler(16000, 48000)
	first := piece.Convert(in[:100])
	second := piece.Convert(in[100:])
	second = append(second, piece.Flush()...)

	joined := append(append([]byte{}, first...), second...)
	if len(joined) != len(whole) {
		t.Fatalf("the chunked conversion produced %d bytes, want %d", len(joined), len(whole))
	}
	for i := range joined {
		if joined[i] != whole[i] {
			t.Fatalf("the chunked conversion differs at byte %d: %d against %d",
				i, joined[i], whole[i])
		}
	}
}

// TestTheResamplerPassesTheSameRateThrough checks that a conversion to
// the same rate does nothing.
//
// The media proxy is asked for the speaker's own rate, so this is the
// common case and it must not cost anything.
func TestTheResamplerPassesTheSameRateThrough(t *testing.T) {
	in := []byte{1, 2, 3, 4}
	out := NewResampler(48000, 48000).Convert(in)
	if len(out) != len(in) {
		t.Fatalf("the conversion produced %d bytes, want %d", len(out), len(in))
	}
}
