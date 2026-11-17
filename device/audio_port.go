package main

import (
	"context"
	"io"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/vithurshanselvarajah/Home-Panel-Kiosk/audio/asp"
	"github.com/vithurshanselvarajah/Home-Panel-Kiosk/audio/mic"
	"github.com/vithurshanselvarajah/Home-Panel-Kiosk/audio/mic/framework"
	"github.com/vithurshanselvarajah/Home-Panel-Kiosk/audio/speaker"
	"github.com/vithurshanselvarajah/Home-Panel-Kiosk/device/sendspin"
)

// The bridge between this project's own audio paths and the ported stack.
//
// The ported speaker is a mixer that pulls: it asks a Source for the frames
// the card is about to play, and it plays them when they are due. The
// ported microphone is a source that pushes frames to whoever is listening.
//
// This project's music is a Sendspin client, and its own player already
// knows when each frame should be heard. So the music becomes a Source: the
// speaker asks it for the frames, and it answers with the ones its player
// says are due.
//
// The bridge is the whole of the difference between the two designs.

// musicSource is the Sendspin player, seen as something the speaker pulls
// from.
//
// The speaker asks for the frames starting at an absolute output frame
// index, and the placement answers with the audio for those frames. The
// frame index IS the timeline: the audio was placed on it when it arrived,
// against the server's timestamp, and the speaker takes it from there.
//
// Nothing is compared against a clock here, and nothing is guessed about
// when the audio is due. A clock comparison would be between two readings
// taken by two different loops at two different moments, and the error
// between them is never small enough to leave alone — so every period
// corrects it, which is heard as a crackle.
type musicSource struct {
	client *sendspin.Client
}

// newMusicSource makes the source for a Sendspin client.
func newMusicSource(client *sendspin.Client, rate int) *musicSource {
	return &musicSource{client: client}
}

// Render fills out with the frames starting at output frame index at.
func (m *musicSource) Render(at uint64, out []int16) {
	if m.client == nil || len(out) == 0 {
		return
	}
	m.client.Render(at, out)
}

// micBridge is the microphone, seen through the ported stack.
type micBridge struct {
	src *mic.Source
}

// start opens the microphone and runs its reader.
//
// The reader is started AFTER the capture is open, never before. The reader
// needs the card to be held, and it gives up for good if it is not: it
// returns "the capture device is not held" and never comes back, so the
// level meter stays at nothing and the wake word never hears anything.
//
// This is the ordering the service group gives the microphone upstream: the
// device is taken first, and only then does the reader start.
func (m *micBridge) start(ctx context.Context) error {
	if err := m.src.Start(ctx); err != nil {
		return err
	}
	go func() {
		if err := m.src.Run(ctx); err != nil && ctx.Err() == nil {
			log.Printf("capture: %v", err)
		}
	}()
	return nil
}

// close releases the microphone.
func (m *micBridge) close() error {
	return m.src.Close()
}

// frames is the channel the frames arrive on.
func (m *micBridge) frames(name string) (<-chan []int16, func()) {
	return m.src.Listen(name)
}

// applyMicSettings tells the ported stack what the microphone's settings are.
//
// The settings live in the shim, so that the ported code reads them where it
// expects them.
func applyMicSettings(gain, sensitivity int, mixing framework.Mixing, cancel, leveling, denoise bool) {
	framework.SetMicGain(gain)
	framework.SetMicSensitivity(sensitivity)
	framework.SetMicMixing(mixing)
	framework.SetMicCancel(cancel)
	framework.SetMicLeveling(leveling)
	framework.SetMicDenoise(denoise)
}

// applySpeakerSettings tells the ported stack what the speaker's settings are.
//
// The gain, the stereo, the tone and the resampling are the speaker's own
// settings, and they are applied to the player as well as kept in the shim:
// the player reads some of them when it starts, and the rest as it plays.
func applySpeakerSettings(gainDB int, stereo bool, resampling framework.Resampling, tone framework.Tone) {
	framework.SetSpeakerGain(gainDB)
	framework.SetSpeakerStereo(stereo)
	framework.SetSpeakerResampling(resampling)
	framework.SetSpeakerTone(tone)

	spk := speaker.Get()
	if spk == nil {
		return
	}
	if err := spk.SetSpeakerGain(gainDB); err != nil {
		log.Printf("the speaker gain could not be set: %v", err)
	}
	// The tuning is on unless it is turned off: it is the vendor's own
	// sound, and it is what makes a small speaker sound like itself.
	spk.SetASP(framework.Get().Speaker.ASP)
	spk.SetStereo(stereo)
	spk.SetTone(aspTone(tone))
	spk.SetResampling(resampling)
}

