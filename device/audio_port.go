package main

import (
	"context"
	"io"
	"log"
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
// The speaker asks for the frames starting at an absolute output frame index.
// The player works in its own time: it knows when each frame should reach
// the card, and it fills a buffer with the frames that are due then.
type musicSource struct {
	client *sendspin.Client

	mu sync.Mutex
	// at is the output frame index the speaker last asked for.
	at uint64
	// played is how many frames have been given to the speaker.
	played uint64
	// rate is the output's rate, so the index and the time can be related.
	rate int
	// buf is the buffer the player fills, reused between calls.
	buf []byte
	// pending is what the player gave but the speaker has not taken yet.
	pending []int16
}

// newMusicSource makes the source for a Sendspin client.
func newMusicSource(client *sendspin.Client, rate int) *musicSource {
	return &musicSource{client: client, rate: rate}
}

// Render fills out with the frames starting at output frame index at.
//
// The speaker asks for the frames the card is about to play, and the player
// is asked for the audio that is due then, which is what it knows best: it
// has the server's clock and the card's delay, and it places each frame at
// the right microsecond.
//
// What the player gives is kept until the speaker asks for it again, so a
// frame is never dropped between the two.
func (m *musicSource) Render(at uint64, out []int16) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.at = at
	want := len(out)
	if want == 0 {
		return
	}

	// What is left over from last time is played first: the speaker is
	// asking for the frames that come next, and they were given already.
	if len(m.pending) > 0 {
		n := copy(out, m.pending)
		m.played += uint64(n / speaker.Channels)
		m.pending = m.pending[n:]
		if len(m.pending) > 0 {
			return
		}
	}

	// The player is asked for the audio that is due now.
	playAt := time.Now().Add(m.lead())
	m.buf = resize(m.buf, want*2)
	if !m.client.Fill(m.buf, playAt, 0) {
		// Nothing is due: silence, which is what a stream that is not
		// playing sounds like.
		clear(out)
		return
	}

	// The player's bytes are interleaved stereo; the speaker wants samples.
	samples := make([]int16, 0, len(m.buf)/4)
	for i := 0; i+3 < len(m.buf); i += 4 {
		samples = append(samples, int16(uint16(m.buf[i])|uint16(m.buf[i+1])<<8))
	}
	n := copy(out, samples)
	m.played += uint64(n / speaker.Channels)
	if n < len(samples) {
		m.pending = append(m.pending[:0], samples[n:]...)
	}
}

// lead is how far ahead the player is asked to fill.
//
// The speaker's write happens before the card takes it, so the audio has to
// be ready a little ahead of the moment it is played.
func (m *musicSource) lead() time.Duration {
	return 80 * time.Millisecond
}

// resize makes a buffer at least n bytes long.
func resize(b []byte, n int) []byte {
	if cap(b) >= n {
		return b[:n]
	}
	return make([]byte, n)
}

// micBridge is the microphone, seen through the ported stack.
type micBridge struct {
	src *mic.Source
}

// start opens the microphone and runs its reader.
func (m *micBridge) start(ctx context.Context) error {
	go func() {
		if err := m.src.Run(ctx); err != nil && ctx.Err() == nil {
			log.Printf("capture: %v", err)
		}
	}()
	return m.src.Start(ctx)
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

// renderDue asks the player for the audio that is due now and holds it for
// the speaker.
//
// The speaker pulls from the source, so the frames are made here when they
// are due and the speaker takes them when it reaches them.
func (m *musicSource) renderDue() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.buf == nil {
		m.buf = make([]byte, speaker.Rate/10*4)
	}
	playAt := time.Now().Add(m.lead())
	if !m.client.Fill(m.buf, playAt, 0) {
		return
	}
	samples := make([]int16, 0, len(m.buf)/4)
	for i := 0; i+3 < len(m.buf); i += 4 {
		samples = append(samples, int16(uint16(m.buf[i])|uint16(m.buf[i+1])<<8))
	}
	m.pending = append(m.pending[:0], samples...)
}

// monoSamples turns mono little-endian bytes into interleaved stereo samples.
//
// The speaker takes interleaved stereo, and the reply, the test sound and
// the media are all mono, so each sample is laid into both channels.
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

// startPortedSpeaker opens the ported speaker and runs its loop.
//
// The speaker holds the card for as long as it is running, and it is
// stopped when the program stops. A card that is still open when the next
// open comes is a card that ALSA asserts on, and an assertion takes the
// process down, so the speaker is started once and kept.
func (a *deviceAudio) startPortedSpeaker(ctx context.Context) error {
	a.mu.Lock()
	if a.speakerPlayer != nil {
		a.mu.Unlock()
		return nil
	}
	a.mu.Unlock()

	spk := speaker.Get()
	// The device is taken first: the write loop needs it, and reports
	// the failure if it cannot be had.
	if err := spk.Start(ctx); err != nil {
		return err
	}
	runCtx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		if err := spk.Run(runCtx); err != nil && runCtx.Err() == nil {
			a.log.Warn("the speaker stopped", "error", err)
		}
	}()

	a.mu.Lock()
	a.speakerPlayer = spk
	a.speakerCancel = cancel
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
	a.speakerCancel = nil
	a.speakerPlayer = nil
	a.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if spk != nil {
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

	mu sync.Mutex
	// frames is how many frames have been written and not yet played.
	frames int
	// played is when the last frame written will be heard.
	played time.Time
}

func (e *externalPlayback) Write(p []byte) (int, error) {
	n, err := e.writer.Write(p)
	if n > 0 {
		// The card's own writes block at the pace the output plays them,
		// which is what keeps the loop from running ahead of the sound.
		// A writer that does not block is paced here instead, so that
		// the queue is taken at the rate it is played.
		frames := n / 4
		d := time.Duration(frames) * time.Second / time.Duration(e.rate)
		e.mu.Lock()
		e.played = time.Now().Add(d)
		e.mu.Unlock()
		time.Sleep(d)
	}
	return n, err
}

func (e *externalPlayback) Close() error { return e.writer.Close() }

// Delay is how many frames written are still to be played.
//
// The backend's writer does not say, so the frames are counted: a period
// is ten milliseconds, and the writer is written at the rate it was opened
// at.
func (e *externalPlayback) Delay() (int, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.frames, nil
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
	w, err := backend.Playback(ctx, playbackRate)
	if err != nil {
		return err
	}
	speaker.SetExternalPlayback(&externalPlayback{writer: w, rate: playbackRate})
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
// It is called before the microphone is started, because the microphone
// holds the capture for as long as it runs.
func (a *deviceAudio) useExternalCapture(ctx context.Context) error {
	a.mu.Lock()
	backend := a.backend
	a.mu.Unlock()
	if backend == nil {
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
