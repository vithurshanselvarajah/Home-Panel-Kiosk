package main

import (
	"bytes"
	"fmt"
	"io"

	"github.com/mewkiz/flac"
	"github.com/mewkiz/flac/frame"
)

type mediaKind int

const (
	mediaWAV mediaKind = iota

	mediaFLAC

	mediaPCM
)

type mediaDecoder struct {
	kind mediaKind

	body io.Reader

	head []byte

	rate int

	flac *flac.Stream

	pending []byte
}

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

		d.kind = mediaPCM
		d.rate = playbackRate
	}
	return d, nil
}

func (d *mediaDecoder) Read(p []byte) (int, error) {
	if len(d.pending) > 0 {
		n := copy(p, d.pending)
		d.pending = d.pending[n:]
		return n, nil
	}
	switch d.kind {
	case mediaPCM:

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

			return d.Read(p)
		}
		return d.Read(p)
	default:
		return 0, fmt.Errorf("home-panel: the audio is a kind that cannot be streamed")
	}
}

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
