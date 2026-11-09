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
	// monitoring is whether the microphone is open for the level meter
	// alone.
	monitoring bool
	// monitorCancel stops the monitored capture.
	monitorCancel context.CancelFunc
	// testSound is the counter that says a tone should be played.
	testSound int
	// bounded is whether the turn is bounded, which is the case when the
	// controller asked for a locked microphone.
	bounded bool

	// output is the open speaker, if there is one.
	output io.WriteCloser
	// outputCancel stops the playback goroutine.
	outputCancel context.CancelFunc
	// playing is whether a playback is running.
	playing bool
	// outputGen says which output loop is the current one, so a loop that
	// has been replaced does not clear the flag of the one that replaced
	// it.
	outputGen int
	// clocked is the clocked speaker, when one could be opened. It is
	// what the music is played through, because it knows when each period
	// reaches the sound card and the music player needs to know that.
	clocked echomuse.ClockedAudioOutput
	// clockedChannels is how many channels the clocked output really has.
	clockedChannels int
	// clockedDisabled says the clocked output could not be used, so the
	// music goes through the shared speaker instead. It is not retried
	// after it has failed: a card that would not open for stereo will
	// not open for it later either.
	clockedDisabled bool
	// lastClockedAttempt is when the clocked output was last tried, so it
	// is not tried on every period.
	lastClockedAttempt time.Time

	// voice is the reply being played, and the mixer takes it out of here.
	voice []byte
	// test is the test tone, and the mixer takes it out of here.
	test []byte
	// voiceActive is whether a reply is playing.
	voiceActive bool
	// voiceStreaming is whether the reply's frames are still arriving.
	//
	// It is not the same as voiceActive: a reply is still playing after
	// its last frame has arrived, because the speaker has a queue of its
	// own. The reply ends when the frames have stopped and the queue is
	// empty.
	voiceStreaming bool
	// voiceMixed is whether the music is playing under the reply.
	voiceMixed bool
	// voiceEnded is called when the reply has been played out.
	//
	// The end of the stream is not the end of the reply: the last of the
	// words is said after the last frame has arrived. The mixer is what
	// knows when the reply is really over.
	voiceEnded func()

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
	// sendspinPump is the goroutine that watches for music and opens the
	// speaker for it.
	sendspinPump context.CancelFunc

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

// periodBytes is the size of one playback period.
//
// A period is 10 milliseconds of mono audio at 48 kHz. It is the size the
// mixer works in, so a reply is played one period at a time.
const periodBytes = playbackRate / 100 * 2

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
	s.Monitoring = a.monitoring
}

// selectInput chooses the microphone.
//
// An empty name means the system's own default, which is what the device
// uses before anybody has chosen anything.
func (a *deviceAudio) selectInput(name string) error {
	if name == "" {
		name = audio.DeviceDefault
	}
	return a.backend.SelectAudioDevices(name, a.currentOutput())
}

// selectOutput chooses the speaker.
//
// An empty name means the system's own default, which is what the device
// uses before anybody has chosen anything.
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

// playTestSound plays a short tone through the speaker.
//
// It is what the settings page's test button uses: a person choosing a
// speaker needs to hear it before they commit to it, and a tone is the
// only thing that says which speaker is which.
//
// The tone goes through the same mixer as the music and the reply, rather
// than straight to the sound card. A device that opened the card a second
// time would either fail or steal the card from the music, and a test
// tone that interrupts the music is worse than no test tone.
//
// The tone is a second long, which is long enough to hear and short enough
// that it is not a nuisance.
func (a *deviceAudio) playTestSound() error {
	// The tone is a 440 Hz sine, which is the note a telephone used and is
	// easy to recognise.
	const frequency = 440.0
	samples := playbackRate * 2
	tone := make([]byte, samples*2)
	for i := 0; i < samples; i++ {
		// The envelope fades the tone in and out, so it does not click at
		// the ends.
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

	// The mixer is what plays it, so the speaker has to be open.
	a.ensureOutput()
	return nil
}

// captureLevel is the level of the microphone, as a percentage of full
// scale.
//
// It is zero until the microphone is open, because a level that says
// something about a microphone that is not open is a level that is lying.
func (a *deviceAudio) captureLevel() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.level
}

