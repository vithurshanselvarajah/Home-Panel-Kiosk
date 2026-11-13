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

// The media player's audio.
//
// Home Assistant hands over a URL rather than the audio itself, and the
// audio is already at the speaker's format: it runs the source through
// its own proxy and converts it to the format the player said it takes.
//
// The proxy's output is a WAV file, header and all, because that is what
// ffmpeg writes to its output. The header is stripped here, because the
// speaker takes PCM and a speaker that is handed a RIFF header plays
// forty-odd bytes of it as a click.
//
// The one exception is FLAC, which Home Assistant's proxy may hand over
// when the source is already FLAC and the proxy is not in the way. It is
// decoded here, because a speaker cannot play FLAC.
//
// The audio is played as it arrives rather than after it has all
// arrived. A track is thirty megabytes and the network is slow, and a
// panel that waited for the whole thing before playing anything would
// take five minutes to say anything. The proxy streams, so the panel
// streams too.

// The time the fetch is allowed to make no progress.
//
// It is a timeout on the wait between chunks, not on the whole fetch. A
// track takes minutes to arrive on a slow network, and a limit on the
// whole fetch would fail every track that is longer than the limit. A
// limit on the wait is what catches a server that has stopped sending.
const mediaFetchIdleTimeout = 30 * time.Second

// mediaBufferBytes is how much music is kept queued ahead of the
// speaker.
//
// The speaker takes one period every ten milliseconds and the fetch
// takes as long as the network takes. Without a buffer the two race:
// the queue runs dry between the network's chunks, the speaker is given
// a period of silence, and the music becomes a tick rather than music.
//
// A second of music is enough to ride out a slow chunk without adding
// a second of delay before the first note.
const mediaBufferBytes = playbackRate * 2

// mediaHighWaterBytes is how much music is allowed to be queued.
//
// The fetch must not run away from the speaker: a track is thirty
// megabytes and it would all be held in memory, and the music would be
// played long after it was fetched. So the fetch waits once the queue
// is this far ahead.
//
// It is above the buffer on purpose: the queue is allowed to fill past
// the buffer while the track starts, so the music begins with something
// to play and does not stutter out of the gate.
const mediaHighWaterBytes = playbackRate * 4

// playMediaURL plays the audio at a URL.
//
// It runs on its own goroutine, because fetching a URL takes as long as
// the network takes and the audio path must not wait for it.
//
// The music goes into its own queue rather than the reply's, so a reply
// and the music do not take each other out of the speaker's hands, and
// so the music can be paused and stopped without touching the reply.
func (a *deviceAudio) playMediaURL(ctx context.Context, url string, announcement bool) {
	if url == "" {
		return
	}

	a.log.Info("playing", "url", url, "announcement", announcement)

	// The stop signal is made before the fetch starts, so a stop that
	// arrives while the fetch is being made is not missed.
	stop := make(chan struct{})
	a.mu.Lock()
	a.media = nil
	a.mediaActive = true
	a.mediaPaused = false
	a.mediaStop = stop
	a.mu.Unlock()
	// The fetch goes on to begin with, so the channel it waits on is
	// closed.
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
	// Home Assistant's music is playing, so the music server's music
	// gives way.
	if a.onMusicStarted != nil {
		a.onMusicStarted("home-assistant")
	}
	a.streamMedia(stream, rate, stop)
	a.mediaFinished(stop)
	a.setMediaState(esphome.MediaPlayerState_MEDIA_PLAYER_STATE_IDLE)
}

// mediaFinished says the fetch has finished.
//
// The audio that has arrived is left in the queue, because the speaker
// has not played it yet: the end of the fetch is not the end of the
// music. The track ends when the queue is empty, which the mixer knows.
func (a *deviceAudio) mediaFinished(stop chan struct{}) {
	a.mu.Lock()
	if a.mediaStop == stop {
		a.mediaStop = nil
	}
	a.mu.Unlock()
}

