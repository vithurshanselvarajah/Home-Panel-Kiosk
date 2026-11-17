package sendspin

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"

	"github.com/mewkiz/flac/frame"
	"github.com/pion/opus"
)

const (
	outRate     = 48000
	outChannels = 2
	outBits     = 16
)

type streamFormat struct {
	Codec      string `json:"codec"`
	SampleRate int    `json:"sample_rate"`
	Channels   int    `json:"channels"`
	BitDepth   int    `json:"bit_depth"`
	Header     string `json:"codec_header,omitempty"`
}

type decoder struct {
	f streamFormat

	opus opus.Decoder

	opusOut []int16
}

var errFormat = errors.New("sendspin: stream format the device did not ask for")

func newDecoder(f streamFormat) (*decoder, error) {
	if f.SampleRate != outRate || f.BitDepth != outBits || f.Channels < 1 || f.Channels > 2 {
		return nil, fmt.Errorf("%w: %s %dHz %d-bit %dch", errFormat, f.Codec, f.SampleRate, f.BitDepth, f.Channels)
	}
	switch f.Codec {
	case "flac":
		if f.Header != "" {
			h, err := base64.StdEncoding.DecodeString(f.Header)
			if err != nil || len(h) < 4 || string(h[:4]) != "fLaC" {
				return nil, errors.New("sendspin: flac codec_header is not a FLAC stream header")
			}
		}
	case "opus":

		dec, err := opus.NewDecoderWithOutput(outRate, outChannels)
		if err != nil {
			return nil, fmt.Errorf("%w: opus decoder: %w", errFormat, err)
		}
		return &decoder{f: f, opus: dec}, nil
	case "pcm":
	default:
		return nil, fmt.Errorf("%w: codec %q", errFormat, f.Codec)
	}
	return &decoder{f: f}, nil
}

func (d *decoder) decode(dst []int16, payload []byte) ([]int16, error) {
	if d.f.Codec == "opus" {
		if d.opusOut == nil {
			d.opusOut = make([]int16, 5760*outChannels)
		}
		n, err := d.opus.DecodeToInt16(payload, d.opusOut)
		if err != nil {
			return dst, fmt.Errorf("sendspin: opus decode: %w", err)
		}
		return append(dst, d.opusOut[:n*outChannels]...), nil
	}
	if d.f.Codec == "pcm" {
		n := len(payload) / 2 / d.f.Channels
		for i := 0; i < n; i++ {
			if d.f.Channels == 1 {
				v := int16(binary.LittleEndian.Uint16(payload[i*2:]))
				dst = append(dst, v, v)
				continue
			}
			dst = append(dst,
				int16(binary.LittleEndian.Uint16(payload[i*4:])),
				int16(binary.LittleEndian.Uint16(payload[i*4+2:])))
		}
		return dst, nil
	}

	r := bytes.NewReader(payload)
	for r.Len() > 0 {
		fr, err := frame.Parse(r)
		if err == io.EOF {
			break
		}
		if err != nil {
			return dst, err
		}
		if len(fr.Subframes) == 0 {
			continue
		}
		n := len(fr.Subframes[0].Samples)
		if len(fr.Subframes) == 1 {
			for _, s := range fr.Subframes[0].Samples[:n] {
				v := int16(s)
				dst = append(dst, v, v)
			}
			continue
		}
		l, rr := fr.Subframes[0].Samples, fr.Subframes[1].Samples
		for i := 0; i < n; i++ {
			dst = append(dst, int16(l[i]), int16(rr[i]))
		}
	}
	return dst, nil
}

func chunkPayload(msg []byte, codec string) (ts int64, payload []byte, err error) {
	if len(msg) < 9 {
		return 0, nil, errors.New("sendspin: audio chunk shorter than its header")
	}
	ts = int64(binary.BigEndian.Uint64(msg[1:9]))
	if codec == "flac" && !flacSync(msg[9:]) && len(msg) >= 13 && flacSync(msg[13:]) {
		return ts, msg[13:], nil
	}
	return ts, msg[9:], nil
}

func flacSync(b []byte) bool { return len(b) >= 2 && b[0] == 0xFF && b[1]&0xFE == 0xF8 }