// aspTone is the speaker's tone, in the tuning's own terms.
func aspTone(t framework.Tone) asp.Tone {
	switch t {
	case framework.ToneNone:
		return asp.Tone{}
	default:
		return asp.Tone{Bass: 0, Treble: 0}
	}
}

// setClearerSpeech turns the tuning's low band down while words are played.
func setClearerSpeech(on bool) {
	if spk := speaker.Get(); spk != nil {
		spk.SetClearerSpeech(on)
	}
}

// setLouderTop makes the top of the volume dial go hotter into the tuning.
func setLouderTop(on bool) {
	if spk := speaker.Get(); spk != nil {
		spk.SetLouderTop(on)
	}
}

// unused keeps the speaker import honest.
var _ = speaker.Rate

// portedSpeaker is the ported speaker, or nil when it is not open.
func (a *deviceAudio) portedSpeaker() *speaker.Player {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.speakerPlayer
}

// musicSourceFor is the music, as a source the speaker pulls from.
//
// The source is made once and kept: the player and the source have to be
// the same for as long as the music is playing, because the source holds
// what the player has given but the speaker has not taken yet.
func (a *deviceAudio) musicSourceFor(client *sendspin.Client) *musicSource {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.musicSource == nil || a.musicSource.client != client {
		a.musicSource = newMusicSource(client, speaker.Rate)
	}
	return a.musicSource
}

// monoSamples16 turns mono little-endian bytes into mono samples.
//
// The reply is handed to the speaker's voice path, which takes one channel
// and lays it into both itself: it interpolates the reply up to the card's
// own rate, and it knows what the reply should sound like at the speaker's
// rate. Duplicating the samples here would put them in the wrong place in
// the stream the speaker builds.
func monoSamples16(pcm []byte) []int16 {
	out := make([]int16, 0, len(pcm)/2)
	for i := 0; i+1 < len(pcm); i += 2 {
		out = append(out, int16(uint16(pcm[i])|uint16(pcm[i+1])<<8))
	}
	return out
}

// monoSamples turns mono little-endian bytes into interleaved stereo samples.
//
// The speaker takes interleaved stereo, and the test sound and the media
// are all mono, so each sample is laid into both channels.
func monoSamples(pcm []byte) []int16 {
	out := make([]int16, 0, len(pcm))
	for i := 0; i+1 < len(pcm); i += 2 {
		v := int16(uint16(pcm[i]) | uint16(pcm[i+1])<<8)
		out = append(out, v, v)
	}
	return out
}

// stereoSamples turns interleaved stereo little-endian bytes into samples.
func stereoSamples(pcm []byte) []int16 {
	out := make([]int16, 0, len(pcm)/2)
	for i := 0; i+1 < len(pcm); i += 2 {
		out = append(out, int16(uint16(pcm[i])|uint16(pcm[i+1])<<8))
	}
	return out
}

// startingSpeaker is a speaker being started.
//
// A caller that finds one waits for it: the speaker is started once, and
// two write loops on one player would fight over the queues.
type startingSpeaker struct {
	finished chan struct{}
	once     sync.Once
}

// finish says the speaker has been started, or has failed to.
func (s *startingSpeaker) finish() { s.once.Do(func() { close(s.finished) }) }

// waitForSpeaker waits until the speaker is free to be taken.
//
// The speaker is one for the whole program, so a caller that could not
// take it waits for whoever holds it to let go. The wait is on the
// speaker's own release, not on a guess, so that a release that happens
// between the check and the wait is not missed.
func (a *deviceAudio) waitForSpeaker(ctx context.Context) error {
	return speaker.Get().WaitForRelease().Wait(ctx)
}

