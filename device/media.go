package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/mewkiz/flac"
	"github.com/vithurshanselvarajah/Home-Panel-Kiosk/audio"
	"github.com/vithurshanselvarajah/Home-Panel-Kiosk/device/esphome"
)

const mediaFetchIdleTimeout = 30 * time.Second

const mediaBufferBytes = playbackRate * 2

const mediaHighWaterBytes = playbackRate * 4

func (a *deviceAudio) playMediaURL(ctx context.Context, url string, announcement bool) {
	if url == "" {
		return
	}

	a.log.Info("playing", "url", url, "announcement", announcement)

	stop := make(chan struct{})
	a.mu.Lock()
	a.media = nil
	a.mediaActive = true
	a.mediaPaused = false
	a.mediaStop = stop
	a.mu.Unlock()

	a.resumeMediaFetch()

	stream, rate, err := openMedia(ctx, url)
	if err != nil {
		a.log.Error("the audio could not be fetched", "error", err)
		a.mediaFinished(stop)
		a.setMediaState(esphome.MediaPlayerState_MEDIA_PLAYER_STATE_IDLE)
		return
	}
	defer stream.Close()

	a.setMediaState(esphome.MediaPlayerState_MEDIA_PLAYER_STATE_PLAYING)

	if a.onMusicStarted != nil {
		a.onMusicStarted("home-assistant")
	}
	a.streamMedia(stream, rate, stop)
	a.mediaFinished(stop)
	a.setMediaState(esphome.MediaPlayerState_MEDIA_PLAYER_STATE_IDLE)
}

func (a *deviceAudio) mediaFinished(stop chan struct{}) {
	a.mu.Lock()
	if a.mediaStop == stop {
		a.mediaStop = nil
	}
	a.mu.Unlock()
}

func (a *deviceAudio) streamMedia(stream io.Reader, rate int, stop <-chan struct{}) {
	resampler := audio.NewResampler(rate, playbackRate)

	buf := make([]byte, 32*1024)
	for {

		select {
		case <-stop:
			return
		case <-a.mediaResumed():
		}

		if !a.waitForMediaRoom(stop) {
			return
		}

		n, err := stream.Read(buf)
		if n > 0 {
			a.playMedia(resampler.Convert(buf[:n]))
		}
		if err != nil {
			if err != io.EOF {
				a.log.Error("the audio could not be read", "error", err)
			}
			break
		}
	}

	if tail := resampler.Flush(); len(tail) > 0 {
		a.playMedia(tail)
	}
}

func (a *deviceAudio) waitForMediaRoom(stop <-chan struct{}) bool {
	for {
		a.mu.Lock()
		queued := len(a.media)
		paused := a.mediaPaused
		playing := a.mediaPlaying
		a.mu.Unlock()

		if paused || !playing || queued < mediaHighWaterBytes {
			return true
		}

		select {
		case <-stop:
			return false
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func (a *deviceAudio) mediaResumed() <-chan struct{} {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.mediaResumedCh == nil {
		a.mediaResumedCh = make(chan struct{})
	}
	return a.mediaResumedCh
}

func (a *deviceAudio) pauseMediaFetch() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.mediaResumedCh == nil {
		a.mediaResumedCh = make(chan struct{})
		return
	}
	if !channelIsClosed(a.mediaResumedCh) {
		return
	}
	a.mediaResumedCh = make(chan struct{})
}

func (a *deviceAudio) resumeMediaFetch() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.mediaResumedCh == nil {
		a.mediaResumedCh = make(chan struct{})
	}
	select {
	case <-a.mediaResumedCh:
	default:
		close(a.mediaResumedCh)
	}
}

func channelIsClosed(ch chan struct{}) bool {
	if ch == nil {
		return false
	}
	select {
	case <-ch:
		return true
	default:
		return false
	}
}

func (a *deviceAudio) playMedia(pcm []byte) {
	a.mu.Lock()
	if !a.mediaActive {
		a.mu.Unlock()
		return
	}
	a.media = append(a.media, pcm...)
	ready := len(a.media) >= mediaBufferBytes
	if ready {
		a.mediaPlaying = true
	}
	a.mu.Unlock()

	if ready {
		a.ensureOutput()
	}
}

func resampleToPlayback(pcm []byte, rate int) []byte {
	if rate <= 0 || rate == playbackRate {
		return pcm
	}
	return audio.NewResampler(rate, playbackRate).Convert(pcm)
}

func (a *deviceAudio) streamVoice(stream io.Reader, rate int) {
	resampler := audio.NewResampler(rate, voiceRate)

	comp := &louder{}

	buf := make([]byte, 32*1024)
	for {
		n, err := stream.Read(buf)
		if n > 0 {
			a.playVoice(a.compressWith(resampler.Convert(buf[:n]), comp))
		}
		if err != nil {
			if err != io.EOF {
				a.log.Error("the audio could not be read", "error", err)
			}
			break
		}
	}

	if tail := resampler.Flush(); len(tail) > 0 {
		a.playVoice(a.compressWith(tail, comp))
	}
}

func mediaClient() *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			ResponseHeaderTimeout: mediaFetchIdleTimeout,
			IdleConnTimeout:       mediaFetchIdleTimeout,
			DisableCompression:    true,
		},
	}
}

