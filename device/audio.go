package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/vithurshanselvarajah/Home-Panel-Kiosk/device/esphome"
	"github.com/vithurshanselvarajah/Home-Panel-Kiosk/device/sendspin"

	"github.com/vithurshanselvarajah/Home-Panel-Kiosk/audio"
	"github.com/vithurshanselvarajah/Home-Panel-Kiosk/audio/mic"
	"github.com/vithurshanselvarajah/Home-Panel-Kiosk/audio/speaker"
	vol "github.com/vithurshanselvarajah/Home-Panel-Kiosk/audio/volume"
)

type audioBackend interface {
	Capture(ctx context.Context, rate int) (io.ReadCloser, error)

	Playback(ctx context.Context, rate int) (io.WriteCloser, error)

	OpenClockedOutput(ctx context.Context, rate, channels int) (audio.ClockedOutput, error)

	SelectedAudioDevices() (string, string)

	SelectAudioDevices(input, output string) error

	ChooseDefaultInput() error
}

type deviceAudio struct {
	backend audioBackend
	log     *slog.Logger

	onSendMicAudio func(pcm []byte)

	onWakeAudio func(pcm []byte)

	onWakeReset func()

	onSendspinStatus func(status sendspin.Status)

	onSendspinStarted func()

	onVolume func(volume float32)

	onPanelsVolume func(level int)

	onReplyEnded func()

	onMediaState func(state esphome.MediaPlayerState)

	onMusicStarted func(source string)

	mediaState esphome.MediaPlayerState

	sendspinPlaying bool

	onAnnounceFinished func()

	credentialsDir string

	mu sync.Mutex

	capture io.ReadCloser

	captureCancel context.CancelFunc

	capturing bool

	monitoring bool

	monitorCancel context.CancelFunc

	monitorDone chan struct{}

	testSound int

	testSoundSeen bool

	listening bool

	streaming bool

	output io.WriteCloser

	outputCancel context.CancelFunc

	playing bool

	outputGen int

	clocked audio.ClockedOutput

	clockedChannels int

	voice []byte

	test []byte

	voiceActive bool

	voiceStreaming bool

	voiceMixed bool

	voiceAnnouncement bool

	voiceEnded func()

	voiceStarted func()

	spokenTotal int64

	spokenComplete bool

	voiceResampler *audio.Resampler

	voiceLouder *louder

	media []byte

	mediaPaused bool

	mediaActive bool

	mediaPlaying bool

	mediaStop chan struct{}

	mediaResumedCh chan struct{}

	volume int

	level int

	muted bool

	ducked bool

	duckDB float64

	wakeThresholdValue float64

	wakeWordEnabled bool

	sendspin *sendspin.Client

	sendspinName string

	sendspinUnpaired bool

	sendspinMuted bool

	sendspinStatus sendspin.Status

	sendspinPump context.CancelFunc

	inputs  []audio.Endpoint
	outputs []audio.Endpoint

	speakerCancel context.CancelFunc

	speakerDone chan struct{}

	micBridge *micBridge

	speakerPlayer *speaker.Player

	speakerStarting *startingSpeaker

	musicSource *musicSource

	mute *muteController
}

const (
	captureRate  = audio.CaptureRate
	playbackRate = audio.PlaybackRate
)

const voiceRate = 16000

const periodBytes = playbackRate / 100 * 2

func (a *deviceAudio) setEndpoints(inputs, outputs []audio.Endpoint) {
	a.mu.Lock()
	a.inputs = inputs
	a.outputs = outputs
	a.mu.Unlock()
}

func (a *deviceAudio) fillState(s *State) {
	a.mu.Lock()
	defer a.mu.Unlock()
	s.AudioInputs = append(s.AudioInputs, a.inputs...)
	s.AudioOutputs = append(s.AudioOutputs, a.outputs...)
	s.SelectedAudioInput, s.SelectedAudioOutput = a.backend.SelectedAudioDevices()

	if a.level > s.Level {
		s.Level = a.level
	}
	s.Monitoring = a.monitoring

	if a.mute != nil {
		s.Muted = a.mute.Get()
	} else {
		s.Muted = a.muted
	}

	music := a.sendspinStatus
	s.MusicState = music.State
	s.MusicPaired = music.PairedWith
	s.MusicBuffered = music.BufferedMs
	s.MusicSynced = music.Synced
	s.MusicTitle = music.Metadata.Title
	s.MusicArtist = music.Metadata.Artist
	s.MusicAlbum = music.Metadata.Album
	s.MusicArtwork = music.Metadata.ArtworkURL
	s.MusicPosition = music.Metadata.ProgressMs
	s.MusicDuration = music.Metadata.DurationMs

	s.MusicPlaying = music.Metadata.Speed >= 1000
	s.MusicTransport = music.Transport

	s.MediaState = mediaStateName(a.mediaState)

	s.Volume = a.volume
}