// streamMedia plays the audio as it arrives.
//
// The audio is read in blocks and queued, so the speaker starts playing
// the first of it while the rest is still on its way. A track is thirty
// megabytes and the network is slow, and a panel that waited for the
// whole thing before playing anything would take five minutes to say
// anything.
//
// The audio is converted to the rate the speaker plays at as it goes,
// because the proxy is asked for that rate but does not always have it.
//
// A paused track is not read from: the audio that has already arrived
// is played out and then the track waits for the play.
func (a *deviceAudio) streamMedia(stream io.Reader, rate int, stop <-chan struct{}) {
	resampler := audio.NewResampler(rate, playbackRate)

	buf := make([]byte, 32*1024)
	for {
		// A paused track waits. The stop is the way out of the wait.
		select {
		case <-stop:
			return
		case <-a.mediaResumed():
		}

		// The fetch waits until the speaker has caught up.
		//
		// The network is slower than the speaker and the fetch must not
		// run away from it: the queue would grow to the size of the
		// track, and the music would be played long after it was
		// fetched.
		//
		// It waits until the queue is a second of music ahead, so the
		// speaker always has something to play and the network has
		// room to catch up.
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

	// The tail of the audio is held by the converter, because there is
	// no next sample to interpolate towards.
	if tail := resampler.Flush(); len(tail) > 0 {
		a.playMedia(tail)
	}
}

// waitForMediaRoom waits until there is room in the queue for more.
//
// It says false when the track was stopped while it waited.
//
// The wait is only once the speaker is playing. Before that the queue
// is filling up and there is nothing to wait for: the speaker has not
// started, so the queue can grow as much as it likes.
func (a *deviceAudio) waitForMediaRoom(stop <-chan struct{}) bool {
	for {
		a.mu.Lock()
		queued := len(a.media)
		paused := a.mediaPaused
		playing := a.mediaPlaying
		a.mu.Unlock()

		// A paused track is not filling up, so there is no point in
		// waiting for room.
		if paused || !playing || queued < mediaHighWaterBytes {
			return true
		}

		// The queue is far enough ahead. The speaker catches up at ten
		// milliseconds a period, so the wait is short.
		select {
		case <-stop:
			return false
		case <-time.After(10 * time.Millisecond):
		}
	}
}

// mediaResumed is the channel the fetch waits on while the music is
// paused.
//
// It is closed when the music is played, which lets the fetch go on. It
// is made again when the music is paused, so the fetch waits. The fetch
// asks for it each time round its loop, so it always has the current
// one.
func (a *deviceAudio) mediaResumed() <-chan struct{} {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.mediaResumedCh == nil {
		a.mediaResumedCh = make(chan struct{})
	}
	return a.mediaResumedCh
}

// pauseMediaFetch stops the fetch from reading while the music is
// paused.
//
// The channel the fetch waits on is made open, so the fetch blocks on
// it, and the music that has already arrived stays where it is.
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

// resumeMediaFetch lets the fetch go on.
//
// The channel is closed, which is how the fetch is told.
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

// channelIsClosed says whether a channel has been closed.
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

// playMedia queues music for the speaker.
//
// The music has its own queue: a reply and the music are played at the
// same time, and they must not take each other out of the queue.
//
// The speaker is not opened until the queue has a buffer's worth of
// music in it. Opening it on the first chunk means the speaker takes
// the music as fast as the network delivers it, which is slower than
// the speaker plays, and the music arrives in bursts with gaps between
// them.
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

// resampleToPlayback converts audio to the rate the speaker plays at.
//
// The proxy is asked for the speaker's own rate, so this is usually a
// copy. It is not always: the proxy is not in the way when the source is
// already in a format it will hand over as it is, and then the audio is
// at whatever rate the source was at.
func resampleToPlayback(pcm []byte, rate int) []byte {
	if rate <= 0 || rate == playbackRate {
		return pcm
	}
	return audio.NewResampler(rate, playbackRate).Convert(pcm)
}

// streamVoice plays the audio as it arrives.
//
// The audio is read in blocks and queued, so the speaker starts playing
// the first of it while the rest is still on its way. A track is thirty
// megabytes and the network is slow, and a panel that waited for the
// whole thing before playing anything would take five minutes to say
// anything.
//
// The audio is converted to the rate the speaker plays at as it goes,
// because the proxy is asked for that rate but does not always have it.
func (a *deviceAudio) streamVoice(stream io.Reader, rate int) {
	resampler := audio.NewResampler(rate, playbackRate)

	buf := make([]byte, 32*1024)
	for {
		n, err := stream.Read(buf)
		if n > 0 {
			a.playVoice(resampler.Convert(buf[:n]))
		}
		if err != nil {
			if err != io.EOF {
				a.log.Error("the audio could not be read", "error", err)
			}
			break
		}
	}

	// The tail of the audio is held by the converter, because there is
	// no next sample to interpolate towards.
	if tail := resampler.Flush(); len(tail) > 0 {
		a.playVoice(tail)
	}
}

// fetchMedia gets the audio at a URL and turns it into PCM.
//
// The request is a plain one: Home Assistant's proxy is on the same
// network as the panel, and the URL is given to the panel in the clear
// anyway.
//
// What comes back is whatever the proxy made: a WAV file, or FLAC when
// the proxy was not in the way. Both are turned into the PCM the speaker
// takes, and the rate the audio was at comes back with it.
//
// The whole file is read before anything is played, so this is for the
// short pieces — the announcement and the test tone. A track is played
// as it arrives, through openMedia.
func fetchMedia(ctx context.Context, url string) ([]byte, int, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, 0, err
	}

	response, err := mediaClient().Do(request)
	if err != nil {
		return nil, 0, err
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		return nil, 0, fmt.Errorf("the audio came back as %s", response.Status)
	}

	body, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, 0, err
	}

	return decodeMedia(body)
}

