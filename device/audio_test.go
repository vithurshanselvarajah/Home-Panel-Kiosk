package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"math"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/vithurshanselvarajah/Home-Panel-Kiosk/audio"
	"github.com/vithurshanselvarajah/Home-Panel-Kiosk/audio/speaker"
	vol "github.com/vithurshanselvarajah/Home-Panel-Kiosk/audio/volume"
	"github.com/vithurshanselvarajah/Home-Panel-Kiosk/device/esphome"
	"github.com/vithurshanselvarajah/Home-Panel-Kiosk/device/sendspin"
	"github.com/vithurshanselvarajah/Home-Panel-Kiosk/device/state"
)

type fakeBackend struct {
	mu sync.Mutex

	open bool

	opens int
}

func (b *fakeBackend) Capture(ctx context.Context, rate int) (io.ReadCloser, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.open {
		return nil, errors.New("the device is busy")
	}
	b.open = true
	b.opens++
	return &fakeReader{backend: b, done: make(chan struct{})}, nil
}

func (b *fakeBackend) Playback(ctx context.Context, rate int) (io.WriteCloser, error) {
	return &fakeWriter{}, nil
}

func (b *fakeBackend) OpenClockedOutput(ctx context.Context, rate, channels int) (audio.ClockedOutput, error) {
	return nil, errors.New("no clocked output")
}

func (b *fakeBackend) SelectedAudioDevices() (string, string) { return "", "" }

func (b *fakeBackend) SelectAudioDevices(input, output string) error { return nil }

func (b *fakeBackend) ChooseDefaultInput() error { return nil }

func (b *fakeBackend) isOpen() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.open
}

func (b *fakeBackend) opensCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.opens
}

type fakeReader struct {
	backend *fakeBackend
	done    chan struct{}
	once    sync.Once
}

func (r *fakeReader) Read(p []byte) (int, error) {
	select {
	case <-r.done:
		return 0, io.EOF
	case <-time.After(100 * time.Millisecond):
	}

	return len(p), nil
}

func (r *fakeReader) Close() error {
	r.once.Do(func() {
		close(r.done)
		r.backend.mu.Lock()
		r.backend.open = false
		r.backend.mu.Unlock()
	})
	return nil
}

type fakeWriter struct{}

func (w *fakeWriter) Write(p []byte) (int, error) { return len(p), nil }
func (w *fakeWriter) Close() error                { return nil }

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}

func TestTheMonitorGivesUpTheMicrophone(t *testing.T) {
	backend := &fakeBackend{}
	a := &deviceAudio{backend: backend, log: testLogger()}

	a.setMonitoring(true)
	waitFor(t, func() bool { return backend.isOpen() }, "the meter did not open the microphone")

	a.startCapture()
	defer a.stopCapture()

	waitFor(t, func() bool { return a.capturing }, "the capture did not start")

	if opens := backend.opensCount(); opens != 1 {
		t.Errorf("the microphone was opened %d times, want 1", opens)
	}
}

func TestTheMeterTakesTheMicrophoneBack(t *testing.T) {
	backend := &fakeBackend{}
	a := &deviceAudio{backend: backend, log: testLogger()}

	a.setMonitoring(true)
	waitFor(t, func() bool { return backend.isOpen() }, "the meter did not open the microphone")

	a.startCapture()
	defer a.stopCapture()

	a.stopCapture()
	a.setMonitoring(true)
	waitFor(t, func() bool { return backend.isOpen() }, "the meter did not take the microphone back")
}

func waitFor(t *testing.T, ok func() bool, message string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if ok() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal(message)
}