// startPortedSpeaker opens the ported speaker and runs its loop.
//
// The speaker holds the card for as long as it is running, and it is
// stopped when the program stops. A card that is still open when the next
// open comes is a card that ALSA asserts on, and an assertion takes the
// process down, so the speaker is started once and kept.
func (a *deviceAudio) startPortedSpeaker(ctx context.Context) error {
	// The speaker is started once. Two write loops on one player would
	// fight over the queues, and the card would get the audio of both.
	//
	// The check and the claim are under one lock: a check that released
	// the lock before the claim let two callers through, and both started
	// the speaker.
	a.mu.Lock()
	if a.speakerPlayer != nil {
		a.mu.Unlock()
		return nil
	}
	// A speaker being started already is waited for, rather than a second
	// one started: two write loops on one player fight over the queues.
	if starting := a.speakerStarting; starting != nil {
		a.mu.Unlock()
		select {
		case <-starting.finished:
		case <-ctx.Done():
			return ctx.Err()
		}
		a.mu.Lock()
		player := a.speakerPlayer
		a.mu.Unlock()
		if player != nil {
			return nil
		}
		return a.startPortedSpeaker(ctx)
	}
	// The player is claimed before the card is taken, so a second caller
	// waits for the first to finish rather than starting one too.
	starting := &startingSpeaker{finished: make(chan struct{})}
	a.speakerStarting = starting
	spk := speaker.Get()
	// The claim is on the speaker itself, because the speaker is one for
	// the whole program: two callers that each thought they held it would
	// both write to it.
	//
	// The claim is not taken here, but after the lock is released and
	// the deferred cleanup is armed: a claim that failed while the lock
	// was held waited for this caller's own "finished" channel, which
	// nothing closes until this call returns. It waited for ever.
	if !spk.Claim() {
		a.mu.Unlock()
		// Somebody else has it. This caller waits for them to let go.
		//
		// The wait is on the speaker itself, not on this caller's own
		// start: the other holder is not this caller, so its start says
		// nothing about when the claim will be given back.
		if err := a.waitForSpeaker(ctx); err != nil {
			return err
		}
		return a.startPortedSpeaker(ctx)
	}
	// The player is claimed here, before the write loop starts: a claim
	// made after the loop was launched let a second caller through, and
	// two write loops fought over the queues.
	a.speakerPlayer = spk
	a.mu.Unlock()
	defer func() {
		a.mu.Lock()
		a.speakerStarting = nil
		a.mu.Unlock()
		starting.finish()
	}()

	// The device is taken first: the write loop needs it, and reports
	// the failure if it cannot be had.
	if err := spk.Start(ctx); err != nil {
		a.mu.Lock()
		a.speakerPlayer = nil
		a.mu.Unlock()
		// The claim is given back: the speaker was never opened, and a
		// claim that was never given back would hold the speaker for
		// ever. Nothing else in the program could ever make a sound.
		spk.Release()
		return err
	}
	runCtx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		if err := spk.Run(runCtx); err != nil && runCtx.Err() == nil {
			// The write loop is already running: another caller got
			// there first, and this one has nothing to do.
			if !strings.Contains(err.Error(), "already running") {
				a.log.Warn("the speaker stopped", "error", err)
			}
		}
	}()

	a.mu.Lock()
	a.speakerCancel = cancel
	a.speakerDone = done
	a.mu.Unlock()

	// The speaker opens the card itself, which takes a moment; the run
	// loop reports the failure when it happens.
	return nil
}

// stopPortedSpeaker closes the speaker and releases the card.
//
// The speaker is one for the whole program, so it is stopped when the
// program stops and not before. A card that is still open when the next
// open comes is a card that ALSA asserts on, and an assertion takes the
// process down.
//
// The external playback is cleared with it, so that a speaker left over
// from a previous run does not write to a writer that has gone.
func (a *deviceAudio) stopPortedSpeaker() {
	a.mu.Lock()
	cancel := a.speakerCancel
	spk := a.speakerPlayer
	done := a.speakerDone
	// The claim is taken here, before the wait, so that a second caller
	// does not stop a speaker that has just been started by somebody
	// else. The wait is here and not in the caller, because the caller
	// and the write loop's own cleanup both stop the speaker, and only
	// one of them may wait for the loop.
	a.speakerCancel = nil
	a.speakerDone = nil
	a.speakerPlayer = nil
	a.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	// The write loop is waited for before the player is let go.
	//
	// Two write loops on one player fight over the queues, and the card
	// gets the audio of both: the speaker starts again as soon as it is
	// stopped, and the old loop was still writing. So the player stays
	// claimed until the loop has finished.
	if done != nil {
		<-done
	}
	if spk != nil {
		// The claim is given back by the close: the speaker was this
		// caller's own, because only the holder of the claim can have
		// set it.
		//
		// A caller that holds no speaker does not give the claim back:
		// the claim may belong to somebody else by now, and a stop that
		// took a speaker that was not its own would take the card out
		// from under them.
		_ = spk.Close()
	}
	speaker.SetExternalPlayback(nil)
}