func (a *deviceAudio) selectInput(name string) error {
	if name == "" {
		if err := a.backend.ChooseDefaultInput(); err != nil {
			return err
		}
		return nil
	}
	return a.backend.SelectAudioDevices(name, a.currentOutput())
}

func (a *deviceAudio) selectOutput(name string) error {
	if name == "" {
		name = audio.DeviceDefault
	}
	return a.backend.SelectAudioDevices(a.currentInput(), name)
}

func (a *deviceAudio) currentInput() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	in, _ := a.backend.SelectedAudioDevices()
	return in
}

func (a *deviceAudio) currentOutput() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	_, out := a.backend.SelectedAudioDevices()
	return out
}

func (a *deviceAudio) playTestSound() error {

	const frequency = 440.0
	samples := playbackRate * 2
	tone := make([]byte, samples*2)
	for i := 0; i < samples; i++ {

		envelope := 1.0
		fade := samples / 20
		if i < fade {
			envelope = float64(i) / float64(fade)
		} else if i > samples-fade {
			envelope = float64(samples-i) / float64(fade)
		}
		sample := int16(12000 * envelope * math.Sin(2*math.Pi*frequency*float64(i)/playbackRate))
		tone[i*2] = byte(sample)
		tone[i*2+1] = byte(uint16(sample) >> 8)
	}

	a.mu.Lock()
	a.test = tone
	a.mu.Unlock()

	a.ensureOutput()
	return nil
}

func (a *deviceAudio) monitoringMic() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.monitoring && !a.capturing
}

func (a *deviceAudio) setMonitoring(on bool) {
	a.mu.Lock()
	a.monitoring = on
	alreadyCapturing := a.capturing
	a.mu.Unlock()

	if on && !alreadyCapturing {
		a.startMonitor()
		return
	}
	if !on {
		a.stopMonitor()
	}
}

func (a *deviceAudio) stopMonitor() {
	a.mu.Lock()
	cancel := a.monitorCancel
	done := a.monitorDone
	a.monitorCancel = nil
	a.monitorDone = nil
	a.mu.Unlock()
	if cancel != nil {
		cancel()
	}

	if done != nil {
		<-done
	}
}

func (a *deviceAudio) startMonitor() {
	a.mu.Lock()
	if a.monitorCancel != nil || a.capturing {
		a.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	a.monitorCancel = cancel

	finished := make(chan struct{})
	a.monitorDone = finished
	a.mu.Unlock()

	bridge, err := a.micBridgeFor(ctx)
	if err != nil {
		a.log.Warn("the microphone could not be opened for the level meter", "error", err)
		a.mu.Lock()
		a.monitorCancel = nil
		a.monitorDone = nil
		a.mu.Unlock()
		cancel()
		close(finished)
		return
	}
	level := bridge.src

	go func() {
		defer func() {

			a.mu.Lock()
			a.monitorCancel = nil
			a.monitorDone = nil
			a.mu.Unlock()
			cancel()

			close(finished)
		}()

		for {
			select {
			case <-ctx.Done():
				return
			case <-time.After(100 * time.Millisecond):
			}
			a.setLevel(roomLevel(level.Level()))
		}
	}()
}

func roomLevel(level float64) int {
	if level <= 0 {
		return 0
	}
	if level >= 1 {
		return 100
	}
	return int(level * 100)
}

func (a *deviceAudio) setTestSound(counter int) {
	a.mu.Lock()
	first := !a.testSoundSeen
	changed := counter != a.testSound && !first
	a.testSound = counter
	a.testSoundSeen = true
	a.mu.Unlock()
	if !changed {
		return
	}
	go func() {
		if err := a.playTestSound(); err != nil {
			a.log.Warn("the test sound could not be played", "error", err)
			return
		}
		a.log.Info("the test sound was played")
	}()
}

func (a *deviceAudio) setVolume(level int) {
	level = clampVolume(level)

	a.mu.Lock()
	a.volume = level
	player := a.sendspin
	a.mu.Unlock()

	if player != nil {

		player.SetVolume(level)
	}

	if spk := a.portedSpeaker(); spk != nil {
		spk.SetVolume(speakerStep(level))
	}

	if a.onVolume != nil {
		a.onVolume(float32(level) / 100)
	}

	if a.onPanelsVolume != nil {
		a.onPanelsVolume(level)
	}
}

func (a *deviceAudio) sendspinPlayer() *sendspin.Client {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.sendspin
}

func mediaStateName(state esphome.MediaPlayerState) string {
	switch state {
	case esphome.MediaPlayerState_MEDIA_PLAYER_STATE_PLAYING:
		return "playing"
	case esphome.MediaPlayerState_MEDIA_PLAYER_STATE_PAUSED:
		return "paused"
	case esphome.MediaPlayerState_MEDIA_PLAYER_STATE_ANNOUNCING:
		return "announcing"
	default:
		return "idle"
	}
}

func (a *deviceAudio) wakeThreshold() float64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.wakeThresholdValue
}