func TestTheSendspinGainFollowsTheVolume(t *testing.T) {
	full := []byte{0x00, 0x40, 0x00, 0x40}
	half := append([]byte(nil), full...)
	applySendspinGain(half, 50, false, false, 0)

	want := int16(float64(16384) * float64(vol.GainForPercent(50)))
	got := int16(uint16(half[0]) | uint16(half[1])<<8)
	if got != want {
		t.Errorf("at half volume the sample was %d, want %d", got, want)
	}

	if got == 0 {
		t.Error("half volume was silence")
	}
	if got >= 8192 {
		t.Errorf("half volume was %d, which is not quieter than half the amplitude", got)
	}

	silent := append([]byte(nil), full...)
	applySendspinGain(silent, 0, false, false, 0)
	if silent[0] != 0x00 || silent[1] != 0x00 {
		t.Error("at zero volume the music was not silent")
	}
}

func TestTheReportedVolumeIsThePanelsVolume(t *testing.T) {
	a := &deviceAudio{backend: &fakeBackend{}, log: testLogger()}
	a.setVolume(42)

	state := &State{}
	a.fillState(state)

	if state.Volume != 42 {
		t.Errorf("the reported volume was %d, want 42", state.Volume)
	}
	if state.Muted {
		t.Error("the panel reported itself muted when it was not")
	}
}

func TestTheServersMuteSilencesTheMusic(t *testing.T) {
	a := &deviceAudio{backend: &fakeBackend{}, log: testLogger()}

	a.mu.Lock()
	a.sendspinMuted = true
	muted := a.muted || a.sendspinMuted
	a.mu.Unlock()

	pcm := []byte{0x00, 0x40, 0x00, 0x40}
	applySendspinGain(pcm, 100, muted, false, 0)
	for i := 0; i < len(pcm); i += 2 {
		if pcm[i] != 0x00 || pcm[i+1] != 0x00 {
			t.Error("the music was not silent when the server had muted it")
		}
	}
}

func TestTheMicrophoneMuteDoesNotMuteTheSpeaker(t *testing.T) {
	a := &deviceAudio{backend: &fakeBackend{}, log: testLogger()}
	a.setMuted(true)

	a.mu.Lock()
	serverMuted := a.sendspinMuted
	a.mu.Unlock()
	if serverMuted {
		t.Fatal("the microphone mute changed the speaker mute")
	}

	pcm := []byte{0x00, 0x40, 0x00, 0x40}
	applySendspinGain(pcm, 100, serverMuted, false, 0)

	if pcm[0] == 0x00 && pcm[1] == 0x00 {
		t.Fatalf("the speaker was silenced by the microphone mute: %v", pcm)
	}
}

func TestTheMicrophoneStaysOpenForTheWakeWord(t *testing.T) {
	backend := &fakeBackend{}
	a := &deviceAudio{backend: backend, log: testLogger()}

	a.setListening(true)
	waitFor(t, func() bool { return backend.isOpen() }, "the microphone did not open for the wake word")

	a.startCapture()
	a.setStreaming(true)
	a.setStreaming(false)
	a.maybeStopCapture()

	if !backend.isOpen() {
		t.Error("the microphone was closed when the turn ended")
	}
	if !a.capturing {
		t.Error("the capture was stopped when the turn ended")
	}
}

func TestTheWakeWordIsForgottenWhenTheMicrophoneOpens(t *testing.T) {
	backend := &fakeBackend{}
	a := &deviceAudio{backend: backend, log: testLogger()}

	resets := 0
	a.onWakeReset = func() { resets++ }

	a.setListening(true)
	waitFor(t, func() bool { return backend.isOpen() }, "the microphone did not open")

	if resets == 0 {
		t.Error("the wake word was not told that the microphone had opened")
	}
}

func TestTheReplyIsPlayedAtTheSpeakersRate(t *testing.T) {
	a := &deviceAudio{backend: &fakeBackend{}, log: testLogger()}

	pcm := make([]byte, 320)
	for i := 0; i < 160; i++ {
		pcm[i*2] = byte(i)
		pcm[i*2+1] = 0
	}

	converted := a.resampleVoice(pcm)

	if len(converted) != len(pcm) {
		t.Errorf("the reply was %d bytes, want %d: it was converted", len(converted), len(pcm))
	}

	if up := speaker.VoiceUpsample; up != 3 {
		t.Errorf("the voice path interpolates by %d, want 3", up)
	}
}

