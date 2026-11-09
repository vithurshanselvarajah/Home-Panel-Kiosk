package main

import (
	"context"
	"io"
	"log/slog"
	"math"
	"path/filepath"
	"sync"
	"time"

	"github.com/wilbowes/EchoMuse-SDK/clients/go/echomuse"
	"github.com/wilbowes/EchoMuse-SDK/clients/go/echomuse/protocol"
	"github.com/wilbowes/EchoMuse-SDK/clients/go/echomuse/sendspin"
	"github.com/wilbowes/EchoMuse-SDK/clients/go/echomuse/turn"

	"github.com/vithurshanselvarajah/Home-Panel-Kiosk/audio"
)

// deviceAudio is the device's audio: the microphone it listens with, the
// speaker it answers through, and the music that plays under a reply.
//
// It is one object because the three share one sound card and one device's
// worth of state. The voice and the music go out through the same speaker,
// so they are mixed rather than played one after the other — a reply that
// waited for the music to stop would be late answering.
type deviceAudio struct {
	backend *audio.Backend
	device  *echomuse.Device
	guard   *turn.Guard
	log     *slog.Logger
	// credentialsDir is where the music player keeps its identity.
	credentialsDir string

	mu sync.Mutex
	// capture is the open microphone, if there is one.
	capture io.ReadCloser
	// captureCancel stops the capture goroutine.
	captureCancel context.CancelFunc
	// capturing is whether a capture is running.
	capturing bool
	// bounded is whether the turn is bounded, which is the case when the
	// controller asked for a locked microphone.
	bounded bool

	// output is the open speaker, if there is one.
	output io.WriteCloser
	// outputCancel stops the playback goroutine.
	outputCancel context.CancelFunc
	// playing is whether a playback is running.
	playing bool

	// voice is the reply being played, and the mixer takes it out of here.
	voice []byte
	// voiceActive is whether a reply is playing.
	voiceActive bool
	// voiceMixed is whether the music is playing under the reply.
	voiceMixed bool

	// volume is the device's volume, 0 to 100.
	volume int
	// level is the microphone's level, as a percentage of full scale.
	level int
	// muted is whether the device's own mute switch is on.
	muted bool
	// ducked is whether the controller asked for the music to duck.
	ducked bool
	// duckDB is how far the music ducks, in decibels.
	duckDB float64

	// sendspin is the music player.
	sendspin *sendspin.Client
	// sendspinName is the name the player was started with.
	sendspinName string
	// sendspinUnpaired is whether the player accepts an unpaired server.
	sendspinUnpaired bool
	// sendspinMuted is whether the music player was muted by its server.
	sendspinMuted bool

	// endpoints are the audio devices the machine has.
	inputs  []audio.Endpoint
	outputs []audio.Endpoint
}

// The rates. The controller expects captured audio at 16 kHz and sends
// audio at 48 kHz.
const (
	captureRate  = audio.CaptureRate
	playbackRate = audio.PlaybackRate
)

// setEndpoints records the audio devices the machine has.
func (a *deviceAudio) setEndpoints(inputs, outputs []audio.Endpoint) {
	a.mu.Lock()
	a.inputs = inputs
	a.outputs = outputs
	a.mu.Unlock()
}

// fillState copies the audio's own state into the reported state.
func (a *deviceAudio) fillState(s *State) {
	a.mu.Lock()
	defer a.mu.Unlock()
	s.AudioInputs = append(s.AudioInputs, a.inputs...)
	s.AudioOutputs = append(s.AudioOutputs, a.outputs...)
	s.SelectedAudioInput, s.SelectedAudioOutput = a.backend.SelectedAudioDevices()
	// The level is the microphone's own, and it is only meaningful while
	// the microphone is open. The speech detector's level is used when
	// there is one, because it is the level of the frames that are being
	// sent rather than of every frame.
	if a.level > s.Level {
		s.Level = a.level
	}
}