func (a *deviceAudio) wakeEnabled() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.wakeWordEnabled
}

func (a *deviceAudio) duckLevel() float64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.duckDB
}

func (a *deviceAudio) speaking() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.voiceActive
}

func (a *deviceAudio) setDuckDb(db float64) {
	a.mu.Lock()
	a.duckDB = db
	a.mu.Unlock()
}

func (a *deviceAudio) setWakeThreshold(threshold float64) {
	a.mu.Lock()
	a.wakeThresholdValue = threshold
	a.mu.Unlock()
}

func (a *deviceAudio) setWakeEnabled(enabled bool) {
	a.mu.Lock()
	a.wakeWordEnabled = enabled
	a.mu.Unlock()
}

func (a *deviceAudio) setMuted(muted bool) {
	if a.mute != nil {
		if err := a.mute.Set(muted); err != nil {
			a.log.Warn("the microphone could not be muted", "error", err)
		}
		return
	}
	a.mu.Lock()
	a.muted = muted
	a.mu.Unlock()
}

func (a *deviceAudio) stepVolume(delta int) {
	a.mu.Lock()
	level := a.volume
	a.mu.Unlock()

	step := vol.Step(level) + delta
	a.setVolume(vol.Percent(step))
}

func (a *deviceAudio) setDucked(on bool) {
	a.mu.Lock()
	a.ducked = on
	a.mu.Unlock()
}

func clampVolume(level int) int {
	if level < 0 {
		return 0
	}
	if level > 100 {
		return 100
	}
	return level
}

func (a *deviceAudio) startCapture() {
	a.mu.Lock()
	if a.capturing {
		a.mu.Unlock()
		return
	}

	if a.monitorCancel != nil {
		a.mu.Unlock()
		a.stopMonitor()
		a.mu.Lock()
	}
	a.capturing = true
	a.streaming = false
	ctx, cancel := context.WithCancel(context.Background())
	a.captureCancel = cancel
	a.mu.Unlock()

	if a.onWakeReset != nil {
		a.onWakeReset()
	}

	a.log.Info("the microphone is opening")

	reader, err := a.openMicrophone(ctx)
	if err != nil {
		a.log.Error("the microphone could not be opened", "error", err)
		a.mu.Lock()
		a.capturing = false
		a.captureCancel = nil
		a.mu.Unlock()
		cancel()
		return
	}
	a.mu.Lock()
	a.capture = reader
	a.mu.Unlock()

	go func() {

		restart := false
		defer func() {
			a.mu.Lock()
			a.capturing = false
			a.captureCancel = nil
			a.capture = nil
			wantMonitor := a.monitoring
			a.mu.Unlock()
			cancel()

			if wantMonitor {
				a.startMonitor()
			}

			if restart {
				a.restartCapture()
			}
		}()

		defer reader.Close()

		buf := make([]byte, captureRate/10*2)
		for {
			n, err := reader.Read(buf)
			if n > 0 {

				a.setLevel(peakLevel(buf[:n]))
				a.pushFrame(buf[:n])
			}
			if err != nil {
				if ctx.Err() == nil {
					a.log.Warn("the microphone stopped", "error", err)
					restart = true
				}
				return
			}
		}
	}()
}

func (a *deviceAudio) openMicrophone(ctx context.Context) (io.ReadCloser, error) {
	bridge, err := a.micBridgeFor(ctx)
	if err != nil {
		return nil, err
	}
	frames, stop := bridge.frames("device")
	return &micReader{bridge: bridge, frames: frames, stop: stop}, nil
}

func (a *deviceAudio) micBridgeFor(ctx context.Context) (*micBridge, error) {
	a.mu.Lock()
	bridge := a.micBridge
	a.mu.Unlock()
	if bridge != nil {
		return bridge, nil
	}

	if err := a.useExternalCapture(ctx); err != nil {
		return nil, err
	}

	bridge = &micBridge{src: mic.Get()}
	if err := bridge.start(ctx); err != nil {
		return nil, err
	}
	a.mu.Lock()
	a.micBridge = bridge
	a.mu.Unlock()
	return bridge, nil
}