func TestTheConnectionFollowsTheLink(t *testing.T) {
	tracker := state.NewTracker()
	reporter := &stateReporter{
		log:   testLogger(),
		state: tracker,
		audio: &deviceAudio{backend: &fakeBackend{}, log: testLogger()},
	}

	conn, _ := connection(tracker.Snapshot())
	if conn != "disconnected" {
		t.Errorf("the connection was %q with the link down, want disconnected", conn)
	}

	reporter.setLink(true)
	conn, reason := connection(tracker.Snapshot())
	if conn != "connected" {
		t.Errorf("the connection was %q with the link up, want connected", conn)
	}
	if reason == "" {
		t.Error("the connection had no reason to give")
	}

	reporter.setLink(false)
	conn, _ = connection(tracker.Snapshot())
	if conn != "disconnected" {
		t.Errorf("the connection was %q with the link down, want disconnected", conn)
	}
}

func TestTheMusicIsInTheState(t *testing.T) {
	audio := &deviceAudio{backend: &fakeBackend{}, log: testLogger()}
	audio.setSendspinStatus(sendspin.Status{
		State: "playing",
		Metadata: sendspin.Metadata{
			Title:      "Suck the Blood",
			Artist:     "The Sleepy Jackson",
			Album:      "Fumbling Towards Fitness",
			ArtworkURL: "http://192.168.1.20/cover.jpg",
			ProgressMs: 30000,
			DurationMs: 195000,
			Speed:      1000,
		},
		Transport: []string{"play", "pause", "next", "previous", "seek"},
	})

	var state State
	audio.fillState(&state)

	if state.MusicTitle != "Suck the Blood" {
		t.Errorf("the title was %q, want %q", state.MusicTitle, "Suck the Blood")
	}
	if state.MusicArtist != "The Sleepy Jackson" {
		t.Errorf("the artist was %q, want %q", state.MusicArtist, "The Sleepy Jackson")
	}
	if state.MusicAlbum != "Fumbling Towards Fitness" {
		t.Errorf("the album was %q, want %q", state.MusicAlbum, "Fumbling Towards Fitness")
	}
	if state.MusicArtwork != "http://192.168.1.20/cover.jpg" {
		t.Errorf("the artwork was %q, want %q", state.MusicArtwork, "http://192.168.1.20/cover.jpg")
	}
	if state.MusicPosition != 30000 {
		t.Errorf("the position was %d, want 30000", state.MusicPosition)
	}
	if state.MusicDuration != 195000 {
		t.Errorf("the length was %d, want 195000", state.MusicDuration)
	}
	if !state.MusicPlaying {
		t.Error("the music was not said to be playing")
	}
	if len(state.MusicTransport) != 5 {
		t.Errorf("there were %d transport commands, want 5", len(state.MusicTransport))
	}
}

func TestThePausedMusicIsNotPlaying(t *testing.T) {
	audio := &deviceAudio{backend: &fakeBackend{}, log: testLogger()}
	audio.setSendspinStatus(sendspin.Status{
		State: "connected",
		Metadata: sendspin.Metadata{
			Title: "Suck the Blood",
			Speed: 0,
		},
	})

	var state State
	audio.fillState(&state)

	if state.MusicPlaying {
		t.Error("a paused track was said to be playing")
	}
}

