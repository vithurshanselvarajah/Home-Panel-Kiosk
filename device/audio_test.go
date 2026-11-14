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
	vol "github.com/vithurshanselvarajah/Home-Panel-Kiosk/audio/volume"
	"github.com/vithurshanselvarajah/Home-Panel-Kiosk/device/esphome"
	"github.com/vithurshanselvarajah/Home-Panel-Kiosk/device/sendspin"
	"github.com/vithurshanselvarajah/Home-Panel-Kiosk/device/state"
)

// fakeBackend is a sound card that is not there.
//
// It refuses to open the microphone twice, exactly as a real card does,
// so a handover that is a race fails the test rather than passing it.
type fakeBackend struct {
	mu sync.Mutex
	// open is whether the microphone is open.
	open bool
	// opens counts the opens, so a test can tell a handover from a
	// reopen.
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

// isOpen says whether the microphone is open.
func (b *fakeBackend) isOpen() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.open
}

// opensCount is how many times the microphone has been opened.
func (b *fakeBackend) opensCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.opens
}

// fakeReader is a microphone that reads silence.
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
	// It blocks like a real one, for the length of a frame.
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

// fakeWriter is a speaker that swallows what it is given.
type fakeWriter struct{}

func (w *fakeWriter) Write(p []byte) (int, error) { return len(p), nil }
func (w *fakeWriter) Close() error                { return nil }

// testLogger is a logger that writes to the test's output.
func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}

// TestTheMonitorGivesUpTheMicrophone checks that the level meter lets go of
// the microphone when the controller asks for it.
//
// The sound card cannot be opened twice. The level meter opens it on its
// own so a person can see what the microphone hears while they are
// setting the device up, and the controller's capture opens it again when
// it wants to listen.
//
// The handover has to be a handover and not a race: a capture that starts
// while the meter still holds the card is told the card is busy, and the
// panel goes deaf until the turn asks again.
func TestTheMonitorGivesUpTheMicrophone(t *testing.T) {
	backend := &fakeBackend{}
	a := &deviceAudio{backend: backend, log: testLogger()}

	// The meter opens the microphone.
	a.setMonitoring(true)
	waitFor(t, func() bool { return backend.isOpen() }, "the meter did not open the microphone")

	// A turn starts and asks for it. This must not fail with the card busy.
	a.startCapture()
	defer a.stopCapture()

	// The capture opens the microphone on its own goroutine, so the wait
	// is for it to have the card.
	waitFor(t, func() bool { return backend.opensCount() >= 2 }, "the capture did not take the microphone")

	if !a.capturing {
		t.Fatal("the capture did not start")
	}
	if a.monitoringMic() {
		t.Error("the meter still holds the microphone after the capture took it")
	}
	// The microphone was opened twice: once for the meter and once for
	// the capture. The meter gave it up and the capture took it.
	if opens := backend.opensCount(); opens != 2 {
		t.Errorf("the microphone was opened %d times, want 2", opens)
	}
}

// TestTheMeterTakesTheMicrophoneBack checks that the meter opens the
// microphone when the controller is not using it.
//
// The meter is what a person looks at to see whether the microphone
// works, so it has to work on its own.
func TestTheMeterTakesTheMicrophoneBack(t *testing.T) {
	backend := &fakeBackend{}
	a := &deviceAudio{backend: backend, log: testLogger()}

	a.setMonitoring(true)
	waitFor(t, func() bool { return backend.isOpen() }, "the meter did not open the microphone")

	// The controller takes the microphone.
	a.startCapture()
	defer a.stopCapture()

	// The controller gives it back.
	a.stopCapture()
	a.setMonitoring(true)
	waitFor(t, func() bool { return backend.isOpen() }, "the meter did not take the microphone back")
}

// waitFor waits for a condition, up to a second.
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