type micReader struct {
	bridge *micBridge
	frames <-chan []int16
	stop   func()

	closed atomic.Bool
}

func (m *micReader) Read(p []byte) (int, error) {
	if m.closed.Load() {
		return 0, io.EOF
	}
	for {
		frame, ok := <-m.frames
		if !ok {
			return 0, io.EOF
		}
		if len(frame) == 0 {
			continue
		}
		copied := 0
		for _, v := range frame {
			if copied+2 > len(p) {
				break
			}
			p[copied] = byte(v)
			p[copied+1] = byte(uint16(v) >> 8)
			copied += 2
		}
		if copied > 0 {
			return copied, nil
		}
	}
}

func (m *micReader) Close() error {
	m.closed.Store(true)
	if m.stop != nil {
		m.stop()
	}
	return nil
}

func (a *deviceAudio) restartCapture() {
	a.mu.Lock()
	wanted := a.listening || a.monitoring || a.streaming
	a.mu.Unlock()

	if !wanted {
		return
	}

	a.log.Info("the microphone is opening again")
	a.startCapture()
}

func (a *deviceAudio) setLevel(level int) {
	a.mu.Lock()
	a.level = level
	a.mu.Unlock()
}

func peakLevel(pcm []byte) int {
	peak := 0
	for i := 0; i+1 < len(pcm); i += 2 {
		v := int16(uint16(pcm[i]) | uint16(pcm[i+1])<<8)
		if v < 0 {
			v = -v
		}
		if int(v) > peak {
			peak = int(v)
		}
	}
	if peak == 0 {
		return 0
	}
	decibels := 20 * math.Log10(float64(peak)/32767)
	level := int((decibels + 60) * 100 / 60)
	if level < 0 {
		return 0
	}
	if level > 100 {
		return 100
	}
	return level
}

func (a *deviceAudio) stopCapture() {
	a.mu.Lock()
	cancel := a.captureCancel
	reader := a.capture
	a.captureCancel = nil
	a.capture = nil
	a.capturing = false
	a.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	if reader != nil {
		_ = reader.Close()
	}
}

func (a *deviceAudio) pushFrame(pcm []byte) {
	a.mu.Lock()
	streaming := a.streaming
	muted := a.muted
	a.mu.Unlock()

	if a.mute != nil {
		muted = a.mute.Get()
	}

	if muted {
		pcm = make([]byte, len(pcm))
	}

	if a.onWakeAudio != nil {
		a.onWakeAudio(pcm)
	}

	if streaming && a.onSendMicAudio != nil {
		a.onSendMicAudio(pcm)
	}
}

func (a *deviceAudio) setListening(listening bool) {
	a.mu.Lock()
	a.listening = listening
	alreadyCapturing := a.capturing
	a.mu.Unlock()

	if listening {

		a.startCapture()
		return
	}
	if !alreadyCapturing {
		return
	}

	a.maybeStopCapture()
}

func (a *deviceAudio) maybeStopCapture() {
	a.mu.Lock()
	wanted := a.listening || a.monitoring || a.streaming
	a.mu.Unlock()
	if !wanted {
		a.stopCapture()
	}
}

func (a *deviceAudio) setStreaming(streaming bool) {
	a.mu.Lock()
	a.streaming = streaming
	a.mu.Unlock()

	if streaming {
		if turn, ok := a.backend.(interface{ TurnStarted() }); ok {
			turn.TurnStarted()
		}
	}
}

func (a *deviceAudio) sendMicAudio(pcm []byte) {
	if a.onSendMicAudio != nil {
		a.onSendMicAudio(pcm)
	}
}

func (a *deviceAudio) announce(mediaID, text string) {
	a.log.Info("the panel was asked to speak", "text", text, "media", mediaID)

	if mediaID == "" {

		if a.onAnnounceFinished != nil {
			a.onAnnounceFinished()
		}
		return
	}

	a.mu.Lock()
	a.voiceAnnouncement = true
	a.voiceActive = true
	a.voiceStreaming = true
	a.mu.Unlock()

	a.ensureOutput()

	go a.playAnnouncement(mediaID)
}

func (a *deviceAudio) playAnnouncement(mediaID string) {
	stream, rate, err := openMedia(context.Background(), mediaID)
	if err != nil {
		a.log.Error("the announcement could not be fetched", "error", err)
		a.mu.Lock()
		a.voiceStreaming = false
		a.voiceActive = false
		a.mu.Unlock()
		a.endVoice()
		return
	}
	defer stream.Close()

	a.streamVoice(stream, rate)
	a.finishVoice()
}