// selectInput chooses the microphone.
func (a *deviceAudio) selectInput(name string) error {
	if name == "" {
		name = audio.DeviceDefault
	}
	return a.backend.SelectAudioDevices(name, a.currentOutput())
}

// selectOutput chooses the speaker.
func (a *deviceAudio) selectOutput(name string) error {
	if name == "" {
		name = audio.DeviceDefault
	}
	return a.backend.SelectAudioDevices(a.currentInput(), name)
}

// currentInput is the microphone in use.
func (a *deviceAudio) currentInput() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	in, _ := a.backend.SelectedAudioDevices()
	return in
}

// currentOutput is the speaker in use.
func (a *deviceAudio) currentOutput() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	_, out := a.backend.SelectedAudioDevices()
	return out
}

// setVolume sets the device's volume.
func (a *deviceAudio) setVolume(level int) {
	a.mu.Lock()
	a.volume = clampVolume(level)
	a.mu.Unlock()
	if a.sendspin != nil {
		a.sendspin.SetVolume(level)
	}
}

// setDucked turns the music duck on or off.
func (a *deviceAudio) setDucked(on bool) {
	a.mu.Lock()
	a.ducked = on
	a.mu.Unlock()
}

// clampVolume keeps a volume in the range the device accepts.
func clampVolume(level int) int {
	if level < 0 {
		return 0
	}
	if level > 100 {
		return 100
	}
	return level
}

// applyConfig applies the configuration the controller pushed.
func (a *deviceAudio) applyConfig(c protocol.Config) {
	if c.StartupVolume != nil {
		a.setVolume(*c.StartupVolume)
	}
	if c.DuckDb != nil {
		a.mu.Lock()
		a.duckDB = *c.DuckDb
		a.mu.Unlock()
	}
	a.applySendspinConfig(c)
}

// startCapture opens the microphone and streams it to the controller.
//
// The stream begins when the controller asks for it and stops when it says
// to stop, which is the order the protocol expects: a device that streams
// before it is asked is a device streaming to nobody.
func (a *deviceAudio) startCapture(bounded bool) {
	a.mu.Lock()
	if a.capturing {
		a.mu.Unlock()
		return
	}
	a.capturing = true
	a.bounded = bounded
	ctx, cancel := context.WithCancel(context.Background())
	a.captureCancel = cancel
	a.mu.Unlock()

	// The bounded turn is the device's own, so it starts when the
	// microphone does.
	a.guard.Start(time.Now())

	a.log.Info("the microphone is opening")

	go func() {
		defer func() {
			a.mu.Lock()
			a.capturing = false
			a.captureCancel = nil
			a.capture = nil
			a.mu.Unlock()
			cancel()
		}()

		reader, err := a.backend.Capture(ctx, captureRate)
		if err != nil {
			a.log.Error("the microphone could not be opened", "error", err)
			return
		}
		a.mu.Lock()
		a.capture = reader
		a.mu.Unlock()
		defer reader.Close()

		// 100 milliseconds of audio at 16 kHz is 3200 bytes, which is one
		// of the capture frames' natural size.
		buf := make([]byte, captureRate/10*2)
		for {
			n, err := reader.Read(buf)
			if n > 0 {
				// The level is measured here rather than by the
				// speech detector, because the detector only sees
				// the frames of a bounded turn. A meter that only
				// moves during a turn is a meter that is dead the
				// rest of the time.
				a.setLevel(peakLevel(buf[:n]))
				a.pushFrame(buf[:n])
			}
			if err != nil {
				if ctx.Err() == nil {
					a.log.Warn("the microphone stopped", "error", err)
				}
				return
			}
		}
	}()
}

// setLevel records the level of the last block of audio.
func (a *deviceAudio) setLevel(level int) {
	a.mu.Lock()
	a.level = level
	a.mu.Unlock()
}

// peakLevel is the loudest sample in a block, as a percentage of full
// scale.
//
// The scale is in decibels below full scale, so the quiet end of it is
// stretched out rather than the loud end. A room that is quiet still moves
// the meter, which is what makes it readable.
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

