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

// The one format the device asks for. A single rate and channel count, so the
// server resamples for us: MA does it per client anyway, and a player that
// lists one format never has to switch output mid-stream.
//
// Stereo, because the speaker takes interleaved stereo frames and the ported
// speaker mixes the lanes itself. Advertising mono was a mistake: the player
// then writes each mono sample into BOTH halves of a frame, and a reader that
// takes the frame as interleaved stereo hears the same sample twice and plays
// it at double speed. One format, one channel count, and the decoder and the
// speaker agree on it.
//
// FLAC only. The spec requires a player to list flac or pcm; FLAC is ~40% of
// PCM's bitrate for 3.28% of a core (PR #271), on links measured at 4.6-7.1%
// loss. The audio-chunk header check in chunkPayload also leans on FLAC's
// sync code, which PCM does not have.
const (
	outRate     = 48000
	outChannels = 2
	outBits     = 16
)

// streamFormat is what a stream/start said the chunks are.
type streamFormat struct {
	Codec      string `json:"codec"`
	SampleRate int    `json:"sample_rate"`
	Channels   int    `json:"channels"`
	BitDepth   int    `json:"bit_depth"`
	Header     string `json:"codec_header,omitempty"`
}

// decoder turns one chunk of the current format into interleaved stereo S16
// at outRate.
type decoder struct {
	f streamFormat
	// opus is the Opus decoder, when the stream is Opus.
	//
	// It is state: an Opus frame is decoded against the one before it, so
	// the decoder is kept for the length of the stream.
	opus opus.Decoder
	// opusOut is where the Opus samples are decoded into. An Opus frame
	// is at most 120 ms: 5760 samples per channel at 48 kHz.
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
		// The decoder is made here, so a codec that cannot be decoded
		// is refused when the stream starts rather than when the first
		// chunk arrives.
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

// decode appends the chunk's samples to dst as interleaved stereo.
//
// A server that sends mono (which the device does not ask for, but a server
// may) is laid into both channels: one sample, one frame, the same in each
// ear, which is what a single enclosure wants.
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
	// One or more whole FLAC frames. The frames carry their own sample rate
	// and bit depth codes, so the stream header is not needed to decode them.
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

// chunkPayload splits an audio chunk (type byte already checked) into its
// server timestamp and codec payload.
//
// 9.1.1: the header is the type byte and a big-endian int64 timestamp — nine
// bytes. The current spec adds a uint32 send_ahead, making it thirteen. The
// two are told apart by the FLAC frame sync code (0xFFF8 fixed-blocksize,
// 0xFFF9 variable), which every chunk starts with: whichever offset it sits
// at is where the payload begins. A server that upgrades then keeps working.
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