func (a *deviceAudio) endVoice() {
	a.mu.Lock()
	ended := a.voiceEnded
	announcement := a.voiceAnnouncement
	a.voiceAnnouncement = false
	a.mu.Unlock()

	if ended != nil {
		ended()
	}
	if announcement && a.onAnnounceFinished != nil {
		a.onAnnounceFinished()
	}
}

func (a *deviceAudio) playReply(pcm []byte, final bool) {
	if len(pcm) > 0 {
		a.setMediaState(esphome.MediaPlayerState_MEDIA_PLAYER_STATE_PLAYING)

		a.mu.Lock()
		started := a.voiceStarted
		first := !a.voiceActive
		a.mu.Unlock()
		if first && started != nil {
			started()
		}
	}

	a.setDucked(true)

	converted := a.resampleVoice(pcm)

	converted = a.compressVoice(converted)

	a.mu.Lock()
	a.spokenTotal += int64(len(converted))
	a.mu.Unlock()

	a.playVoice(converted)
	if final {
		a.finishVoice()
		a.setMediaState(esphome.MediaPlayerState_MEDIA_PLAYER_STATE_IDLE)
	}
}

func (a *deviceAudio) resampleVoice(pcm []byte) []byte {
	if len(pcm) == 0 {
		return nil
	}

	a.mu.Lock()
	if a.voiceResampler == nil {
		a.voiceResampler = audio.NewResampler(voiceRate, voiceRate)

		a.spokenTotal = 0

		a.spokenComplete = false
	}
	resampler := a.voiceResampler
	a.mu.Unlock()

	return resampler.Convert(pcm)
}

func (a *deviceAudio) compressVoice(pcm []byte) []byte {
	if len(pcm) == 0 {
		return pcm
	}
	if !louderSpeechOn.Load() {
		a.mu.Lock()
		a.voiceLouder = nil
		a.mu.Unlock()
		return pcm
	}

	a.mu.Lock()
	if a.voiceLouder == nil {
		a.voiceLouder = &louder{}
	}
	comp := a.voiceLouder
	a.mu.Unlock()

	return a.compressWith(pcm, comp)
}

func (a *deviceAudio) compressWith(pcm []byte, comp *louder) []byte {
	if len(pcm) == 0 || !louderSpeechOn.Load() {
		return pcm
	}
	out := comp.process(monoSamples16(pcm))
	converted := make([]byte, len(out)*2)
	for i, s := range out {
		converted[i*2] = byte(uint16(s))
		converted[i*2+1] = byte(uint16(s) >> 8)
	}
	return converted
}

func (a *deviceAudio) flushVoice() []byte {
	a.mu.Lock()
	resampler := a.voiceResampler
	a.mu.Unlock()

	if resampler == nil {
		return nil
	}
	return resampler.Flush()
}

func (a *deviceAudio) playVoice(pcm []byte) {
	a.mu.Lock()
	a.voice = append(a.voice, pcm...)
	a.voiceActive = true
	a.voiceStreaming = true
	a.voiceMixed = a.sendspin != nil && a.sendspin.Active()
	a.mu.Unlock()

	a.ensureOutput()
}

func (a *deviceAudio) spokenProgress() float64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.spokenComplete {

		return 1
	}
	if a.spokenTotal <= 0 {
		return 0
	}
	played := a.spokenTotal - int64(len(a.voice))
	if played < 0 {
		played = 0
	}
	progress := float64(played) / float64(a.spokenTotal)
	if progress > 1 {
		return 1
	}
	return progress
}

func (a *deviceAudio) PauseMedia() {
	a.mu.Lock()
	if !a.mediaActive || a.mediaPaused {
		a.mu.Unlock()
		return
	}
	a.mediaPaused = true
	a.mu.Unlock()

	a.pauseMediaFetch()

	a.log.Info("the music was paused")
	a.setMediaState(esphome.MediaPlayerState_MEDIA_PLAYER_STATE_PAUSED)
}

func (a *deviceAudio) ResumeMedia() {
	a.mu.Lock()
	if !a.mediaActive || !a.mediaPaused {
		a.mu.Unlock()
		return
	}
	a.mediaPaused = false
	a.mu.Unlock()

	a.resumeMediaFetch()

	a.log.Info("the music was played again")
	a.setMediaState(esphome.MediaPlayerState_MEDIA_PLAYER_STATE_PLAYING)
	a.ensureOutput()
}