// mediaClient is the client the media is fetched with.
//
// It has no limit on the whole request, because a track takes minutes to
// arrive on a slow network. The limit is on the wait between chunks, so
// a server that stops sending is still noticed.
func mediaClient() *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			ResponseHeaderTimeout: mediaFetchIdleTimeout,
			IdleConnTimeout:       mediaFetchIdleTimeout,
			DisableCompression:    true,
		},
	}
}

// openMedia starts fetching the audio at a URL.
//
// It gives back something that reads PCM, and the rate the audio is at.
// The audio is read as it arrives, so the speaker can play the first of
// it while the rest is still on its way.
//
// A WAV file is read as a stream, with its header taken off as it goes
// by. Anything else is read whole, because there is no way to know
// where the audio starts in it until there is all of it.
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

	// The header is small and arrives at once, so it is read here rather
	// than a chunk at a time. Whether it is a WAV file at all is not
	// known until the first twelve bytes have arrived.
	head := make([]byte, 12)
	if _, err := io.ReadFull(response.Body, head); err != nil {
		response.Body.Close()
		return nil, 0, fmt.Errorf("the audio could not be read: %w", err)
	}

	if string(head[0:4]) != "RIFF" || string(head[8:12]) != "WAVE" {
		// It is not a WAV file, so it is FLAC or raw samples. Both are
		// read as they arrive.
		//
		// They used to be read whole, which worked for a file and not
		// for a radio station: a station never ends, so the read never
		// finished and the panel sat there saying it was playing and
		// playing nothing.
		decoder, err := newMediaDecoder(head, response.Body)
		if err != nil {
			response.Body.Close()
			return nil, 0, err
		}
		return &mediaStream{reader: decoder, body: response.Body}, decoder.rate, nil
	}

	// It is a WAV file. The rest of its header is read, and then the
	// audio is read as it arrives.
	//
	// The twelve bytes of the RIFF header are not passed on: the chunk
	// walker starts at the first chunk, which is after them.
	stream, rate, err := newWAVStream(response.Body)
	if err != nil {
		response.Body.Close()
		return nil, 0, err
	}
	return &mediaStream{reader: stream, body: response.Body}, rate, nil
}

// mediaStream is the audio being fetched, with the body under it.
//
// The body has to be closed when the audio is done with, and the audio
// has to be closed when the body is, so the two are held together.
type mediaStream struct {
	reader io.Reader
	body   io.ReadCloser
}

// Read reads the next of the audio.
func (s *mediaStream) Read(p []byte) (int, error) {
	return s.reader.Read(p)
}

// Close closes the stream and the body under it.
func (s *mediaStream) Close() error {
	return s.body.Close()
}

// wavStream reads the audio out of a WAV file that is still arriving.
//
// The header is read first, and the audio is what is left after it. A
// WAV file that is being streamed has no length in its header — the
// sizes are all ones — so the audio is read until the file ends rather
// than until a count is reached.
type wavStream struct {
	reader io.Reader
	// channels is how many channels the file has.
	channels int
	// audio is the audio, once the header has been read.
	audio io.Reader
	// stereo says the audio has to be mixed down to one channel.
	stereo bool
	// rest is the tail of a partial sample, kept for the next read.
	rest []byte
}

