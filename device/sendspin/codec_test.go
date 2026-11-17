package sendspin

import "testing"

// The decoder must take the codecs the device offers.
//
// Opus is the fallback that always arrives at 48 kHz whatever the source
// was, and PCM is the plain one. A server that cannot send FLAC has
// somewhere to go.
func TestTheDecoderTakesEveryCodecOffered(t *testing.T) {
	for _, codec := range []string{"flac", "opus", "pcm"} {
		d, err := newDecoder(streamFormat{
			Codec: codec, SampleRate: outRate, Channels: outChannels, BitDepth: outBits,
		})
		if err != nil {
			t.Fatalf("the decoder refused %s: %v", codec, err)
		}
		if d == nil {
			t.Fatalf("the decoder for %s is nil", codec)
		}
	}
}

// The device must offer the codecs it can decode.
func TestTheOfferedCodecsAreTheOnesThatCanBeDecoded(t *testing.T) {
	for _, f := range offeredFormats() {
		_, err := newDecoder(streamFormat{
			Codec: f.Codec, SampleRate: f.SampleRate, Channels: f.Channels, BitDepth: f.BitDepth,
		})
		if err != nil {
			t.Fatalf("the device offers %s and cannot decode it: %v", f.Codec, err)
		}
	}
}