func openMedia(ctx context.Context, url string) (io.ReadCloser, int, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, 0, err
	}

	response, err := mediaClient().Do(request)
	if err != nil {
		return nil, 0, err
	}
	if response.StatusCode != http.StatusOK {
		response.Body.Close()
		return nil, 0, fmt.Errorf("the audio came back as %s", response.Status)
	}

	head := make([]byte, 12)
	if _, err := io.ReadFull(response.Body, head); err != nil {
		response.Body.Close()
		return nil, 0, fmt.Errorf("the audio could not be read: %w", err)
	}

	if string(head[0:4]) != "RIFF" || string(head[8:12]) != "WAVE" {

		decoder, err := newMediaDecoder(head, response.Body)
		if err != nil {
			response.Body.Close()
			return nil, 0, err
		}
		return &mediaStream{reader: decoder, body: response.Body}, decoder.rate, nil
	}

	stream, rate, err := newWAVStream(response.Body)
	if err != nil {
		response.Body.Close()
		return nil, 0, err
	}
	return &mediaStream{reader: stream, body: response.Body}, rate, nil
}

type mediaStream struct {
	reader io.Reader
	body   io.ReadCloser
}

func (s *mediaStream) Read(p []byte) (int, error) {
	return s.reader.Read(p)
}

func (s *mediaStream) Close() error {
	return s.body.Close()
}

type wavStream struct {
	reader io.Reader

	channels int

	audio io.Reader

	stereo bool

	rest []byte
}

func newWAVStream(reader io.Reader) (*wavStream, int, error) {
	stream := &wavStream{reader: reader}

	head := make([]byte, 8)
	if _, err := io.ReadFull(reader, head); err != nil {
		return nil, 0, fmt.Errorf("the WAV header could not be read: %w", err)
	}

	rate := 0
	channels := 0
	for {
		id := string(head[0:4])
		size := int64(binary.LittleEndian.Uint32(head[4:8]))

		switch id {
		case "fmt ":
			body := make([]byte, 16)
			if _, err := io.ReadFull(reader, body); err != nil {
				return nil, 0, fmt.Errorf("the WAV format could not be read: %w", err)
			}
			format := binary.LittleEndian.Uint16(body[0:2])
			channels = int(binary.LittleEndian.Uint16(body[2:4]))
			rate = int(binary.LittleEndian.Uint32(body[4:8]))
			bits := int(binary.LittleEndian.Uint16(body[14:16]))
			if format != 1 {
				return nil, 0, fmt.Errorf("the WAV file is not PCM, it is format %d", format)
			}
			if bits != 16 {
				return nil, 0, fmt.Errorf("the WAV file is %d bits, which the speaker cannot play", bits)
			}

			if size > 16 {
				padded := size - 16
				if padded%2 == 1 {
					padded++
				}
				if _, err := io.CopyN(io.Discard, reader, padded); err != nil {
					return nil, 0, fmt.Errorf("the WAV format could not be read: %w", err)
				}
			}
		case "data":

			stream.channels = channels
			stream.stereo = channels > 1
			stream.audio = reader
			return stream, rate, nil
		default:

			if size == 0xFFFFFFFF {
				return nil, 0, fmt.Errorf("the WAV file has no data chunk")
			}
			padded := size
			if padded%2 == 1 {
				padded++
			}
			if _, err := io.CopyN(io.Discard, reader, padded); err != nil {
				return nil, 0, fmt.Errorf("the WAV header could not be read: %w", err)
			}
		}

		if _, err := io.ReadFull(reader, head); err != nil {
			return nil, 0, fmt.Errorf("the WAV header could not be read: %w", err)
		}
	}
}