// newWAVStream reads the header of a WAV file and gives back the audio.
//
// The first twelve bytes have already been read: the RIFF header.
func newWAVStream(reader io.Reader) (*wavStream, int, error) {
	stream := &wavStream{reader: reader}

	// The chunks are walked until the data chunk is found. The ones
	// before it are skipped, and there is often a LIST chunk carrying
	// the metadata that the encoder wrote.
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
			// The chunk is padded to an even length, and the part of it
			// that was not read is skipped.
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
			// The audio starts here. The size in the header is not
			// trusted: a file that is being streamed says its size is
			// all ones, and the audio runs until the file ends.
			stream.channels = channels
			stream.stereo = channels > 1
			stream.audio = reader
			return stream, rate, nil
		default:
			// A chunk that is not one of ours is skipped whole. A chunk
			// whose size is all ones has no end to skip to, so it is
			// the end of the header.
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

		// The next chunk's header follows.
		if _, err := io.ReadFull(reader, head); err != nil {
			return nil, 0, fmt.Errorf("the WAV header could not be read: %w", err)
		}
	}
}

// Read reads the next of the audio.
//
// A file with more than one channel is mixed down to one, because the
// speaker takes one channel.
func (s *wavStream) Read(p []byte) (int, error) {
	if !s.stereo {
		return s.audio.Read(p)
	}

	// Twice as many bytes are read as are wanted, because each frame
	// has two channels in it.
	wanted := len(p)
	if wanted%2 != 0 {
		wanted++
	}
	buf := make([]byte, wanted*2)

	n, err := s.audio.Read(buf)
	if n > 0 {
		// A frame that is not whole is kept for the next read.
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

// decodeMedia turns what came back from the proxy into PCM.
//
// A WAV file has its header stripped, because the speaker takes PCM and
// not a file. FLAC is decoded, because a speaker cannot play FLAC.
// Anything else is assumed to be PCM already, which is what the proxy
// sends when it has nothing to convert.
//
// The rate is the one the audio is at, so that it can be converted to
// the rate the speaker plays at.
func decodeMedia(body []byte) ([]byte, int, error) {
	if pcm, rate, channels, err := parseWAV(body); err == nil {
		return toMono(pcm, channels), rate, nil
	}
	if pcm, rate, err := decodeFLAC(body); err == nil {
		return pcm, rate, nil
	}
	// It is not a file the panel knows how to read, so it is played as
	// it is. The proxy sends PCM when it has nothing to convert, and
	// that is the common case.
	return body, playbackRate, nil
}

// toMono averages the channels of a stereo buffer down to one.
//
// The proxy is asked for one channel, so this is not the common case,
// but a file that arrives in stereo would otherwise be played with the
// channels alternating, which is a sound nobody has heard.
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

// parseWAV reads a WAV file and returns its audio.
//
// The audio is signed 16-bit little-endian PCM, and the file says what
// rate and how many channels it has. A file that says anything else is
// not one the speaker can play.
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

		// The chunks are padded to an even length.
		pos = body2 + size
		if size%2 == 1 {
			pos++
		}
	}
	return nil, 0, 0, fmt.Errorf("the WAV file has no data chunk")
}

// decodeFLAC decodes a FLAC stream into PCM.
//
// The stream is read a frame at a time, and each frame is correlated so
// that the channels are whole samples again.
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
		// The frame is turned into samples the same way whether it
		// came from a whole file or from a stream, so there is one
		// place that does it.
		out = append(out, flacFramePCM(frame)...)
	}
	return out, int(stream.Info.SampleRate), nil
}

// setMediaState records what the speaker is doing.
func (a *deviceAudio) setMediaState(state esphome.MediaPlayerState) {
	a.mu.Lock()
	a.mediaState = state
	a.mu.Unlock()
	if a.onMediaState != nil {
		a.onMediaState(state)
	}
}

// appendSample adds one sample to a buffer of little-endian bytes.
func appendSample(out []byte, v int32) []byte {
	if v > 32767 {
		v = 32767
	}
	if v < -32768 {
		v = -32768
	}
	return append(out, byte(v), byte(uint16(v)>>8))
}
