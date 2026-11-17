package sendspin

import "testing"

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