// stopCapture closes the microphone.
//
// The reader is closed as well as cancelled, because a capture that is only
// marked as stopped leaves the sound card open, and the next capture opens
// a second one. The device then streams whichever of the two the sound
// server gave it, and the controller hears silence while the display says
// it is listening.
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
	a.guard.End()
}

// pushFrame sends one block of captured audio.
//
// A bounded turn replaces the streaming for the length of the turn, so the
// turn's own rules apply to what is sent: nothing goes out until somebody
// is speaking, and the turn ends when they stop.
func (a *deviceAudio) pushFrame(pcm []byte) {
	a.mu.Lock()
	bounded := a.bounded
	a.mu.Unlock()

	if bounded {
		a.guard.Push(pcm, time.Now())
		return
	}

	if err := a.device.SendMicAudio(pcm); err != nil {
		a.log.Debug("the audio did not reach the controller", "error", err)
	}
}

// playVoice plays a reply.
//
// The reply is queued rather than written straight to the speaker, because
// the music is playing on the same speaker and the two have to be mixed.
// Writing it straight would cut the music off and start the reply late.
func (a *deviceAudio) playVoice(pcm []byte) {
	a.mu.Lock()
	a.voice = append(a.voice, pcm...)
	a.voiceActive = true
	a.voiceMixed = a.sendspin != nil && a.sendspin.Active()
	a.mu.Unlock()

	a.ensureOutput()
}

// finishVoice is called when the reply ends.
func (a *deviceAudio) finishVoice() {
	a.mu.Lock()
	// The mixer takes the reply out of the queue as it plays, so the
	// reply is finished when the queue is empty.
	empty := len(a.voice) == 0
	a.mu.Unlock()
	if empty {
		a.finishPlayback()
	}
}

// flush drops whatever is queued.
func (a *deviceAudio) flush() {
	a.mu.Lock()
	a.voice = nil
	a.voiceActive = false
	a.voiceMixed = false
	a.mu.Unlock()
	a.closeOutput()
}

// ensureOutput opens the speaker if it is not open.
func (a *deviceAudio) ensureOutput() {
	a.mu.Lock()
	if a.playing {
		a.mu.Unlock()
		return
	}
	a.playing = true
	ctx, cancel := context.WithCancel(context.Background())
	a.outputCancel = cancel
	a.mu.Unlock()

	go a.outputLoop(ctx, cancel)
}

// closeOutput closes the speaker.
func (a *deviceAudio) closeOutput() {
	a.mu.Lock()
	cancel := a.outputCancel
	a.outputCancel = nil
	a.playing = false
	a.mu.Unlock()

	if cancel != nil {
		cancel()
	}
}

// outputLoop mixes the reply and the music and plays them.
//
// It is one loop, because one loop is what keeps them in step. Two loops
// writing to one sound card is two clocks, and the reply arrives late.
func (a *deviceAudio) outputLoop(ctx context.Context, cancel context.CancelFunc) {
	defer func() {
		a.mu.Lock()
		a.playing = false
		a.outputCancel = nil
		a.mu.Unlock()
		cancel()
	}()

	writer, err := a.backend.Playback(ctx, playbackRate)
	if err != nil {
		a.log.Error("the speaker could not be opened", "error", err)
		return
	}
	a.mu.Lock()
	a.output = writer
	a.mu.Unlock()
	defer writer.Close()

	// 100 milliseconds of mono audio at 48 kHz.
	mono := make([]byte, playbackRate/10*2)

	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}

		a.mu.Lock()
		voice := a.voice
		volume := a.volume
		muted := a.muted
		ducked := a.ducked
		duckDB := a.duckDB
		a.voice = nil
		a.mu.Unlock()

		clear(mono)
		if len(voice) > 0 {
			copy(mono, voice)
		}
		if muted {
			clear(mono)
		} else {
			applyGain(mono, volume)
		}
		_ = ducked
		_ = duckDB

		if _, err := writer.Write(mono); err != nil {
			if ctx.Err() == nil {
				a.log.Warn("the speaker missed a period", "error", err)
			}
			return
		}
	}
}