// externalPlayback is the backend's writer, seen as the speaker's playback.
//
// The device program owns the card through its own backend, so that a
// recording can stand in for it in the tests. The speaker writes to this
// rather than opening the card itself.
type externalPlayback struct {
	writer io.WriteCloser
	rate   int
	// channels is how many samples a frame holds, so the frames written
	// can be counted from the bytes.
	channels int
	// blocking says the writer waits for the hardware itself, so the
	// write is already paced and must not be paced again here.
	blocking bool
	// delay asks the writer how much is still to be played. The card
	// knows exactly; a writer that does not is counted here instead.
	delay func() (int, error)

	mu sync.Mutex
	// frames is how many frames have been written and not yet played.
	frames int
	// writtenAt is when the last frame was written, which is when the
	// count of played frames starts from.
	writtenAt time.Time
	// played is when the last frame written will be heard.
	played time.Time
}

func (e *externalPlayback) Write(p []byte) (int, error) {
	n, err := e.writer.Write(p)
	if n > 0 {
		// The card's own write blocks until the hardware has room, so it
		// already keeps the loop at the pace the output plays. Sleeping
		// here as well would double that pace and the queue would run
		// dry between periods — which is a gap, and a gap is heard as a
		// pop.
		//
		// A writer that does not block (a recording standing in for the
		// card) is paced here instead, so the queue is still taken at the
		// rate it is played.
		frames := n / (2 * e.channels)
		d := time.Duration(frames) * time.Second / time.Duration(e.rate)
		e.mu.Lock()
		e.frames += frames
		e.writtenAt = time.Now()
		e.played = e.writtenAt.Add(d)
		e.mu.Unlock()
		if !e.blocking {
			time.Sleep(d)
		}
	}
	return n, err
}

func (e *externalPlayback) Close() error { return e.writer.Close() }