func (a *deviceAudio) StopMedia() {
	a.mu.Lock()
	if !a.mediaActive {
		a.mu.Unlock()
		return
	}
	a.mediaActive = false
	a.mediaPaused = false
	a.media = nil
	stop := a.mediaStop
	a.mediaStop = nil
	a.mu.Unlock()

	if stop != nil {
		close(stop)
	}

	a.log.Info("the music was stopped")
	a.setMediaState(esphome.MediaPlayerState_MEDIA_PLAYER_STATE_IDLE)
}

func (a *deviceAudio) finishVoice() {
	a.mu.Lock()
	a.voiceStreaming = false

	empty := len(a.voice) == 0
	over := a.voiceActive && empty
	if over {
		a.voiceActive = false

		a.voiceResampler = nil

		a.voiceLouder = nil
	}
	a.mu.Unlock()
	if over {
		a.finishPlayback()

		a.setDucked(false)
		a.endVoice()
	}
}

func (a *deviceAudio) finishReply() {

	if tail := a.flushVoice(); len(tail) > 0 {
		a.mu.Lock()
		a.voice = append(a.voice, tail...)
		a.voiceActive = true

		a.spokenTotal += int64(len(tail))
		a.mu.Unlock()
		a.ensureOutput()
	}
	a.finishVoice()
}

func (a *deviceAudio) flush() {
	a.mu.Lock()
	a.voice = nil
	a.voiceActive = false
	a.voiceStreaming = false
	a.voiceMixed = false
	a.voiceResampler = nil
	a.voiceLouder = nil
	a.mu.Unlock()
	a.closeOutput()
	a.endVoice()
}

func (a *deviceAudio) markReplyPlayedOut() {
	a.mu.Lock()
	a.voiceActive = false
	a.voiceResampler = nil
	a.voiceLouder = nil

	a.spokenComplete = true
	a.mu.Unlock()

	a.finishPlayback()

	a.setDucked(false)
	a.setMediaState(esphome.MediaPlayerState_MEDIA_PLAYER_STATE_IDLE)
	a.endVoice()

	if a.onReplyEnded != nil {
		a.onReplyEnded()
	}
}

func (a *deviceAudio) ensureOutput() {
	a.mu.Lock()
	if a.playing {
		a.mu.Unlock()
		return
	}
	a.playing = true
	a.outputGen++
	generation := a.outputGen
	ctx, cancel := context.WithCancel(context.Background())
	a.outputCancel = cancel
	a.mu.Unlock()

	go a.outputLoop(ctx, generation)
}

func (a *deviceAudio) releaseOutput(generation int) {
	a.mu.Lock()
	if a.outputGen == generation {
		a.playing = false
		a.outputCancel = nil
		a.output = nil
	}
	a.mu.Unlock()
}

func (a *deviceAudio) closeOutput() {
	a.mu.Lock()
	cancel := a.outputCancel
	writer := a.output
	a.outputCancel = nil
	a.output = nil
	a.playing = false
	a.outputGen++
	a.mu.Unlock()

	if cancel != nil {
		cancel()
	}

	a.stopPortedSpeaker()
	if writer != nil {
		_ = writer.Close()
	}
}

func (a *deviceAudio) outputLoop(ctx context.Context, generation int) {

	if err := a.useExternalPlayback(ctx); err != nil {
		a.log.Warn("the speaker's playback could not be prepared", "error", err)
	}
	spk := a.portedSpeaker()
	if spk == nil {
		if err := a.startPortedSpeaker(ctx); err != nil {
			a.log.Error("the speaker could not be opened", "error", err)
			a.releaseOutput(generation)
			return
		}
		spk = a.portedSpeaker()
	}
	if spk == nil {
		a.releaseOutput(generation)
		return
	}

	var clocked audio.ClockedOutput
	channels := 2

	var writer io.WriteCloser = a.externalWriter()

	a.mu.Lock()

	replaced := a.outputGen != generation
	if !replaced {
		a.output = writer
		if clocked != nil {
			a.clocked = clocked
			a.clockedChannels = channels
		}
	}
	a.mu.Unlock()
	if replaced {
		if clocked != nil {
			_ = clocked.Close()
		} else {
			_ = writer.Close()
		}
		return
	}

	defer func() {

		a.stopPortedSpeaker()
		if clocked != nil {
			_ = clocked.Close()
		} else if writer != nil {
			_ = writer.Close()
		}
		a.releaseOutput(generation)
	}()

	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}

		a.mu.Lock()
		player := a.sendspin
		volume := a.volume

		muted := a.sendspinMuted
		ducked := a.ducked
		duckDB := a.duckDB

		voice := a.takeAudio(&a.voice, periodBytes)
		test := a.takeAudio(&a.test, periodBytes)

		var media []byte
		if !a.mediaPaused {
			media = a.takeAudio(&a.media, periodBytes)

			if len(media) == 0 && a.mediaActive && a.mediaStop == nil {
				a.mediaActive = false
				a.mediaPlaying = false
			}
		}
		a.mu.Unlock()

		spk := a.portedSpeaker()
		if spk != nil {

			if len(voice) > 0 {
				spk.PlayVoice(monoSamples16(voice))
			}
			if len(test) > 0 {
				spk.Play(monoSamples(test))
			}
			if len(media) > 0 {

				spk.Bed().Play(monoSamples(media))
			}
		}

		_ = muted
		_ = ducked
		_ = duckDB
		_ = volume

		a.mu.Lock()

		hasMedia := len(a.media) > 0 || a.mediaActive
		empty := len(a.voice) == 0 && len(a.test) == 0 && !hasMedia &&
			(player == nil || !musicConnected(player))

		replyDone := a.voiceActive && !a.voiceStreaming && len(a.voice) == 0
		a.mu.Unlock()
		if replyDone {
			a.markReplyPlayedOut()
		}
		if empty {

			if drainer, ok := writer.(interface{ Drain() error }); ok {
				_ = drainer.Drain()
			}
			return
		}
	}
}