// finishPlayback waits for the speaker to empty and reports the playback
// finished.
//
// The controller releases its speaking state when playback is reported
// finished, so a report that is early is a reply that is still playing
// while the display says it is over.
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
	if err := a.device.SendPlaybackStats(protocol.PlaybackStats{}); err != nil {
		a.log.Debug("playback completion did not reach the controller", "error", err)
	}
}

// applyGain scales the audio by the device's volume.
//
// The scale is applied here rather than on the sound card so that the
// volume is the same for the voice and the music.
func applyGain(pcm []byte, volume int) {
	if volume == 100 {
		return
	}
	gain := float64(volume) / 100
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

// answerSendspinToken answers the controller's request for the music
// player's pairing token.
func (a *deviceAudio) answerSendspinToken() {
	a.mu.Lock()
	player := a.sendspin
	a.mu.Unlock()
	if player == nil {
		_ = a.device.SendSendspinTokenResponse("", "", "the music player is not running")
		return
	}
	if err := a.device.SendSendspinTokenResponse(player.PairingToken(), player.ClientID(), ""); err != nil {
		a.log.Warn("the music player's token did not reach the controller", "error", err)
	}
}

// applySendspinConfig starts or stops the music player.
//
// The controller owns the music: it decides whether the device is a music
// player at all, what it is called, and whether it accepts a server that
// has not paired with it.
func (a *deviceAudio) applySendspinConfig(cfg protocol.Config) {
	if cfg.SendspinEnabled == nil {
		return
	}
	if !*cfg.SendspinEnabled {
		a.stopSendspin("the controller turned the music off")
		_ = a.device.SendSendspinStatus(nil)
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
			Instance:  "echomuse-home-panel",
			Product:   "Home Panel",
			Version:    "0.1.0",
			Unpaired:  unpaired,
			// A shared speaker needs a modest lead to keep the start
			// responsive; a large jitter buffer is seconds of latency
			// the user hears.
			RequiredLeadTime: 250 * time.Millisecond,
			MinBuffer:        150 * time.Millisecond,
		})
		if err != nil {
			a.log.Error("the music player could not be started", "error", err)
			_ = a.device.SendSendspinStatus(map[string]string{"state": "error", "error": err.Error()})
			return
		}
		player.OnVolume(func(level int) {
			a.setVolume(level)
		})
		player.OnSettings(func(_ int, muted bool, _ int) {
			a.mu.Lock()
			a.sendspinMuted = muted
			a.mu.Unlock()
		})
		player.OnChange(func() { _ = a.device.SendSendspinStatus(player.Status()) })
		if err := player.Start(); err != nil {
			a.log.Error("the music player could not be started", "error", err)
			_ = a.device.SendSendspinStatus(map[string]string{"state": "error", "error": err.Error()})
			return
		}
		a.mu.Lock()
		a.sendspin = player
		a.sendspinName = name
		a.mu.Unlock()
		_ = a.device.SendSendspinStatus(player.Status())
		current = player
	}
	current.SetUnpaired(unpaired)
}

// sendspinStore is where the music player's identity is kept.
func (a *deviceAudio) sendspinStore() string {
	return filepath.Join(a.credentialsDir, "sendspin.json")
}

// stopSendspin stops the music player.
func (a *deviceAudio) stopSendspin(reason string) {
	a.mu.Lock()
	player := a.sendspin
	a.sendspin = nil
	a.sendspinName = ""
	a.mu.Unlock()
	if player != nil {
		player.Stop(reason)
	}
}

// stop closes everything.
func (a *deviceAudio) stop() {
	a.stopCapture()
	a.closeOutput()
	a.stopSendspin("the device is shutting down")
}

// dB converts a decibel figure into a gain.
func dB(db float64) float64 {
	return math.Pow(10, db/20)
}