// monitoringMic reports whether the microphone is open for the level meter
// alone.
//
// The level meter opens the microphone so a person can see what it hears
// while they are setting the device up. That is the device listening to
// the room for the person, not the controller listening for a wake word,
// so the two are told apart.
//
// The flag says the monitor was asked for. What matters here is whether
// the monitor actually opened the microphone: when the controller is
// already using it, the monitor reads the frames the controller's capture
// produces and the controller really is listening.
func (a *deviceAudio) monitoringMic() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.monitoring && !a.capturing
}

// setMonitoring opens or closes the microphone so its level can be shown.
//
// The monitoring is a flag rather than a capture of its own when the
// controller is already using the microphone. The level meter reads the
// frames the capture produces, because a sound card cannot be opened twice
// — ALSA says so by taking the process down — and because the level of the
// frames being sent is the level that matters.
//
// The microphone is opened for the meter alone when the controller is not
// using it, so a person can see what the microphone hears while they are
// setting the device up.
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

// startMonitor opens the microphone for the level meter alone.
//
// It is only called when the controller is not already using the
// microphone. When it is, the level comes from the frames the controller's
// capture produces, because a sound card cannot be opened twice.
func (a *deviceAudio) startMonitor() {
	a.mu.Lock()
	if a.monitorCancel != nil || a.capturing {
		a.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	a.monitorCancel = cancel
	a.mu.Unlock()

	go func() {
		defer func() {
			a.mu.Lock()
			a.monitorCancel = nil
			a.mu.Unlock()
			cancel()
		}()

		reader, err := a.backend.Capture(ctx, captureRate)
		if err != nil {
			a.log.Warn("the microphone could not be opened for the level meter", "error", err)
			return
		}
		defer reader.Close()

		buf := make([]byte, captureRate/10*2)
		for {
			n, err := reader.Read(buf)
			if n > 0 {
				a.setLevel(peakLevel(buf[:n]))
			}
			if err != nil {
				if ctx.Err() == nil {
					a.log.Warn("the level meter stopped", "error", err)
				}
				return
			}
			// The controller's capture takes the microphone back when it
			// needs it, and the level meter gives it up.
			a.mu.Lock()
			taken := a.capturing
			a.mu.Unlock()
			if taken {
				return
			}
		}
	}()
}

// stopMonitor closes the monitored microphone.
func (a *deviceAudio) stopMonitor() {
	a.mu.Lock()
	cancel := a.monitorCancel
	a.monitorCancel = nil
	a.monitoring = false
	a.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// setTestSound plays a tone when the counter changes.
//
// The counter is what says whether the tone should play: a button that is
// pressed twice has to make two sounds, and a flag cannot tell the
// difference between the first press and the second.
func (a *deviceAudio) setTestSound(counter int) {
	a.mu.Lock()
	changed := counter != a.testSound
	a.testSound = counter
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
// startCapture opens the microphone and streams it to the controller.
//
// The stream begins when the controller asks for it and stops when it says
// to stop, which is the order the protocol expects: a device that streams
// before it is asked is a device streaming to nobody.
//
// The microphone is opened once. The level meter reads the same frames
// rather than opening a second capture, because a sound card cannot be
// opened twice and ALSA says so by taking the process down.
func (a *deviceAudio) startCapture(bounded bool) {
	a.mu.Lock()
	if a.capturing {
		a.mu.Unlock()
		return
	}
	// The level meter may have the microphone open. It gives it up rather
	// than the two of them trying to open a sound card at once, which ALSA
	// answers by taking the process down.
	if a.monitorCancel != nil {
		a.mu.Unlock()
		a.stopMonitor()
		a.mu.Lock()
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
	a.voiceStreaming = true
	a.voiceMixed = a.sendspin != nil && a.sendspin.Active()
	a.mu.Unlock()

	a.ensureOutput()
}

// finishVoice is called when the reply's last frame arrives.
//
// The reply is not over yet: the speaker has a queue of its own, and the
// last of the words is said after this. The mixer ends the reply when the
// queue is empty, which is the real end of it.
func (a *deviceAudio) finishVoice() {
	a.mu.Lock()
	a.voiceStreaming = false
	// The mixer takes the reply out of the queue as it plays, so the
	// reply is over when the queue is empty. A reply that was cut off
	// has an empty queue already, and the mixer has already ended it.
	//
	// The reply may also have been played out already: the mixer runs a
	// period ahead, so a short reply can be finished before the frame
	// that ends it arrives. The reply is ended here in that case, or it
	// would wait for a mixer that has already let it go.
	empty := len(a.voice) == 0
	over := a.voiceActive && empty
	if over {
		a.voiceActive = false
	}
	ended := a.voiceEnded
	a.mu.Unlock()
	if over {
		a.finishPlayback()
		if ended != nil {
			ended()
		}
	}
}

// flush drops whatever is queued.
//
// A flush is a reply that was cut off. The frame that would say the reply
// ended never arrives, so the reply is ended here.
func (a *deviceAudio) flush() {
	a.mu.Lock()
	a.voice = nil
	a.voiceActive = false
	a.voiceStreaming = false
	a.voiceMixed = false
	ended := a.voiceEnded
	a.mu.Unlock()
	a.closeOutput()
	if ended != nil {
		ended()
	}
}

// ensureOutput opens the speaker if it is not open.
//
// The generation counter is what makes this safe. A loop that is on its way
// out clears the flag when it finishes, and a new loop that started in
// between would have its flag cleared by the old one — so a second press
// would open the speaker a second time, and ALSA asserts on a card that is
// already open. The counter says which loop is the current one, so only the
// current loop clears the flag.
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

// releaseOutput marks the output loop as finished.
//
// Only the current loop clears the flag. A loop that has been replaced must
// not clear the flag of the one that replaced it, or the next sound would
// open the speaker a second time.
func (a *deviceAudio) releaseOutput(generation int) {
	a.mu.Lock()
	if a.outputGen == generation {
		a.playing = false
		a.outputCancel = nil
		a.output = nil
	}
	a.mu.Unlock()
}

// closeOutput closes the speaker.
//
// The writer is closed here rather than left to the loop, because the loop
// may be a moment behind: it is the loop that closes the card, and a card
// that is closed a moment late is a card that the next open finds already
// open. ALSA asserts on that, and an assertion takes the process down.
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
	if writer != nil {
		_ = writer.Close()
	}
}

// outputLoop mixes the reply and the music and plays them.
//
// It is one loop, because one loop is what keeps them in step. Two loops
// writing to one sound card is two clocks, and the reply arrives late.
//
// The music is pulled from the player rather than pushed to it: the player
// knows when each period should be heard, and a device that only played
// what it was given would play the music in whatever order the periods
// arrived, which is what makes it choppy.
//
// The clocked output is what the music goes through when one can be
// opened. It reports when each period reaches the sound card, and the
// player needs that: given a guessed time the player cannot tell that it
// is seconds behind, and it plays what it queued long ago rather than
// what is due. The shared speaker is the fallback for a card that will
// not open for the clocked output, and the music goes through it then.
//
// This is the shape the SDK's own emulator uses, which was written for the
// same device.
func (a *deviceAudio) outputLoop(ctx context.Context, generation int) {
	// The clocked output is opened first, when there is one. It is the
	// music's output and the reply's, because one card cannot be opened
	// twice.
	clocked, channels := a.openClocked(ctx)

	var writer io.WriteCloser
	if clocked == nil {
		// Without a clock the music goes through the shared speaker, and
		// the time handed to the player is a guess.
		w, err := a.backend.Playback(ctx, playbackRate)
		if err != nil {
			a.log.Error("the speaker could not be opened", "error", err)
			a.releaseOutput(generation)
			return
		}
		writer = w
		channels = 1
	}

	a.mu.Lock()
	// The loop may have been replaced while the speaker was opening, in
	// which case the new one owns the card and this one must not.
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

	// The speaker is released before the loop returns, so the next thing
	// that wants to make a sound finds the card closed.
	//
	// A card that is still open when the next open comes is a card that
	// ALSA asserts on, and an assertion takes the whole process down.
	defer func() {
		if clocked != nil {
			_ = clocked.Close()
		} else {
			_ = writer.Close()
		}
		a.releaseOutput(generation)
	}()

	// A period is 10 milliseconds, which is the player's own period. The
	// music is stereo and the reply is mono, so the stereo buffer is
	// twice the size of the mono one.
	stereo := make([]byte, playbackRate/100*4)

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
		muted := a.muted
		ducked := a.ducked
		duckDB := a.duckDB
		// One period is taken from each queue, so a long reply is spread
		// over the periods that follow rather than played all at once.
		voice := a.takeAudio(&a.voice, periodBytes)
		test := a.takeAudio(&a.test, periodBytes)
		a.mu.Unlock()

		// The music is mixed in first, and the reply is mixed over it.
		// The other way round would let the reply duck the music, which
		// is what the controller's duck request is for.
		musicReady := player != nil && player.Active()

		clear(stereo)
		if musicReady {
			// The clocked output says when the period reaches the card.
			// The shared speaker has no such answer, so a guess is used
			// and the player smooths it.
			playAt := time.Now().Add(80 * time.Millisecond)
			uncertainty := 2 * time.Millisecond
			if clocked != nil {
				if at, unc, err := clocked.NextPlaybackTime(); err == nil {
					playAt, uncertainty = at, unc
				}
			}
			player.Fill(stereo, playAt, uncertainty)
			applySendspinGain(stereo, volume, muted, ducked, duckDB)
		}

		// The reply is mono and the music is stereo, so the reply is
		// laid into both channels of the music.
		if len(test) > 0 {
			a.mixVoiceIntoStereo(stereo, test)
		}
		if len(voice) > 0 {
			a.mixVoiceIntoStereo(stereo, voice)
		}
		if muted {
			clear(stereo)
		}

		// The stereo is written to a mono card by averaging the two
		// channels, or to a stereo card whole. The card says how many it
		// has.
		out := stereo
		if channels == 1 {
			out = make([]byte, len(stereo)/2)
			audio.StereoToMono(stereo, out)
		}

		var err error
		if clocked != nil {
			_, err = clocked.Write(out)
		} else {
			_, err = writer.Write(out)
		}
		if err != nil {
			if ctx.Err() == nil {
				a.log.Warn("the speaker missed a period", "error", err)
			}
			return
		}

		// The speaker is released when there is nothing left to play.
		//
		// A loop that kept the card open for ever would make every
		// later sound fail: ALSA asserts on a card that is already
		// open, and an assertion takes the whole process down.
		//
		// The music player counts as something to play for as long as
		// it is connected, not only while it has audio queued. A queue
		// empties between tracks, and releasing the card then would cut
		// the music off and make the next track start late.
		a.mu.Lock()
		empty := len(a.voice) == 0 && len(a.test) == 0 && (player == nil || !musicConnected(player))
		// The reply is over when its frames have stopped arriving and the
		// speaker has played them all. The end of the stream is not that:
		// the speaker still has the words in it, and the display would go
		// quiet while the reply was still being said.
		//
		// It does not wait for the music to finish. The music may run for
		// the rest of the evening, and a reply that waits for it never
		// ends.
		replyDone := a.voiceActive && !a.voiceStreaming && len(a.voice) == 0
		a.mu.Unlock()
		if replyDone {
			a.mu.Lock()
			a.voiceActive = false
			ended := a.voiceEnded
			a.mu.Unlock()
			a.finishPlayback()
			if ended != nil {
				ended()
			}
		}
		if empty {
			// The speaker is drained rather than dropped, so the last
			// of the tone is heard rather than cut off.
			if drainer, ok := writer.(interface{ Drain() error }); ok {
				_ = drainer.Drain()
			}
			return
		}
	}
}

// openClocked opens the clocked speaker, if it can be opened.
//
// It is tried at most once a second, because a card that will not open
// for the clocked output will not open for it on the next period either,
// and a failure on every period would fill the log.
//
// The channel count is a request. The card may give fewer than were
// asked for, and the output says what it really has.
func (a *deviceAudio) openClocked(ctx context.Context) (echomuse.ClockedAudioOutput, int) {
	a.mu.Lock()
	disabled := a.clockedDisabled
	last := a.lastClockedAttempt
	a.mu.Unlock()
	if disabled {
		return nil, 0
	}
	if !last.IsZero() && time.Since(last) < time.Second {
		return nil, 0
	}

	a.mu.Lock()
	a.lastClockedAttempt = time.Now()
	a.mu.Unlock()

	// The music is stereo, and stereo is what the player fills. A card
	// that will not do stereo falls back to mono inside the backend.
	out, err := a.backend.OpenClockedOutput(ctx, playbackRate, 2)
	if err != nil {
		a.log.Warn("the clocked speaker could not be opened; using the shared speaker", "error", err)
		a.mu.Lock()
		a.clockedDisabled = true
		a.mu.Unlock()
		return nil, 0
	}
	channels := 2
	if counted, ok := out.(interface{ Channels() int }); ok {
		if n := counted.Channels(); n > 0 {
			channels = n
		}
	}
	if channels != 2 {
		a.log.Info("the clocked speaker is not stereo", "channels", channels)
	}
	return out, channels
}

// mixVoiceIntoStereo mixes a mono reply into a stereo buffer.
//
// The reply is laid into both channels, so it sits in the middle of the
// music rather than on one side of it.
//
// The reply is mono and the buffer is stereo, so the reply's own samples
// are walked one at a time and laid into each frame's two channels.
func (a *deviceAudio) mixVoiceIntoStereo(stereo, voice []byte) {
	sample := 0
	for i := 0; i+3 < len(stereo) && sample+1 < len(voice); i += 4 {
		v := int32(int16(uint16(voice[sample]) | uint16(voice[sample+1])<<8))
		sample += 2
		for _, j := range []int{i, i + 2} {
			sum := int32(int16(uint16(stereo[j])|uint16(stereo[j+1])<<8)) + v
			if sum > 32767 {
				sum = 32767
			}
			if sum < -32768 {
				sum = -32768
			}
			sample := int16(sum)
			stereo[j], stereo[j+1] = byte(sample), byte(uint16(sample)>>8)
		}
	}
}

// musicConnected says whether the music player has a server behind it.
//
// The player's own status says "connected" or "playing" when there is one,
// and "listening" when it is waiting for a server to find it. The card is
// held for the first two and released for the third, because a player with
// nobody behind it is not going to make a sound.
func musicConnected(player *sendspin.Client) bool {
	switch player.Status().State {
	case "connected", "playing":
		return true
	default:
		return false
	}
}

// takeAudio takes the next period of the queued audio.
//
// The queue is the whole of what is left to play, and one period is taken
// from it. A queue that was emptied all at once would play the first
// period over and over, and a reply of two seconds would be two hundred
// milliseconds of it.
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

// applySendspinGain scales the music by the device's volume, and ducks it
// when the controller asks for it.
//
// The duck is applied to the music only. The reply is the thing the user is
// meant to hear, so it is never ducked.
func applySendspinGain(pcm []byte, volume int, muted, ducked bool, duckDB float64) {
	duckGain := 1.0
	if ducked {
		duckGain = math.Pow(10, duckDB/20)
	}
	for i := 0; i+1 < len(pcm); i += 2 {
		v := float64(int16(uint16(pcm[i]) | uint16(pcm[i+1])<<8))
		if muted {
			v = 0
		} else {
			v = v * float64(volume) / 100 * duckGain
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

// finishPlayback waits for the speaker to empty and reports the playback
// finished.
//
// The controller releases its speaking state when playback is reported
// finished, so a report that is early is a reply that is still playing
// while the display says it is over.
//
// The report is made even when the drain fails: a speaker that will not
// drain must not leave the reply on the display for the life of the
// process.
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
			Version:   "0.1.0",
			Unpaired:  unpaired,
			// The lead and the jitter buffer are the Echo firmware's
			// own figures. The server holds back the audio it sends by
			// this much and more, and the buffer is what the group is
			// played in step with, so it is the server's figure and
			// not ours to choose.
			RequiredLeadTime: 2000 * time.Millisecond,
			MinBuffer:        2000 * time.Millisecond,
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
		player.OnChange(func() {
			_ = a.device.SendSendspinStatus(player.Status())
		})
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
		// The speaker is opened when the music starts playing, not when
		// the player does. A player that is listening for a server is
		// silent, and a speaker opened for it would sit there holding
		// the sound card for nothing.
		//
		// The change callback does not say when the music starts: it
		// fires when the session changes, which is before the first
		// note has arrived. So the pump watches for the audio itself.
		a.startSendspinPump()
	}
	current.SetUnpaired(unpaired)
}

// startSendspinPump watches for music and opens the speaker for it.
//
// The pump is what makes the music audible. The player reports that it is
// active when it has audio queued, and the speaker has to be open before
// that audio can be heard — so something has to look, and the change
// callback is not it: it fires when the session changes, which is before
// the first note has arrived. Without the pump the music arrives, is
// decoded, is queued, and is never played, because the speaker was never
// opened for it.
//
// It is the SDK's own emulator that does the same thing, and it is the
// reference for this.
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
			if player != nil && player.Active() {
				a.ensureOutput()
			}
		}
	}()
}

// sendspinStore is where the music player's identity is kept.
func (a *deviceAudio) sendspinStore() string {
	return filepath.Join(a.credentialsDir, "sendspin.json")
}

// stopSendspin stops the music player.
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