func musicConnected(player *sendspin.Client) bool {
	switch player.Status().State {
	case "connected", "playing":
		return true
	default:
		return false
	}
}

func (a *deviceAudio) takeAudio(queue *[]byte, period int) []byte {
	if len(*queue) == 0 {
		return nil
	}
	taken := period
	if taken > len(*queue) {
		taken = len(*queue)
	}
	out := (*queue)[:taken]
	*queue = (*queue)[taken:]
	return out
}

func applySendspinGain(pcm []byte, volume int, muted, ducked bool, duckDB float64) {
	duckGain := 1.0
	if ducked {
		duckGain = math.Pow(10, duckDB/20)
	}
	gain := float64(vol.GainForPercent(volume)) * duckGain
	for i := 0; i+1 < len(pcm); i += 2 {
		v := float64(int16(uint16(pcm[i]) | uint16(pcm[i+1])<<8))
		if muted {
			v = 0
		} else {
			v = v * gain
		}
		if v > 32767 {
			v = 32767
		}
		if v < -32768 {
			v = -32768
		}
		sample := int16(v)
		pcm[i], pcm[i+1] = byte(sample), byte(uint16(sample)>>8)
	}
}

func applyDuck(pcm []byte, ducked bool, duckDB float64) {
	if !ducked || duckDB == 0 {
		return
	}
	gain := math.Pow(10, duckDB/20)
	for i := 0; i+1 < len(pcm); i += 2 {
		v := float64(int16(uint16(pcm[i]) | uint16(pcm[i+1])<<8))
		v *= gain
		if v > 32767 {
			v = 32767
		}
		if v < -32768 {
			v = -32768
		}
		sample := int16(v)
		pcm[i], pcm[i+1] = byte(sample), byte(uint16(sample)>>8)
	}
}

func (a *deviceAudio) finishPlayback() {
	a.mu.Lock()
	writer := a.output
	a.mu.Unlock()

	if writer != nil {
		if drainer, ok := writer.(interface{ Drain() error }); ok {
			if err := drainer.Drain(); err != nil {
				a.log.Debug("the speaker drain failed", "error", err)
			}
		}
	}
}