func (s *wavStream) Read(p []byte) (int, error) {
	if !s.stereo {
		return s.audio.Read(p)
	}

	wanted := len(p)
	if wanted%2 != 0 {
		wanted++
	}
	buf := make([]byte, wanted*2)

	n, err := s.audio.Read(buf)
	if n > 0 {

		whole := n - n%4
		if whole > 0 {
			mixed := make([]byte, 0, whole/2)
			for i := 0; i+3 < whole; i += 4 {
				left := int32(int16(uint16(buf[i]) | uint16(buf[i+1])<<8))
				right := int32(int16(uint16(buf[i+2]) | uint16(buf[i+3])<<8))
				mixed = appendSample(mixed, (left+right)/2)
			}
			copied := copy(p, mixed)
			if copied < len(mixed) {
				s.rest = append(s.rest, mixed[copied:]...)
			}
			return copied, nil
		}
	}
	if err != nil {
		return 0, err
	}
	return 0, nil
}

func decodeMedia(body []byte) ([]byte, int, error) {
	if pcm, rate, channels, err := parseWAV(body); err == nil {
		return toMono(pcm, channels), rate, nil
	}
	if pcm, rate, err := decodeFLAC(body); err == nil {
		return pcm, rate, nil
	}

	return body, playbackRate, nil
}

func toMono(pcm []byte, channels int) []byte {
	if channels <= 1 {
		return pcm
	}

	out := make([]byte, 0, len(pcm)/channels)
	for i := 0; i+channels*2 <= len(pcm); i += channels * 2 {
		var sum int32
		for c := 0; c < channels; c++ {
			sum += int32(int16(uint16(pcm[i+c*2]) | uint16(pcm[i+c*2+1])<<8))
		}
		out = appendSample(out, sum/int32(channels))
	}
	return out
}

func parseWAV(body []byte) ([]byte, int, int, error) {
	if len(body) < 12 {
		return nil, 0, 0, fmt.Errorf("the audio is too short to be a WAV file")
	}
	if string(body[0:4]) != "RIFF" || string(body[8:12]) != "WAVE" {
		return nil, 0, 0, fmt.Errorf("the audio is not a WAV file")
	}

	pos := 12
	var rate, channels int
	for pos+8 <= len(body) {
		id := string(body[pos : pos+4])
		size := int(binary.LittleEndian.Uint32(body[pos+4 : pos+8]))
		body2 := pos + 8

		switch id {
		case "fmt ":
			if body2+16 > len(body) {
				return nil, 0, 0, fmt.Errorf("the WAV file's format chunk is truncated")
			}
			format := binary.LittleEndian.Uint16(body[body2 : body2+2])
			channels = int(binary.LittleEndian.Uint16(body[body2+2 : body2+4]))
			rate = int(binary.LittleEndian.Uint32(body[body2+4 : body2+8]))
			bits := int(binary.LittleEndian.Uint16(body[body2+14 : body2+16]))
			if format != 1 {
				return nil, 0, 0, fmt.Errorf("the WAV file is not PCM, it is format %d", format)
			}
			if bits != 16 {
				return nil, 0, 0, fmt.Errorf("the WAV file is %d bits, which the speaker cannot play", bits)
			}
		case "data":
			if body2 > len(body) {
				return nil, 0, 0, fmt.Errorf("the WAV file's data chunk is truncated")
			}
			end := body2 + size
			if end > len(body) {
				end = len(body)
			}
			return body[body2:end], rate, channels, nil
		}

		pos = body2 + size
		if size%2 == 1 {
			pos++
		}
	}
	return nil, 0, 0, fmt.Errorf("the WAV file has no data chunk")
}

func decodeFLAC(body []byte) ([]byte, int, error) {
	if len(body) < 4 || string(body[0:4]) != "fLaC" {
		return nil, 0, fmt.Errorf("the audio is not a FLAC stream")
	}

	stream, err := flac.New(bytes.NewReader(body))
	if err != nil {
		return nil, 0, err
	}

	out := make([]byte, 0, len(body))
	for {
		frame, err := stream.ParseNext()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, 0, err
		}

		out = append(out, flacFramePCM(frame)...)
	}
	return out, int(stream.Info.SampleRate), nil
}

func (a *deviceAudio) setMediaState(state esphome.MediaPlayerState) {
	a.mu.Lock()
	a.mediaState = state
	a.mu.Unlock()
	if a.onMediaState != nil {
		a.onMediaState(state)
	}
}

func appendSample(out []byte, v int32) []byte {
	if v > 32767 {
		v = 32767
	}
	if v < -32768 {
		v = -32768
	}
	return append(out, byte(v), byte(uint16(v)>>8))
}