// Delay is how many frames written are still to be played.
//
// The card knows how much of its ring is still to be heard, and it is asked:
// a count made here cannot tell how much the hardware has already taken, and
// a count that only grows becomes a latency of minutes, which the speaker
// then believes and the player then places the audio against.
func (e *externalPlayback) Delay() (int, error) {
	if e.delay != nil {
		return e.delay()
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	// A writer that cannot say: the frames written, less what the clock
	// says has been heard since they were written.
	elapsed := time.Since(e.writtenAt)
	played := int(elapsed.Seconds() * float64(e.rate))
	left := e.frames - played
	if left < 0 {
		left = 0
	}
	return left, nil
}

// useExternalPlayback gives the speaker the backend's writer.
//
// It is called before the speaker is started, because the speaker holds
// the playback for as long as it runs.
func (a *deviceAudio) useExternalPlayback(ctx context.Context) error {
	a.mu.Lock()
	backend := a.backend
	a.mu.Unlock()
	if backend == nil {
		return nil
	}
	// The card is opened at the rate the amplifier's tuning was made for,
	// which is the speaker's own business: the speaker converts what it is
	// given on the way out.
	w, err := backend.Playback(ctx, speaker.CardRate())
	if err != nil {
		return err
	}
	// A card's own write blocks until the hardware has room, so it is
	// already paced. A recording standing in for the card does not block,
	// so the pacing is done here.
	//
	// The card also knows how much of its ring is still to be heard, and
	// that is what the speaker asks for: it is the output latency the
	// music is placed against.
	_, blocking := w.(interface{ Blocks() bool })
	var delay func() (int, error)
	if d, ok := w.(interface{ Delay() (int, error) }); ok {
		delay = d.Delay
	}
	speaker.SetExternalPlayback(&externalPlayback{
		writer:   w,
		rate:     speaker.CardRate(),
		channels: speaker.Channels,
		blocking: blocking,
		delay:    delay,
	})
	return nil
}

// speakerStep is the panel's volume, in the speaker's own steps.
//
// The panel's level is a percentage and the speaker's is a step on a curve
// of thirty, so the percentage is scaled onto the steps. The curve is what
// makes the top of the dial room-loud rather than painful.
func speakerStep(percent int) int {
	percent = max(0, min(percent, 100))
	return percent * speaker.VolumeSteps / 100
}

// externalCapture is the backend's microphone, seen as the mic's capture.
//
// The device program owns the card through its own backend, so that a
// recording can stand in for it in the tests. The microphone reads this
// rather than opening the card itself.
type externalCapture struct {
	reader io.ReadCloser
}

func (e *externalCapture) Read(buf []byte) (int, error) { return e.reader.Read(buf) }

func (e *externalCapture) Close() error { return e.reader.Close() }

// useExternalCapture gives the microphone the backend's capture.
//
// It is only used when the sound card has been replaced by a recording: the
// ported microphone opens the card itself, and it opens it correctly, with
// the card's own device number, its channel count and its sample format.
//
// So the capture is only taken over when the backend is not the card.
func (a *deviceAudio) useExternalCapture(ctx context.Context) error {
	a.mu.Lock()
	backend := a.backend
	a.mu.Unlock()
	if backend == nil {
		return nil
	}
	if card, isCard := backend.(interface{ IsCard() bool }); isCard && card.IsCard() {
		// The card is the card: the microphone opens it as it knows how,
		// with the device number and the format the board wants.
		mic.SetExternalCapture(nil)
		return nil
	}
	r, err := backend.Capture(ctx, captureRate)
	if err != nil {
		return err
	}
	mic.SetExternalCapture(&externalCapture{reader: r})
	return nil
}

// applyAudioSettings applies the audio stack's own settings.
//
// The speaker's tuning, its gain, its stereo, its shelves and the
// microphone's gain, its levelling, its denoiser and its canceller are the
// audio stack's own settings, and they are applied here so that they take
// effect whether they arrive at the start or in the middle.
func (a *deviceAudio) applyAudioSettings(c Config) {
	// The microphone's settings.
	micGain := framework.DefaultMicGain
	if c.MicGainDb != nil {
		micGain = *c.MicGainDb
	}
	micSensitivity := framework.DefaultSensitivity
	if c.MicSensitivity != nil {
		micSensitivity = *c.MicSensitivity
	}
	micCancel := framework.DefaultCancel
	if c.MicCancel != nil {
		micCancel = *c.MicCancel
	}
	micLeveling := framework.DefaultLeveling
	if c.MicLeveling != nil {
		micLeveling = *c.MicLeveling
	}
	micDenoise := framework.DefaultDenoise
	if c.MicDenoise != nil {
		micDenoise = *c.MicDenoise
	}
	applyMicSettings(micGain, micSensitivity, framework.DefaultMixing, micCancel, micLeveling, micDenoise)

	// The speaker's settings.
	speakerGain := 0
	if c.SpeakerGainDb != nil {
		speakerGain = *c.SpeakerGainDb
	}
	speakerStereo := false
	if c.SpeakerStereo != nil {
		speakerStereo = *c.SpeakerStereo
	}
	resampling := framework.DefaultResampling
	if c.SpeakerResampling != nil {
		switch *c.SpeakerResampling {
		case "linear":
			resampling = framework.ResampleLinear
		case "hold":
			resampling = framework.ResampleHold
		default:
			resampling = framework.ResampleSinc
		}
	}
	applySpeakerSettings(speakerGain, speakerStereo, resampling, framework.DefaultTone)

	// The tuning is on unless it is turned off.
	tuning := framework.DefaultASP
	if c.SpeakerTuning != nil {
		tuning = *c.SpeakerTuning
	}
	framework.SetSpeakerASP(tuning)
	if spk := speaker.Get(); spk != nil {
		spk.SetASP(tuning)
	}

	// The listener's own shelves, and the two settings that change what
	// the tuning does while words are playing.
	bass, treble := 0.0, 0.0
	if c.SpeakerBass != nil {
		bass = *c.SpeakerBass
	}
	if c.SpeakerTreble != nil {
		treble = *c.SpeakerTreble
	}
	if spk := speaker.Get(); spk != nil {
		spk.SetTone(asp.Tone{Bass: bass, Treble: treble})
	}
	clearer := false
	if c.ClearerSpeech != nil {
		clearer = *c.ClearerSpeech
	}
	setClearerSpeech(clearer)
	louderTop := false
	if c.LouderTop != nil {
		louderTop = *c.LouderTop
	}
	setLouderTop(louderTop)
}

// The speaker's own settings, and the microphone's.
//
// Each of them changes the audio stack's own setting and is applied to the
// player as well, because the player holds the state the change is about.

// setSpeakerTuning turns the vendor's tuning on or off.
//
// The tuning is the amplifier's own filter and limiter, read off the
// device at run time. With it off, the speaker plays what it is given.
func (a *deviceAudio) setSpeakerTuning(on bool) {
	framework.SetSpeakerASP(on)
	if spk := speaker.Get(); spk != nil {
		spk.SetASP(on)
	}
}

// setSpeakerGain raises the amplifier's gain over the vendor's, in dB.
func (a *deviceAudio) setSpeakerGain(db int) {
	db = max(0, min(db, framework.SpeakerGainMax))
	framework.SetSpeakerGain(db)
	if spk := speaker.Get(); spk != nil {
		if err := spk.SetSpeakerGain(db); err != nil {
			log.Printf("the amplifier's gain could not be set: %v", err)
		}
	}
}

// setSpeakerStereo plays the two drivers separately.
func (a *deviceAudio) setSpeakerStereo(on bool) {
	framework.SetSpeakerStereo(on)
	if spk := speaker.Get(); spk != nil {
		spk.SetStereo(on)
	}
}

// setSpeakerBass changes the bass shelf, in dB.
func (a *deviceAudio) setSpeakerBass(db float64) {
	a.setSpeakerShelves(db, framework.Get().Speaker.Treble)
}

// setSpeakerTreble changes the treble shelf, in dB.
func (a *deviceAudio) setSpeakerTreble(db float64) {
	a.setSpeakerShelves(framework.Get().Speaker.Bass, db)
}

// setSpeakerShelves changes both shelves at once, which is what the player
// takes.
func (a *deviceAudio) setSpeakerShelves(bass, treble float64) {
	bass = max(-asp.ToneRange, min(bass, asp.ToneRange))
	treble = max(-asp.ToneRange, min(treble, asp.ToneRange))
	if spk := speaker.Get(); spk != nil {
		spk.SetTone(asp.Tone{Bass: bass, Treble: treble})
	}
}

// setClearerSpeech brings the tuning's low band down while words play.
func (a *deviceAudio) setClearerSpeech(on bool) {
	setClearerSpeech(on)
}

// setLouderTop makes the top of the volume dial go hotter into the tuning.
func (a *deviceAudio) setLouderTop(on bool) {
	setLouderTop(on)
}

// setMicCancel turns echo cancellation on or off.
//
// With it off, the speaker's own voice comes back through the microphones
// and the wake word hears the panel talking to itself.
func (a *deviceAudio) setMicCancel(on bool) {
	framework.SetMicCancel(on)
	a.applyMicSettings()
}

// setMicLeveling turns the microphone's automatic gain on or off.
func (a *deviceAudio) setMicLeveling(on bool) {
	framework.SetMicLeveling(on)
	a.applyMicSettings()
}

// setMicDenoise turns noise reduction on or off.
func (a *deviceAudio) setMicDenoise(on bool) {
	framework.SetMicDenoise(on)
	a.applyMicSettings()
}

// setMicSensitivity changes how far over the room counts as speech.
func (a *deviceAudio) setMicSensitivity(db int) {
	framework.SetMicSensitivity(db)
	a.applyMicSettings()
}

// applyMicSettings tells the ported stack the microphone's settings, from
// what the shim holds.
func (a *deviceAudio) applyMicSettings() {
	mic := framework.Get().Microphone
	applyMicSettings(mic.Gain, mic.Sensitivity, mic.Mixing, mic.Cancel, mic.Leveling, mic.Denoise)
}

// externalWriter is the card the speaker writes to, when the card has been
// taken over by this program's own backend.
//
// The speaker holds it, so it is asked for rather than opened again.
func (a *deviceAudio) externalWriter() io.WriteCloser {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.output
}