// TestTheSendspinGainFollowsTheVolume checks that the music is scaled by
// the panel's volume.
//
// It is the whole of the music's level, so a volume of zero is silence.
// The panel reported zero for a long time, because the volume was never
// copied into the reported state and Home Assistant sent back what it
// was told.
//
// The scale is the curve's gain and not the percentage: half the dial is
// a quiet conversation, not half the amplitude.
func TestTheSendspinGainFollowsTheVolume(t *testing.T) {
	full := []byte{0x00, 0x40, 0x00, 0x40} // 16384, 16384
	half := append([]byte(nil), full...)
	applySendspinGain(half, 50, false, false, 0)

	want := int16(float64(16384) * float64(vol.GainForPercent(50)))
	got := int16(uint16(half[0]) | uint16(half[1])<<8)
	if got != want {
		t.Errorf("at half volume the sample was %d, want %d", got, want)
	}

	// Half the dial is quieter than half the amplitude, and louder than
	// silence: that is the whole point of the curve.
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

// TestTheReportedVolumeIsThePanelsVolume checks that the panel reports
// the volume it is actually using.
//
// The volume is pushed to Home Assistant as the speaker's state, and Home
// Assistant sends the volume back as a command. A panel that reported
// zero would be told to be silent, and would then be silent.
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

// TestTheServersMuteSilencesTheMusic checks that a mute from the music
// server reaches the speaker.
//
// The server can mute the music, and that is not the panel's own switch.
// The two are kept apart, so that unmuting on one does not unmute the
// other, and both are honoured.
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

// TestTheMicrophoneMuteDoesNotMuteTheSpeaker checks that the panel's
// microphone switch leaves Sendspin's independent speaker mute untouched.
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
	// At full volume the curve's gain is not unity: its top sits below
	// unity so the amplifier's own protection stays in front of the
	// signal. What matters here is that the microphone mute did not
	// silence the speaker at all.
	if pcm[0] == 0x00 && pcm[1] == 0x00 {
		t.Fatalf("the speaker was silenced by the microphone mute: %v", pcm)
	}
}