func (a *deviceAudio) applySendspinConfig(cfg Config) {
	if cfg.SendspinEnabled == nil {
		return
	}
	if !*cfg.SendspinEnabled {
		a.stopSendspin("the music was turned off")
		return
	}

	a.mu.Lock()
	name := a.sendspinName
	if cfg.SendspinName != nil {
		name = *cfg.SendspinName
	}
	unpaired := a.sendspinUnpaired
	if cfg.SendspinUnpaired != nil {
		unpaired = *cfg.SendspinUnpaired
		a.sendspinUnpaired = unpaired
	}
	current := a.sendspin
	a.mu.Unlock()

	if name == "" {
		name = "Home Panel"
	}
	if current == nil || a.sendspinName != name {
		a.stopSendspin("the name changed")
		current = nil
	}

	if current == nil {
		player, err := sendspin.New(sendspin.Config{
			StorePath: a.sendspinStore(),
			Name:      name,
			Addresses: panelAddresses(),

			Instance: sendspinInstance(name),
			Product:  "Home Panel",
			Version:  "0.1.0",
			Unpaired: unpaired,

			RequiredLeadTime: 300 * time.Millisecond,
			MinBuffer:        200 * time.Millisecond,
		})
		if err != nil {
			a.log.Error("the music player could not be started", "error", err)
			a.setSendspinError(err)
			return
		}

		player.SetAttach(
			func() {
				spk := speaker.Get()
				if spk == nil {
					return
				}
				spk.Attach(a.musicSourceFor(player))
			},
			func() {
				spk := speaker.Get()
				if spk == nil {
					return
				}
				spk.Detach(a.musicSourceFor(player))
			},
		)

		player.SetBackgrounds(speaker.Sound().Backgrounds())
		player.SetStarted(func() bool {
			spk := speaker.Get()
			return spk != nil && spk.Started()
		})
		player.SetPlacement(
			func() (uint64, time.Duration) {
				spk := speaker.Get()
				if spk == nil {
					return 0, speaker.DefaultLatency
				}
				return spk.Position()
			},
			func() time.Duration {
				spk := speaker.Get()
				if spk == nil {
					return speaker.DefaultLatency
				}
				return spk.Latency()
			},
		)
		player.OnVolume(func(level int) {
			a.setVolume(level)
		})

		player.OnServerVolume(func(level int) {
			a.setVolume(level)
		})
		player.OnSettings(func(_ int, muted bool, _ int) {

			a.mu.Lock()
			a.sendspinMuted = muted
			a.mu.Unlock()
		})
		player.OnChange(func() {
			a.setSendspinStatus(player.Status())
		})
		if err := player.Start(); err != nil {
			a.log.Error("the music player could not be started", "error", err)
			a.setSendspinError(err)
			return
		}
		a.mu.Lock()
		a.sendspin = player
		a.sendspinName = name
		a.mu.Unlock()
		a.setSendspinStatus(player.Status())

		if a.onSendspinStarted != nil {
			a.onSendspinStarted()
		}
		current = player

		a.startSendspinPump()
	}
	current.SetUnpaired(unpaired)
}

func (a *deviceAudio) startSendspinPump() {
	a.mu.Lock()
	if a.sendspinPump != nil {
		a.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	a.sendspinPump = cancel
	a.mu.Unlock()

	go func() {
		ticker := time.NewTicker(10 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
			a.mu.Lock()
			player := a.sendspin
			a.mu.Unlock()
			if player == nil {
				continue
			}

			a.setSendspinStatus(player.Status())

			if player.Streaming() {
				a.ensureOutput()
			}
		}
	}()
}

func (a *deviceAudio) setSendspinStatus(status sendspin.Status) {
	a.mu.Lock()
	a.sendspinStatus = status
	a.mu.Unlock()
	if a.onSendspinStatus != nil {
		a.onSendspinStatus(status)
	}
}

func (a *deviceAudio) setSendspinError(err error) {
	a.log.Error("the music player failed", "error", err)
}

func (a *deviceAudio) sendspinStore() string {
	return filepath.Join(a.credentialsDir, "sendspin.json")
}

func (a *deviceAudio) sendspinPairingToken() (string, error) {
	return sendspin.PairingToken(a.sendspinStore())
}

func (a *deviceAudio) stopSendspin(reason string) {
	a.mu.Lock()
	player := a.sendspin
	pump := a.sendspinPump
	a.sendspin = nil
	a.sendspinName = ""
	a.sendspinPump = nil
	a.mu.Unlock()
	if pump != nil {
		pump()
	}
	if player != nil {
		player.Stop(reason)
	}
}

func (a *deviceAudio) stop() {
	a.stopCapture()
	a.closeOutput()
	a.stopSendspin("the device is shutting down")
}

func sendspinInstance(name string) string {
	sum := uint32(2166136261)
	for i := 0; i < len(name); i++ {
		sum = (sum ^ uint32(name[i])) * 16777619
	}

	if machine := machineAddress(); machine != "" {
		for i := 0; i < len(machine); i++ {
			sum = (sum ^ uint32(machine[i])) * 16777619
		}
	}
	return fmt.Sprintf("%s-%02x%02x", name, (sum>>8)&0xFF, sum&0xFF)
}

func machineAddress() string {
	entries, err := os.ReadDir("/sys/class/net")
	if err != nil {
		return ""
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		address, err := os.ReadFile(filepath.Join("/sys/class/net", entry.Name(), "address"))
		if err != nil {
			continue
		}
		candidate := strings.TrimSpace(string(address))
		if len(candidate) == 17 && candidate[2] == ':' {
			return candidate
		}
	}
	return ""
}
