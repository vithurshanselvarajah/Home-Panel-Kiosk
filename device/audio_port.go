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

type musicSource struct {
	client *sendspin.Client
}

func newMusicSource(client *sendspin.Client, rate int) *musicSource {
	return &musicSource{client: client}
}

func (m *musicSource) Render(at uint64, out []int16) {
	if m.client == nil || len(out) == 0 {
		return
	}
	m.client.Render(at, out)
}

type micBridge struct {
	src *mic.Source
}

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

func (m *micBridge) frames(name string) (<-chan []int16, func()) {
	return m.src.Listen(name)
}

func applyMicSettings(gain, sensitivity int, mixing framework.Mixing, cancel, leveling, denoise bool) {
	framework.SetMicGain(gain)
	framework.SetMicSensitivity(sensitivity)
	framework.SetMicMixing(mixing)
	framework.SetMicCancel(cancel)
	framework.SetMicLeveling(leveling)
	framework.SetMicDenoise(denoise)
}

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

	spk.SetASP(framework.Get().Speaker.ASP)
	spk.SetStereo(stereo)
	spk.SetTone(aspTone(tone))
	spk.SetResampling(resampling)
}

func aspTone(t framework.Tone) asp.Tone {
	switch t {
	case framework.ToneNone:
		return asp.Tone{}
	default:
		return asp.Tone{Bass: 0, Treble: 0}
	}
}

func setClearerSpeech(on bool) {
	if spk := speaker.Get(); spk != nil {
		spk.SetClearerSpeech(on)
	}
}

func setLouderTop(on bool) {
	if spk := speaker.Get(); spk != nil {
		spk.SetLouderTop(on)
	}
}

var _ = speaker.Rate

func (a *deviceAudio) portedSpeaker() *speaker.Player {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.speakerPlayer
}

func (a *deviceAudio) musicSourceFor(client *sendspin.Client) *musicSource {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.musicSource == nil || a.musicSource.client != client {
		a.musicSource = newMusicSource(client, speaker.Rate)
	}
	return a.musicSource
}

func monoSamples16(pcm []byte) []int16 {
	out := make([]int16, 0, len(pcm)/2)
	for i := 0; i+1 < len(pcm); i += 2 {
		out = append(out, int16(uint16(pcm[i])|uint16(pcm[i+1])<<8))
	}
	return out
}

func monoSamples(pcm []byte) []int16 {
	out := make([]int16, 0, len(pcm))
	for i := 0; i+1 < len(pcm); i += 2 {
		v := int16(uint16(pcm[i]) | uint16(pcm[i+1])<<8)
		out = append(out, v, v)
	}
	return out
}

type startingSpeaker struct {
	finished chan struct{}
	once     sync.Once
}

func (s *startingSpeaker) finish() { s.once.Do(func() { close(s.finished) }) }

func (a *deviceAudio) waitForSpeaker(ctx context.Context) error {
	return speaker.Get().WaitForRelease().Wait(ctx)
}

func (a *deviceAudio) startPortedSpeaker(ctx context.Context) error {

	a.mu.Lock()
	if a.speakerPlayer != nil {
		a.mu.Unlock()
		return nil
	}

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

	starting := &startingSpeaker{finished: make(chan struct{})}
	a.speakerStarting = starting
	spk := speaker.Get()

	if !spk.Claim() {
		a.mu.Unlock()

		if err := a.waitForSpeaker(ctx); err != nil {
			return err
		}
		return a.startPortedSpeaker(ctx)
	}

	a.speakerPlayer = spk
	a.mu.Unlock()
	defer func() {
		a.mu.Lock()
		a.speakerStarting = nil
		a.mu.Unlock()
		starting.finish()
	}()

	if err := spk.Start(ctx); err != nil {
		a.mu.Lock()
		a.speakerPlayer = nil
		a.mu.Unlock()

		spk.Release()
		return err
	}
	runCtx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		if err := spk.Run(runCtx); err != nil && runCtx.Err() == nil {

			if !strings.Contains(err.Error(), "already running") {
				a.log.Warn("the speaker stopped", "error", err)
			}
		}
	}()

	a.mu.Lock()
	a.speakerCancel = cancel
	a.speakerDone = done
	a.mu.Unlock()

	return nil
}

func (a *deviceAudio) stopPortedSpeaker() {
	a.mu.Lock()
	cancel := a.speakerCancel
	spk := a.speakerPlayer
	done := a.speakerDone

	a.speakerCancel = nil
	a.speakerDone = nil
	a.speakerPlayer = nil
	a.mu.Unlock()

	if cancel != nil {
		cancel()
	}

	if done != nil {
		<-done
	}
	if spk != nil {

		_ = spk.Close()
	}
	speaker.SetExternalPlayback(nil)
}