func TestTheMusicDucksUnderAReply(t *testing.T) {
	audio := &deviceAudio{backend: &fakeBackend{}, log: testLogger()}
	audio.volume = 100
	audio.setDuckDb(-20)

	music := make([]byte, periodBytes)
	for i := 0; i+1 < len(music); i += 2 {
		music[i] = 0x00
		music[i+1] = 0x40
	}

	before := make([]byte, len(music))
	copy(before, music)
	applyDuck(music, false, -20)
	if music[1] != before[1] {
		t.Errorf("the music was changed without a duck: %d then %d", before[1], music[1])
	}

	applyDuck(music, true, -20)
	quiet := int16(uint16(music[0]) | uint16(music[1])<<8)
	if quiet >= 16384 {
		t.Errorf("the music was not ducked: %d is not quieter than 16384", quiet)
	}

	if quiet < 1200 || quiet > 2000 {
		t.Errorf("the music was ducked to %d, which is about %.1f decibels rather than twenty",
			quiet, 20*math.Log10(float64(quiet)/16384))
	}
}

func TestTheDuckingIsTurnedOnByTheReply(t *testing.T) {
	audio := &deviceAudio{backend: &fakeBackend{}, log: testLogger()}
	audio.volume = 100
	audio.setDuckDb(-20)

	audio.playReply(make([]byte, 320), false)
	audio.mu.Lock()
	ducked := audio.ducked
	audio.mu.Unlock()
	if !ducked {
		t.Error("the music was not ducked under the reply")
	}

	audio.finishReply()

	audio.mu.Lock()
	audio.voice = nil
	audio.mu.Unlock()
	audio.finishVoice()

	audio.mu.Lock()
	ducked = audio.ducked
	audio.mu.Unlock()
	if ducked {
		t.Error("the music was still ducked after the reply was over")
	}
}

func TestTheWordsAreInTheState(t *testing.T) {
	first := State{
		Connection: "connected",
		Transcript: "what is the time",
	}
	second := State{
		Connection: "connected",
		Transcript: "what is the weather",
	}
	if same(first, second) {
		t.Error("the state is the same when the words are different")
	}

	third := State{
		Connection: "connected",
		Spoken:     "it is half past three",
	}
	fourth := State{
		Connection: "connected",
		Spoken:     "it is half past four",
	}
	if same(third, fourth) {
		t.Error("the state is the same when the answer is different")
	}

	fifth := State{Connection: "connected", SpokenProgress: 0.25}
	sixth := State{Connection: "connected", SpokenProgress: 0.5}
	if same(fifth, sixth) {
		t.Error("the state is the same when the reply has moved on")
	}
}

func TestThePlayerIsIdleAfterTheReply(t *testing.T) {
	audio := &deviceAudio{backend: &fakeBackend{}, log: testLogger()}
	audio.volume = 100

	states := []esphome.MediaPlayerState{}
	audio.onMediaState = func(state esphome.MediaPlayerState) {
		states = append(states, state)
	}

	audio.playReply(make([]byte, 320), false)
	if len(states) == 0 || states[len(states)-1] != esphome.MediaPlayerState_MEDIA_PLAYER_STATE_PLAYING {
		t.Fatalf("the player is not playing while the reply is: %v", states)
	}

	audio.finishVoice()
	if states[len(states)-1] != esphome.MediaPlayerState_MEDIA_PLAYER_STATE_PLAYING {
		t.Errorf("the player went idle before the words were said: %v", states)
	}

	audio.mu.Lock()
	audio.voiceActive = true
	audio.voiceStreaming = false
	audio.voice = nil
	audio.mu.Unlock()
	audio.markReplyPlayedOut()

	if states[len(states)-1] != esphome.MediaPlayerState_MEDIA_PLAYER_STATE_IDLE {
		t.Errorf("the player is %v after the reply, it should be idle", states)
	}
}

