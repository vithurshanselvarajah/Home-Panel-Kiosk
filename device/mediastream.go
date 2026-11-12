package main

import (
	"bytes"
	"fmt"
	"io"

	"github.com/mewkiz/flac"
	"github.com/mewkiz/flac/frame"
)

// The streaming media decoder.
//
// Home Assistant's proxy hands the audio over in a format it has not
// always converted, and a radio station is a stream that never ends. So
// the audio has to be read as it arrives: a file that was read whole
// would never be read at all, and the panel would sit there saying it
// was playing and playing nothing.
//
// There are three kinds of audio the proxy hands over:
//
//   - A WAV file, which is walked a chunk at a time. Its header says
//     what the audio is.
//   - A FLAC stream, which is decoded a frame at a time.
//   - Raw PCM, which is the bytes as they are.
//
// The first twelve bytes say which it is.

// mediaKind is what kind of audio the proxy handed over.
type mediaKind int

const (
	// mediaWAV is a WAV file.
	mediaWAV mediaKind = iota
	// mediaFLAC is a FLAC stream.
	mediaFLAC
	// mediaPCM is raw samples.
	mediaPCM
)

// mediaDecoder reads the audio as it arrives.
//
// It is an io.Reader: the speaker takes what it can play and asks for
// more.
type mediaDecoder struct {
	kind mediaKind
	// body is what is left of the stream.
	body io.Reader
	// head is the first twelve bytes, which have already been read.
	head []byte
	// rate is the rate the audio is at.
	rate int
	// flac is the FLAC stream, when the audio is FLAC.
	flac *flac.Stream
	// pending is the audio that has been decoded but not yet taken.
	pending []byte
}

// newMediaDecoder makes a decoder for the audio that has arrived.
//
// The first twelve bytes have already been read, because they are what
// says what kind of audio it is.
func newMediaDecoder(head []byte, body io.Reader) (*mediaDecoder, error) {
	d := &mediaDecoder{head: head, body: body}
	switch {
	case string(head[0:4]) == "RIFF" && string(head[8:12]) == "WAVE":
		d.kind = mediaWAV
		d.rate = playbackRate
	case string(head[0:4]) == "fLaC":
		d.kind = mediaFLAC
		stream, err := flac.New(io.MultiReader(bytes.NewReader(head), body))
		if err != nil {
			return nil, err
		}
		d.flac = stream
		d.rate = int(stream.Info.SampleRate)
	default:
		// The proxy sends PCM when it has nothing to convert, and that
		// is the common case.
		d.kind = mediaPCM
		d.rate = playbackRate
	}
	return d, nil
}

// Read gives back the audio.
func (d *mediaDecoder) Read(p []byte) (int, error) {
	if len(d.pending) > 0 {
		n := copy(p, d.pending)
		d.pending = d.pending[n:]
		return n, nil
	}
	switch d.kind {
	case mediaPCM:
		// The bytes are the samples. The ones that were read to find
		// out what the audio is are the first of them.
		if len(d.head) > 0 {
			n := copy(p, d.head)
			d.head = d.head[n:]
			return n, nil
		}
		return d.body.Read(p)
	case mediaFLAC:
		frame, err := d.flac.ParseNext()
		if err != nil {
			return 0, err
		}
		d.pending = flacFramePCM(frame)
		if len(d.pending) == 0 {
			// A frame with no samples in it is not the end of the
			// stream, so the next one is tried.
			return d.Read(p)
		}
		return d.Read(p)
	default:
		return 0, fmt.Errorf("home-panel: the audio is a kind that cannot be streamed")
	}
}

// flacFramePCM turns a FLAC frame into samples.
//
// A stereo frame is averaged down to the one channel the speaker takes.
func flacFramePCM(frame *frame.Frame) []byte {
	if frame == nil || len(frame.Subframes) == 0 {
		return nil
	}
	samples := frame.Subframes[0].Samples
	if len(frame.Subframes) == 1 {
		out := make([]byte, 0, len(samples)*2)
		for _, s := range samples {
			out = appendSample(out, int32(s))
		}
		return out
	}
	right := frame.Subframes[1].Samples
	out := make([]byte, 0, len(samples)*2)
	for i, s := range samples {
		other := int32(0)
		if i < len(right) {
			other = int32(right[i])
		}
		out = appendSample(out, (int32(s)+other)/2)
	}
	return out
}