type externalPlayback struct {
	writer io.WriteCloser
	rate   int

	channels int

	blocking bool

	delay func() (int, error)

	mu sync.Mutex

	frames int

	writtenAt time.Time

	played time.Time
}

func (e *externalPlayback) Write(p []byte) (int, error) {
	n, err := e.writer.Write(p)
	if n > 0 {

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

func (e *externalPlayback) Delay() (int, error) {
	if e.delay != nil {
		return e.delay()
	}
	e.mu.Lock()
	defer e.mu.Unlock()

	elapsed := time.Since(e.writtenAt)
	played := int(elapsed.Seconds() * float64(e.rate))
	left := e.frames - played
	if left < 0 {
		left = 0
	}
	return left, nil
}

func (a *deviceAudio) useExternalPlayback(ctx context.Context) error {
	a.mu.Lock()
	backend := a.backend
	a.mu.Unlock()
	if backend == nil {
		return nil
	}

	w, err := backend.Playback(ctx, speaker.CardRate())
	if err != nil {
		return err
	}

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

func speakerStep(percent int) int {
	percent = max(0, min(percent, 100))
	return percent * speaker.VolumeSteps / 100
}

type externalCapture struct {
	reader io.ReadCloser
}

func (e *externalCapture) Read(buf []byte) (int, error) { return e.reader.Read(buf) }

func (e *externalCapture) Close() error { return e.reader.Close() }

func (a *deviceAudio) useExternalCapture(ctx context.Context) error {
	a.mu.Lock()
	backend := a.backend
	a.mu.Unlock()
	if backend == nil {
		return nil
	}
	if card, isCard := backend.(interface{ IsCard() bool }); isCard && card.IsCard() {

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

func (a *deviceAudio) applyAudioSettings(c Config) {

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

	tuning := framework.DefaultASP
	if c.SpeakerTuning != nil {
		tuning = *c.SpeakerTuning
	}
	framework.SetSpeakerASP(tuning)
	if spk := speaker.Get(); spk != nil {
		spk.SetASP(tuning)
	}

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
	louderSpeech := false
	if c.LouderSpeech != nil {
		louderSpeech = *c.LouderSpeech
	}
	louderSpeechOn.Store(louderSpeech)
}

func (a *deviceAudio) setSpeakerTuning(on bool) {
	framework.SetSpeakerASP(on)
	if spk := speaker.Get(); spk != nil {
		spk.SetASP(on)
	}
}

func (a *deviceAudio) setSpeakerGain(db int) {
	db = max(0, min(db, framework.SpeakerGainMax))
	framework.SetSpeakerGain(db)
	if spk := speaker.Get(); spk != nil {
		if err := spk.SetSpeakerGain(db); err != nil {
			log.Printf("the amplifier's gain could not be set: %v", err)
		}
	}
}

func (a *deviceAudio) setSpeakerStereo(on bool) {
	framework.SetSpeakerStereo(on)
	if spk := speaker.Get(); spk != nil {
		spk.SetStereo(on)
	}
}

func (a *deviceAudio) setSpeakerBass(db float64) {
	a.setSpeakerShelves(db, framework.Get().Speaker.Treble)
}

func (a *deviceAudio) setSpeakerTreble(db float64) {
	a.setSpeakerShelves(framework.Get().Speaker.Bass, db)
}

func (a *deviceAudio) setSpeakerShelves(bass, treble float64) {
	bass = max(-asp.ToneRange, min(bass, asp.ToneRange))
	treble = max(-asp.ToneRange, min(treble, asp.ToneRange))
	if spk := speaker.Get(); spk != nil {
		spk.SetTone(asp.Tone{Bass: bass, Treble: treble})
	}
}

func (a *deviceAudio) setClearerSpeech(on bool) {
	setClearerSpeech(on)
}

func (a *deviceAudio) setLouderTop(on bool) {
	setLouderTop(on)
}

func (a *deviceAudio) setLouderSpeech(on bool) {
	louderSpeechOn.Store(on)
}

func (a *deviceAudio) setMicCancel(on bool) {
	framework.SetMicCancel(on)
	a.applyMicSettings()
}

func (a *deviceAudio) setMicLeveling(on bool) {
	framework.SetMicLeveling(on)
	a.applyMicSettings()
}

func (a *deviceAudio) setMicDenoise(on bool) {
	framework.SetMicDenoise(on)
	a.applyMicSettings()
}

func (a *deviceAudio) setMicSensitivity(db int) {
	framework.SetMicSensitivity(db)
	a.applyMicSettings()
}

func (a *deviceAudio) applyMicSettings() {
	mic := framework.Get().Microphone
	applyMicSettings(mic.Gain, mic.Sensitivity, mic.Mixing, mic.Cancel, mic.Leveling, mic.Denoise)
}

func (a *deviceAudio) externalWriter() io.WriteCloser {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.output
}