func TestTheSpokenProgressFollowsTheReply(t *testing.T) {
	audio := &deviceAudio{backend: &fakeBackend{}, log: testLogger()}
	audio.volume = 100

	reply := make([]byte, voiceRate/10*2)
	for i := 0; i+1 < len(reply); i += 2 {
		reply[i] = 0x10
		reply[i+1] = 0x00
	}

	audio.playReply(reply, false)

	if got := audio.spokenProgress(); got > 0.01 {
		t.Errorf("the progress is %v before anything has been played", got)
	}

	audio.mu.Lock()
	half := len(audio.voice) / 2
	audio.voice = audio.voice[half:]
	audio.mu.Unlock()

	if got := audio.spokenProgress(); got < 0.4 || got > 0.6 {
		t.Errorf("the progress is %v when half the reply has been played", got)
	}

	audio.mu.Lock()
	audio.voice = nil
	audio.mu.Unlock()

	if got := audio.spokenProgress(); got < 0.99 {
		t.Errorf("the progress is %v after the reply was played, it should be all of it", got)
	}
}

func TestTheProgressReachesTheEndWithTheReplyTail(t *testing.T) {
	audio := &deviceAudio{backend: &fakeBackend{}, log: testLogger()}
	audio.volume = 100

	reply := make([]byte, voiceRate/10*2)
	for i := 0; i+1 < len(reply); i += 2 {
		reply[i] = 0x10
		reply[i+1] = 0x00
	}

	audio.playReply(reply, false)
	audio.finishReply()

	audio.mu.Lock()
	queued := len(audio.voice)
	counted := audio.spokenTotal
	audio.mu.Unlock()
	if int64(queued) > counted {
		t.Errorf("%d bytes are queued but only %d were counted", queued, counted)
	}

	audio.mu.Lock()
	audio.voice = nil
	audio.mu.Unlock()

	if got := audio.spokenProgress(); got < 0.99 {
		t.Errorf("the progress is %v after the reply was played, it should be all of it", got)
	}
}

func TestTheProgressIsCompleteWhenTheReplyIsPlayedOut(t *testing.T) {
	audio := &deviceAudio{backend: &fakeBackend{}, log: testLogger()}
	audio.volume = 100

	reply := make([]byte, voiceRate/10*2)
	for i := 0; i+1 < len(reply); i += 2 {
		reply[i] = 0x10
		reply[i+1] = 0x00
	}

	audio.playReply(reply, false)
	audio.finishReply()

	audio.markReplyPlayedOut()

	if got := audio.spokenProgress(); got < 0.99 {
		t.Errorf("the progress is %v after the reply was played out, it should be all of it", got)
	}
}

func TestTheProgressStartsAgainForTheNextReply(t *testing.T) {
	audio := &deviceAudio{backend: &fakeBackend{}, log: testLogger()}
	audio.volume = 100

	reply := make([]byte, voiceRate/10*2)
	for i := 0; i+1 < len(reply); i += 2 {
		reply[i] = 0x10
		reply[i+1] = 0x00
	}

	audio.playReply(reply, false)
	audio.finishReply()
	audio.markReplyPlayedOut()

	audio.playReply(reply, false)

	if got := audio.spokenProgress(); got > 0.01 {
		t.Errorf("the progress is %v at the start of the next reply, it should be nothing", got)
	}
}

func TestTheMuteSilencesTheMicrophone(t *testing.T) {
	audio := &deviceAudio{backend: &fakeBackend{}, log: testLogger()}

	var heard [][]byte
	audio.onWakeAudio = func(pcm []byte) {
		heard = append(heard, append([]byte(nil), pcm...))
	}

	frame := make([]byte, 320)
	for i := range frame {
		frame[i] = 0x40
	}

	audio.pushFrame(frame)
	if len(heard) != 1 {
		t.Fatalf("the wake word heard %d blocks, it should have heard one", len(heard))
	}
	if heard[0][0] == 0 {
		t.Fatalf("the wake word heard silence, it should have heard the room")
	}

	audio.setMuted(true)
	audio.pushFrame(frame)
	if len(heard) != 2 {
		t.Fatalf("the wake word heard %d blocks, it should have heard two", len(heard))
	}
	for _, b := range heard[1] {
		if b != 0 {
			t.Fatalf("a muted microphone heard %v, it should have heard silence", heard[1][:8])
		}
	}
}