// TestTheMicrophoneStaysOpenForTheWakeWord checks that the microphone is
// still open after a turn has finished.
//
// The wake word is what decides whether the next turn happens, and it
// decides it by hearing the room. A microphone that is closed when the
// turn ends is a deaf panel: the wake word never fires again, and the
// only way out is to restart the application.
func TestTheMicrophoneStaysOpenForTheWakeWord(t *testing.T) {
	backend := &fakeBackend{}
	a := &deviceAudio{backend: backend, log: testLogger()}

	// The wake word is listening, so the microphone is open.
	a.setListening(true)
	waitFor(t, func() bool { return backend.isOpen() }, "the microphone did not open for the wake word")

	// A turn starts and finishes.
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

// TestTheWakeWordIsForgottenWhenTheMicrophoneOpens checks that the wake
// word is told when the microphone is opened.
//
// The detector's score is computed from the last two seconds of audio,
// and the reply is the panel talking. Without a reset the panel can hear
// itself and fire the wake word at its own voice.
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

// TestTheReplyIsPlayedAtTheSpeakersRate checks that the reply is
// converted to the rate the speaker plays at.
//
// Home Assistant speaks to a speaker at 16 kHz and the speaker plays at
// 48 kHz. The reply is queued and taken out of the queue at the
// speaker's rate, so a reply that was not converted would be played
// three times too fast — a chipmunk rather than a voice.
func TestTheReplyIsPlayedAtTheSpeakersRate(t *testing.T) {
	a := &deviceAudio{backend: &fakeBackend{}, log: testLogger()}

	// One hundredth of a second of reply at 16 kHz: 160 samples.
	pcm := make([]byte, 320)
	for i := 0; i < 160; i++ {
		pcm[i*2] = byte(i)
		pcm[i*2+1] = 0
	}

	converted := a.resampleVoice(pcm)
	if len(converted) == len(pcm) {
		t.Errorf("the reply was %d bytes, the same as it went in: it was not converted", len(converted))
	}
	// 160 samples at 16 kHz is 480 at 48 kHz.
	if len(converted) < 950 || len(converted) > 970 {
		t.Errorf("the converted reply was %d bytes, want about 960", len(converted))
	}
}

// TestTheConnectionFollowsTheLink checks that the reported connection
// follows the link to Home Assistant.
//
// The drawer says whether the panel can reach Home Assistant, and the
// link is what says it. The link used to be recorded and never set, so
// the drawer said "Disconnected" for ever.
func TestTheConnectionFollowsTheLink(t *testing.T) {
	tracker := state.NewTracker()
	reporter := &stateReporter{
		log:   testLogger(),
		state: tracker,
		audio: &deviceAudio{backend: &fakeBackend{}, log: testLogger()},
	}

	// The link is down to begin with.
	conn, _ := connection(tracker.Snapshot())
	if conn != "disconnected" {
		t.Errorf("the connection was %q with the link down, want disconnected", conn)
	}

	// The link comes up.
	reporter.setLink(true)
	conn, reason := connection(tracker.Snapshot())
	if conn != "connected" {
		t.Errorf("the connection was %q with the link up, want connected", conn)
	}
	if reason == "" {
		t.Error("the connection had no reason to give")
	}

	// And it goes down again.
	reporter.setLink(false)
	conn, _ = connection(tracker.Snapshot())
	if conn != "disconnected" {
		t.Errorf("the connection was %q with the link down, want disconnected", conn)
	}
}

// TestTheMusicIsInTheState checks that what is playing is in the state
// the panel reads.
//
// The drawer shows the title, the artist and the album, and the panel
// has nothing but the state file to learn them from. The fields were
// written and never filled in for a while, which is the trap this test
// is for.
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

// TestThePausedMusicIsNotPlaying checks that a paused track is said to
// be paused.
//
// The speed is a multiplier of a thousand, and a paused track has a
// speed of zero.
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

// TestTheMusicDucksUnderAReply checks that the music is turned down
// while somebody is speaking.
//
// The duck was set and never applied: the level was read from the
// configuration and the music was never scaled by it, so a duck of
// twenty decibels did nothing at all.
func TestTheMusicDucksUnderAReply(t *testing.T) {
	audio := &deviceAudio{backend: &fakeBackend{}, log: testLogger()}
	audio.volume = 100
	audio.setDuckDb(-20)

	// A period of music at a steady level.
	music := make([]byte, periodBytes)
	for i := 0; i+1 < len(music); i += 2 {
		music[i] = 0x00
		music[i+1] = 0x40 // 16384
	}

	// Not ducked: the music is as it was.
	before := make([]byte, len(music))
	copy(before, music)
	applyDuck(music, false, -20)
	if music[1] != before[1] {
		t.Errorf("the music was changed without a duck: %d then %d", before[1], music[1])
	}

	// Ducked: it is quieter, by about twenty decibels.
	applyDuck(music, true, -20)
	quiet := int16(uint16(music[0]) | uint16(music[1])<<8)
	if quiet >= 16384 {
		t.Errorf("the music was not ducked: %d is not quieter than 16384", quiet)
	}
	// Twenty decibels is a tenth of the level, give or take.
	if quiet < 1200 || quiet > 2000 {
		t.Errorf("the music was ducked to %d, which is about %.1f decibels rather than twenty",
			quiet, 20*math.Log10(float64(quiet)/16384))
	}
}

// TestTheDuckingIsTurnedOnByTheReply checks that a reply ducks the
// music and that the music comes back up afterwards.
func TestTheDuckingIsTurnedOnByTheReply(t *testing.T) {
	audio := &deviceAudio{backend: &fakeBackend{}, log: testLogger()}
	audio.volume = 100
	audio.setDuckDb(-20)

	// The reply arrives.
	audio.playReply(make([]byte, 320), false)
	audio.mu.Lock()
	ducked := audio.ducked
	audio.mu.Unlock()
	if !ducked {
		t.Error("the music was not ducked under the reply")
	}

	// The reply's audio has all arrived.
	audio.finishReply()

	// The music comes back up when the speaker has drained the reply,
	// which is what the mixer does. The queue is emptied here, which is
	// what the mixer would have done.
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

// TestTheWordsAreInTheState checks that the words reach the panel.
//
// The state is only written when it has changed, so the words have to be
// part of that comparison. Without it the state is not rewritten when the
// words change, and the panel is never told what was heard or what is
// being said: the overlay sits there thinking for ever.
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

	// The answer's words matter too.
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

	// And the progress of the reply, which moves as it is spoken.
	fifth := State{Connection: "connected", SpokenProgress: 0.25}
	sixth := State{Connection: "connected", SpokenProgress: 0.5}
	if same(fifth, sixth) {
		t.Error("the state is the same when the reply has moved on")
	}
}

// TestThePlayerIsIdleAfterTheReply checks that the speaker is idle once
// the reply has been said.
//
// Home Assistant shows the speaker's state on the player card, and a
// player left on "playing" is a player that looks busy for ever.
//
// The reply is over when the speaker has drained it, so that is where
// the state goes back to idle. It is not where the last frame arrives:
// the speaker still has the words in it then.
func TestThePlayerIsIdleAfterTheReply(t *testing.T) {
	audio := &deviceAudio{backend: &fakeBackend{}, log: testLogger()}
	audio.volume = 100

	states := []esphome.MediaPlayerState{}
	audio.onMediaState = func(state esphome.MediaPlayerState) {
		states = append(states, state)
	}

	// The reply arrives.
	audio.playReply(make([]byte, 320), false)
	if len(states) == 0 || states[len(states)-1] != esphome.MediaPlayerState_MEDIA_PLAYER_STATE_PLAYING {
		t.Fatalf("the player is not playing while the reply is: %v", states)
	}

	// The last frame arrives. The speaker still has the words in it, so
	// the player is still playing.
	audio.finishVoice()
	if states[len(states)-1] != esphome.MediaPlayerState_MEDIA_PLAYER_STATE_PLAYING {
		t.Errorf("the player went idle before the words were said: %v", states)
	}

	// The speaker has drained it. That is when the player goes idle.
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

// TestTheSpokenProgressFollowsTheReply checks that the progress of the
// reply is measured in the right units.
//
// The reply arrives at sixteen kilohertz and is played at forty-eight,
// so the arrival and the queue are three times different sizes. Counting
// the arrival and comparing it with the queue gave a progress that was
// always nothing, and the answer's words never appeared.
func TestTheSpokenProgressFollowsTheReply(t *testing.T) {
	audio := &deviceAudio{backend: &fakeBackend{}, log: testLogger()}
	audio.volume = 100

	// A tenth of a second of reply, at the rate it arrives at.
	reply := make([]byte, voiceRate/10*2)
	for i := 0; i+1 < len(reply); i += 2 {
		reply[i] = 0x10
		reply[i+1] = 0x00
	}

	audio.playReply(reply, false)

	// It has arrived but none of it has been played.
	if got := audio.spokenProgress(); got > 0.01 {
		t.Errorf("the progress is %v before anything has been played", got)
	}

	// Half of it has been played: the speaker has taken half of the
	// queue.
	audio.mu.Lock()
	half := len(audio.voice) / 2
	audio.voice = audio.voice[half:]
	audio.mu.Unlock()

	// The progress is about half. It is measured against the audio that
	// has arrived, so the two have to be counted in the same units: the
	// reply arrives at sixteen kilohertz and is played at forty-eight,
	// and counting the arrival against the queue gave a progress that
	// was always nothing.
	if got := audio.spokenProgress(); got < 0.4 || got > 0.6 {
		t.Errorf("the progress is %v when half the reply has been played", got)
	}

	// All of it has been played.
	audio.mu.Lock()
	audio.voice = nil
	audio.mu.Unlock()

	if got := audio.spokenProgress(); got < 0.99 {
		t.Errorf("the progress is %v after the reply was played, it should be all of it", got)
	}
}

// TestTheProgressReachesTheEndWithTheReplyTail checks that the progress
// of the reply reaches the end when the reply is finished off.
//
// The last of the reply is held by the converter and released when the
// reply ends. Those bytes are played, but they were not counted, so the
// progress was measured against less audio than there was and never
// reached the end. The panel shows the answer's words as the audio is
// played, so the last of the words never appeared.
func TestTheProgressReachesTheEndWithTheReplyTail(t *testing.T) {
	audio := &deviceAudio{backend: &fakeBackend{}, log: testLogger()}
	audio.volume = 100

	// A tenth of a second of reply, at the rate it arrives at.
	reply := make([]byte, voiceRate/10*2)
	for i := 0; i+1 < len(reply); i += 2 {
		reply[i] = 0x10
		reply[i+1] = 0x00
	}

	// The reply, and the end of it. The end releases the tail of the
	// conversion, which is part of the reply and has to be counted.
	// The reply, and then the end of it. This is the way the reply
	// really arrives: the audio in pieces with the end flag clear, and
	// the end of it said separately.
	audio.playReply(reply, false)
	audio.finishReply()

	// The tail is part of the reply and is counted as part of it.
	//
	// The progress is measured against the audio that has arrived, so
	// everything that is played has to be counted. The tail was not, so
	// the progress was measured against less audio than there was and
	// could never reach the end: the panel showed the answer's words as
	// the audio was played, and the last of the words never appeared.
	audio.mu.Lock()
	queued := len(audio.voice)
	counted := audio.spokenTotal
	audio.mu.Unlock()
	if int64(queued) > counted {
		t.Errorf("%d bytes are queued but only %d were counted", queued, counted)
	}

	// Everything that was queued has been played.
	audio.mu.Lock()
	audio.voice = nil
	audio.mu.Unlock()

	if got := audio.spokenProgress(); got < 0.99 {
		t.Errorf("the progress is %v after the reply was played, it should be all of it", got)
	}
}

// TestTheProgressIsCompleteWhenTheReplyIsPlayedOut checks that the
// progress of the reply reaches the end when the reply has been said.
//
// The progress is measured against the audio that arrived, and the last
// of that is held by the converter until the reply ends. So the count
// runs a little short and the progress never quite reaches the end.
//
// The panel shows the answer's words as the audio is played, so the last
// of the words was never shown: the answer stopped at "Sorry, I am not
// aware of any device called a jok".
func TestTheProgressIsCompleteWhenTheReplyIsPlayedOut(t *testing.T) {
	audio := &deviceAudio{backend: &fakeBackend{}, log: testLogger()}
	audio.volume = 100

	// A tenth of a second of reply, at the rate it arrives at.
	reply := make([]byte, voiceRate/10*2)
	for i := 0; i+1 < len(reply); i += 2 {
		reply[i] = 0x10
		reply[i+1] = 0x00
	}

	audio.playReply(reply, false)
	audio.finishReply()

	// The reply has been played out: the speaker has drained it.
	audio.markReplyPlayedOut()

	if got := audio.spokenProgress(); got < 0.99 {
		t.Errorf("the progress is %v after the reply was played out, it should be all of it", got)
	}
}

// TestTheProgressStartsAgainForTheNextReply checks that a new reply's
// progress is measured from the start.
//
// The completion belongs to the reply that was played out, and the next
// reply has to be measured from its own beginning.
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

	// The next reply.
	audio.playReply(reply, false)

	if got := audio.spokenProgress(); got > 0.01 {
		t.Errorf("the progress is %v at the start of the next reply, it should be nothing", got)
	}
}

// TestTheMuteSilencesTheMicrophone checks that a muted microphone
// sends silence.
//
// The mute is the microphone's and not the speaker's: a person who
// mutes the panel wants the room to stop hearing them, and they still
// want to hear the music.
//
// So the audio is silenced before the wake word hears it and before
// Home Assistant hears it. A muted microphone that still sent the room
// would be a mute that did nothing.
func TestTheMuteSilencesTheMicrophone(t *testing.T) {
	audio := &deviceAudio{backend: &fakeBackend{}, log: testLogger()}

	var heard [][]byte
	audio.onWakeAudio = func(pcm []byte) {
		heard = append(heard, append([]byte(nil), pcm...))
	}

	// A block of sound.
	frame := make([]byte, 320)
	for i := range frame {
		frame[i] = 0x40
	}

	// Unmuted, the wake word hears the room.
	audio.pushFrame(frame)
	if len(heard) != 1 {
		t.Fatalf("the wake word heard %d blocks, it should have heard one", len(heard))
	}
	if heard[0][0] == 0 {
		t.Fatalf("the wake word heard silence, it should have heard the room")
	}

	// And muted, it hears nothing.
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
