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
	"time"

	"github.com/vithurshanselvarajah/Home-Panel-Kiosk/device/esphome"
	"github.com/vithurshanselvarajah/Home-Panel-Kiosk/device/sendspin"

	"github.com/vithurshanselvarajah/Home-Panel-Kiosk/audio"
	"github.com/vithurshanselvarajah/Home-Panel-Kiosk/audio/mic"
	"github.com/vithurshanselvarajah/Home-Panel-Kiosk/audio/speaker"
	vol "github.com/vithurshanselvarajah/Home-Panel-Kiosk/audio/volume"
)

// audioBackend is the sound card, as the device uses it.
//
// It is an interface rather than the concrete type so the handover
// between the level meter and the controller's capture can be tested
// without a sound card — the handover is the part that goes wrong, and
// it goes wrong on a machine that has one.
type audioBackend interface {
	// Capture opens the microphone.
	Capture(ctx context.Context, rate int) (io.ReadCloser, error)
	// Playback opens the speaker.
	Playback(ctx context.Context, rate int) (io.WriteCloser, error)
	// OpenClockedOutput opens a speaker that reports when each period
	// reaches the sound card.
	OpenClockedOutput(ctx context.Context, rate, channels int) (audio.ClockedOutput, error)
	// SelectedAudioDevices reports the devices in use.
	SelectedAudioDevices() (string, string)
	// SelectAudioDevices chooses the devices.
	SelectAudioDevices(input, output string) error
	// ChooseDefaultInput picks a microphone that works, for when nobody
	// has chosen one.
	ChooseDefaultInput() error
}

// deviceAudio is the device's audio: the microphone it listens with, the
// speaker it answers through, and the music that plays under a reply.
//
// It is one object because the three share one sound card and one device's
// worth of state. The voice and the music go out through the same speaker,
// so they are mixed rather than played one after the other — a reply that
// waited for the music to stop would be late answering.
type deviceAudio struct {
	backend audioBackend
	log     *slog.Logger
	// onSendMicAudio is called with the microphone frames, so they can be
	// sent to home assistant while a turn is running.
	onSendMicAudio func(pcm []byte)
	// onWakeAudio is called with every block of captured audio, so the
	// wake word can score it.
	//
	// Every block, not just the ones in a turn: the wake word is what
	// decides whether a turn starts, so it has to hear the room all the
	// time. The gate inside the wake word is what keeps that cheap.
	onWakeAudio func(pcm []byte)
	// onWakeReset is called when the microphone is opened, so the wake
	// word can forget the audio of the turn that just finished.
	//
	// The detector's score is computed from the last two seconds of
	// audio, and the reply is the panel talking. Without a reset the
	// panel can hear itself.
	onWakeReset func()
	// onSendspinStatus is called when the music player's status changes.
	onSendspinStatus func(status sendspin.Status)
	// onSendspinStarted is called when the music player has started.
	//
	// The player's identity goes in the device's capabilities, and it
	// is not known until the player has started. Home Assistant reads it
	// from there and passes it to the music server, which pairs with
	// the player using the token the device hands out.
	onSendspinStarted func()
	// onVolume is called when the panel's volume changes, so the speaker's
	// entity can report it.
	onVolume func(volume float32)
	// onSystemVolume is called when the panel's volume changes, so the
	// system mixer can be set to match.
	//
	// The mixer is the application’s business — it is what the person
	// means by the volume — so the panel asks for it rather than
	// reaching for it itself.
	onSystemVolume func(level int)
	// onPanelsVolume is called when the volume changes, so the panels'
	// sliders follow it.
	//
	// The level is the device program's, so the panels are told where it
	// is rather than each keeping its own idea of it.
	onPanelsVolume func(level int)
	// onReplyEnded is called when the reply has been played out.
	//
	// The reply is over when the speaker has drained it, which is the
	// only moment that is true: the last frame arriving is not the end,
	// because the speaker still has the words in it.
	onReplyEnded func()
	// onMediaState is called when the speaker changes what it is doing.
	onMediaState func(state esphome.MediaPlayerState)
	// onMusicStarted is called when one of the music sources starts.
	//
	// It says which: "home-assistant" for the music Home Assistant is
	// playing and "server" for the music a music server is playing.
	//
	// The two take turns, and this is how they tell each other.
	onMusicStarted func(source string)
	// mediaState is what the music Home Assistant is playing is doing.
	//
	// It is kept so the panel can show it, and it is the panel's own
	// music as opposed to the music server's.
	mediaState esphome.MediaPlayerState
	// sendspinPlaying is whether the music server's music is playing.
	//
	// It is kept so the turn is only handed over once, when the music
	// starts and not on every reading of the status.
	sendspinPlaying bool
	// onAnnounceFinished is called when an announcement has been played
	// out, so that Home Assistant can be told.
	onAnnounceFinished func()
	// credentialsDir is where the music player keeps its identity.
	credentialsDir string

	mu sync.Mutex
	// capture is the open microphone, if there is one.
	capture io.ReadCloser
	// captureCancel stops the capture goroutine.
	captureCancel context.CancelFunc
	// capturing is whether a capture is running.
	capturing bool
	// monitoring is whether the level meter was asked for.
	//
	// It is asked for by the settings page, and the meter opens the
	// microphone for it when the controller is not using it. The
	// controller's capture takes the microphone back when it needs it, and
	// the meter gives it up — but the meter is wanted again as soon as
	// the controller has finished, so the request is kept.
	monitoring bool
	// monitorCancel stops the monitored capture.
	monitorCancel context.CancelFunc
	// monitorDone is closed when the monitor has let go of the microphone.
	monitorDone chan struct{}
	// testSound is the counter that says a tone should be played.
	testSound int
	// testSoundSeen is whether a counter has arrived, so the first one is
	// adopted rather than played.
	testSoundSeen bool
	// listening says the wake word is listening, so the microphone stays
	// open.
	//
	// The wake word has to hear the room all the time: it is what decides
	// whether a turn starts, so a microphone that opens only when a turn
	// is already running could never hear one.
	//
	// The gate inside the wake word is what keeps that affordable — it
	// scores only the frames that are loud enough to be somebody talking.
	listening bool
	// streaming is whether the microphone frames are being sent to home
	// assistant.
	//
	// They are sent only while a turn is running. The panel does not
	// decide when the utterance is over — home assistant does, with its
	// own voice activity detection — so the turn's end is home
	// assistant's message, and the capture is open for exactly that
	// long.
	streaming bool

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
	clocked audio.ClockedOutput
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
	// voiceAnnouncement is whether the voice is an announcement rather
	// than a reply.
	//
	// Home Assistant waits for the announcement to be finished before it
	// goes on, so the end of an announcement is reported to it and the
	// end of a reply is not.
	voiceAnnouncement bool
	// voiceEnded is called when the reply has been played out.
	//
	// The end of the stream is not the end of the reply: the last of the
	// words is said after the last frame has arrived. The mixer is what
	// knows when the reply is really over.
	voiceEnded func()
	// voiceStarted is called when the reply's first audio arrives.
	//
	// The panel's overlay is drawn from this, so the screen has to know
	// a reply is coming before the words are there.
	voiceStarted func()
	// spokenTotal is how many bytes of reply have arrived.
	//
	// It is what the spoken progress is measured against: the audio
	// that has arrived less the audio that has been played is how far
	// along the reply is.
	spokenTotal int64
	// spokenComplete says the reply has been played out.
	//
	// It is the last word on the reply's progress: once the speaker has
	// drained the reply, the reply has been said, whatever the count of
	// the bytes says. The count is of the audio that arrived, and the
	// last of that is held by the converter, so the count runs a little
	// short of the reply and the progress never quite reaches the end.
	spokenComplete bool
	// voiceResampler converts the reply to the rate the speaker plays at.
	//
	// The reply arrives at the rate Home Assistant speaks at, which is
	// not the rate the speaker runs at, and the mixer takes the reply out
	// of the queue at the speaker's rate. Without the conversion the
	// reply is played three times too fast, which is a chipmunk rather
	// than a voice.
	voiceResampler *audio.Resampler

	// media is the music Home Assistant is playing, and the mixer takes
	// it out of here.
	//
	// The music is a stream that is fetched while it is played, so it
	// has its own queue: a reply must not be able to take the music
	// out of the speaker's hands.
	media []byte
	// mediaPaused is whether the music is paused.
	//
	// A paused track is not playing, so the mixer takes nothing out of
	// the queue and the fetch stops as well: the audio that has already
	// arrived stays where it is until the track is played again.
	mediaPaused bool
	// mediaActive is whether there is music to play.
	//
	// It stays true until the track is stopped or the queue is empty,
	// which is after the fetch has finished: the end of the fetch is
	// not the end of the music, because the speaker has not played it
	// all yet.
	mediaActive bool
	// mediaPlaying is whether the speaker has been opened for the music.
	//
	// The speaker is not opened until the queue has a buffer's worth of
	// music in it, so the fetch knows to keep filling rather than
	// waiting for room.
	mediaPlaying bool
	// mediaStop tells the fetch to give up.
	//
	// It is closed when the track is stopped, and the fetch returns.
	mediaStop chan struct{}
	// mediaResumedCh is closed while the music is not paused.
	//
	// The fetch waits on it, so a paused track stops being read. It is
	// closed and made again when the pause changes.
	mediaResumedCh chan struct{}

	// volume is the device's volume, 0 to 100.
	volume int
	// level is the microphone's level, as a percentage of full scale.
	level int
	// muted is whether the device's own mute switch is on.
	muted bool
	// ducked is whether the music is ducked under a reply.
	//
	// It is turned on while a reply is playing and off when the reply is
	// over, so the music comes back up afterwards.
	ducked bool
	// duckDB is how far the music ducks, in decibels.
	duckDB float64
	// wakeThresholdValue is how sensitive the wake word is.
	wakeThresholdValue float64
	// wakeWordEnabled is whether the wake word is listening.
	//
	// It is a copy of the wake word's own answer, kept so the audio can
	// be asked without reaching for the wake word. It starts on, because
	// the wake word starts on: a copy that started off would say the
	// wake word was off on a panel that was listening.
	wakeWordEnabled bool

	// sendspin is the music player.
	sendspin *sendspin.Client
	// sendspinName is the name the player was started with.
	sendspinName string
	// sendspinUnpaired is whether the player accepts an unpaired server.
	sendspinUnpaired bool
	// sendspinMuted is whether the music player was muted by its server.
	sendspinMuted bool
	// sendspinStatus is the music player's own account of what it is
	// doing.
	sendspinStatus sendspin.Status
	// sendspinPump is the goroutine that watches for music and opens the
	// speaker for it.
	sendspinPump context.CancelFunc

	// endpoints are the audio devices the machine has.
	inputs  []audio.Endpoint
	outputs []audio.Endpoint

	// speakerCancel stops the ported speaker's loop.
	speakerCancel context.CancelFunc

	// micBridge is the microphone, as the ported audio stack holds it: it
	// opens the card itself, cancels the panel's own voice out of what the
	// microphones hear, levels and cleans the speech, and pushes frames to
	// whoever is listening.
	micBridge *micBridge

	// speakerPlayer is the speaker, as the ported audio stack holds it: it
	// is the mixer, and it writes to the card.
	speakerPlayer *speaker.Player
	// musicSource is the music, as a source the speaker pulls from.
	musicSource *musicSource

	// mute is the microphone's mute, when the board has a hardware latch
	// for it.
	//
	// The latch is the source of truth: it is what the button on the
	// unit moves, and it is what says whether the microphones are cut.
	mute *muteController
}

// The rates. The controller expects captured audio at 16 kHz and sends
// audio at 48 kHz.
//
// The Echo Show 8's speaker is run at 96 kHz, not 48 kHz, and the
// difference is not a preference. The amplifier holds a tuning that was
// made for 96 kHz: its equaliser and its limiter are set for that rate,
// and at 48 kHz every one of those corners sits an octave low, so the
// tone is wrong and the protection that should be catching the peaks is
// not catching them. The rate the card is opened at is therefore the
// amplifier's, and everything else is converted to it.
const (
	captureRate  = audio.CaptureRate
	playbackRate = audio.PlaybackRate
)

// athenaRate is the rate the Echo Show 8's speaker runs at.
//
// The amplifier's tuning was made for it, so it is the rate the card is
// opened at and the rate everything is converted to on the way out.
const athenaRate = 96000

// cardRate is the rate the sound card is opened at.
//
// On the Echo Show 8 that is the amplifier's own rate, 96 kHz, because
// the tuning inside the amplifier was made for it: at 48 kHz every
// corner of its equaliser and its limiter sits an octave low, so the
// tone is wrong and the protection that should be catching the peaks is
// not catching them.
//
// The mix is still made at playbackRate and converted on the way out,
// because nothing upstream needs the higher rate.
const cardRate = athenaRate

// upsampleTaps is the length of each phase of the filter that takes the
// mix up to the card's rate: 96 taps at 96 kHz, flat to 21 kHz and about
// 74 dB down on the images from 27 kHz. 1 ms of delay.
const upsampleTaps = 48

// voiceRate is the rate the reply arrives at.
//
// Home Assistant speaks to a speaker at 16 kHz, whatever the speaker
// itself runs at, and the reply has to be converted before it is played
// or it is heard at the wrong speed.
const voiceRate = 16000

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
	// The mute is the latch's, when the board has one. The latch is what
	// the button on the unit moves, so what it says is what is reported.
	if a.mute != nil {
		s.Muted = a.mute.Get()
	} else {
		s.Muted = a.muted
	}
	// The music player's own state. It says whether a server is
	// connected, whether the player is in step with it, how much music
	// is queued, and what is playing.
	//
	// It is the first thing to look at when the music is not playing: a
	// player that is connected but not in step has the music and
	// cannot play it.
	//
	// The metadata is the music itself: the title, the artist, the
	// album, the artwork and where the track has got to. The panel's
	// drawer shows it, so it is part of the state.
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
	// The speed is a multiplier of a thousand, so a thousand is playing
	// and anything else is not.
	s.MusicPlaying = music.Metadata.Speed >= 1000
	s.MusicTransport = music.Transport
	// The music Home Assistant is playing is the panel's own, and the
	// panel shows it beside the music server's.
	s.MediaState = mediaStateName(a.mediaState)
	// The volume is the panel's own, and it is reported so that Home
	// Assistant shows the right level on the speaker's card.
	//
	// It has to be reported: the volume is pushed to Home Assistant as
	// the speaker's state, and Home Assistant sends the volume back as a
	// command. A panel that reported zero would be told to be silent.
	s.Volume = a.volume
}

// selectInput chooses the microphone.
//
// An empty name means the system's own default, which is what the device
// uses before anybody has chosen anything.
//
// The system's default is not always a microphone that works: on the
// Echo Show 8 the card has a dummy in front of the real microphone
// array, and the dummy is what the system default points at. So the
// default is not taken on trust: the devices are tried and the first
// one that opens is used.
func (a *deviceAudio) selectInput(name string) error {
	if name == "" {
		if err := a.backend.ChooseDefaultInput(); err != nil {
			return err
		}
		return nil
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
// microphone is already open. The level meter reads the frames the
// capture produces, because a sound card cannot be opened twice — ALSA
// says so by taking the process down — and because the level of the
// frames being sent is the level that matters.
//
// The microphone is opened for the meter alone when nothing else wants
// it, so a person can see what the microphone hears while they are
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

// stopMonitor closes the monitored microphone.
//
// It does not forget that the meter was asked for. The controller's
// capture takes the microphone back when it needs it, and the meter gives
// it up — but the meter is wanted again as soon as the controller has
// finished, so the request is kept and the meter is reopened when the
// microphone is free.
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
	// The wait is outside the lock on purpose. The monitor's own cleanup
	// takes the lock, so waiting for it while holding it would be a
	// deadlock: the monitor would be waiting for the lock and this would
	// be waiting for the monitor.
	if done != nil {
		<-done
	}
}

// startMonitor opens the microphone for the level meter alone.
//
// It is only called when the controller is not already using the
// microphone. When it is, the level comes from the frames the controller's
// capture produces, because a sound card cannot be opened twice.
//
// The microphone is opened before this returns, and not on another
// goroutine. The handover has to be ordered: the controller asks for the
// microphone at some moment, and the meter has to have let go of it
// before that moment. A meter that opens the card later finds the card
// already taken by the capture, and the capture fails with the card
// busy.
func (a *deviceAudio) startMonitor() {
	a.mu.Lock()
	if a.monitorCancel != nil || a.capturing {
		a.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	a.monitorCancel = cancel
	// The monitor says when it has let go of the sound card, so whoever
	// asked for the microphone can wait for it rather than asking while
	// the card is still held.
	finished := make(chan struct{})
	a.monitorDone = finished
	a.mu.Unlock()

	reader, err := a.backend.Capture(ctx, captureRate)
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

	go func() {
		defer func() {
			// The microphone is released BEFORE the monitor says it has
			// let go. The other way round the caller would ask for the
			// microphone while the meter still had it, and be told the
			// card was busy.
			_ = reader.Close()
			a.mu.Lock()
			a.monitorCancel = nil
			a.monitorDone = nil
			a.mu.Unlock()
			cancel()
			// The channel is closed last, so a caller waiting for the
			// microphone to be released is woken by the release rather
			// than by the bookkeeping.
			close(finished)
		}()

		// The microphone is closed as soon as the monitor is cancelled,
		// rather than when the next read returns. A read blocks in the
		// sound card until the next frame arrives, and whoever asked for
		// the microphone is waiting for the card to be free.
		go func() {
			<-ctx.Done()
			_ = reader.Close()
		}()

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

// setTestSound plays a tone when the counter changes.
//
// The counter is what says whether the tone should play: a button that is
// pressed twice has to make two sounds, and a flag cannot tell the
// difference between the first press and the second.
//
// The first counter the device is given is adopted rather than acted on.
// It is the counter the application had when it started, and a tone is
// only wanted when somebody asks for one — a counter that arrived with
// the configuration is a tone from a previous run.
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

// setVolume sets the panel's volume.
//
// There is one volume, and the speaker owns it. The speaker's level is
// what Home Assistant shows, the panel's own gain is what the music and
// the reply are played at, the music server is told, and the panels are
// told so their sliders follow.
//
// They are one setting seen from four places, so they are set together:
// a panel whose speaker says one thing and whose music plays at another
// is a panel nobody can reason about.
//
// The mixer is deliberately not among them. The level is applied in one
// place — in the samples, on a curve — and the mixer is left where the
// card's driver put it. A level applied in two places is a level nobody
// can reason about: the same slider position sounds different depending
// on which path the audio took.
//
// The level is a percentage, 0 to 100. The protocol's media player uses
// a fraction, so that is converted at the boundary rather than kept in
// two forms.
func (a *deviceAudio) setVolume(level int) {
	level = clampVolume(level)

	a.mu.Lock()
	a.volume = level
	player := a.sendspin
	a.mu.Unlock()

	if player != nil {
		// The player is told, so the server knows what the panel is
		// playing at.
		player.SetVolume(level)
	}

	// The speaker applies its own volume, on the vendor's curve, so the
	// level is given to it in its own steps.
	//
	// The panel's level is a percentage and the speaker's is a step on a
	// curve of thirty, so the percentage is scaled onto the steps. The
	// curve is what makes the top of the dial room-loud rather than
	// painful, and it is applied after the vendor's tuning.
	if spk := a.portedSpeaker(); spk != nil {
		spk.SetVolume(speakerStep(level))
	}

	// The speaker's own state is told, so Home Assistant's slider shows
	// the level the panel is actually using.
	if a.onVolume != nil {
		a.onVolume(float32(level) / 100)
	}

	// The panels are told, so their sliders follow the level rather than
	// each keeping its own idea of it.
	if a.onPanelsVolume != nil {
		a.onPanelsVolume(level)
	}
}

// sendspinPlayer returns the music player, when there is one.
//
// It is what the device's capabilities describe, so that a music server
// can tell that this player is the one Home Assistant already knows.
func (a *deviceAudio) sendspinPlayer() *sendspin.Client {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.sendspin
}

// mediaStateName is what the panel's own music is doing, in words.
//
// The protocol's own names are long and they are the protocol's, and
// the panel draws them rather than reading them.
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

// wakeThreshold is how sensitive the wake word is.
//
// It is the score the wake word has to reach, as a fraction of the
// scale.
func (a *deviceAudio) wakeThreshold() float64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.wakeThresholdValue
}

// wakeEnabled is whether the wake word is listening.
func (a *deviceAudio) wakeEnabled() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.wakeWordEnabled
}

// duckLevel is how far the music is turned down under a reply.
func (a *deviceAudio) duckLevel() float64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.duckDB
}

// mutedNow is whether the microphone is muted.
//
// The hardware latch is the source of truth when the board has one, so
// the button on the unit is reflected here without anything having to
// ask for it.
func (a *deviceAudio) mutedNow() bool {
	if a.mute != nil {
		return a.mute.Get()
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.muted
}

// speaking says whether the panel is playing a reply.
//
// It is what the wake word's bar follows: while the panel is speaking,
// the bar is dropped so that a person can interrupt it.
func (a *deviceAudio) speaking() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.voiceActive
}

// currentVolume returns the panel's volume level.
func (a *deviceAudio) currentVolume() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.volume
}

// setDuckDb sets how far the music is turned down under a reply.
//
// The level is in decibels, and it is negative: the music is quieter
// than it would be, not louder.
func (a *deviceAudio) setDuckDb(db float64) {
	a.mu.Lock()
	a.duckDB = db
	a.mu.Unlock()
}

// setWakeThreshold sets how sensitive the wake word is.
func (a *deviceAudio) setWakeThreshold(threshold float64) {
	a.mu.Lock()
	a.wakeThresholdValue = threshold
	a.mu.Unlock()
}

// setWakeEnabled switches the wake word on or off.
func (a *deviceAudio) setWakeEnabled(enabled bool) {
	a.mu.Lock()
	a.wakeWordEnabled = enabled
	a.mu.Unlock()
}

// setMuted turns the microphone's mute on or off.
//
// On the Echo Show 8 the mute is a hardware latch, and the latch is asked
// to move: what it says afterwards is what the application uses, because
// the latch is the source of truth and the button on the unit is the same
// latch.
//
// On a board with no latch the software mute stands on its own, and the
// microphone's frames are silenced before anything sees them.
//
// It is the microphone's and not the speaker's. A person who mutes the
// panel wants the room to stop hearing them, and they still want to hear
// the music.
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

// stepVolume moves the level one step along the curve.
//
// The buttons move the level by a step rather than by a number of
// decibels, so a press is the same size of step wherever the level is:
// the curve is spread over its thirty steps, and a step is a step.
//
// The level is a percentage, and the step is turned back into one.
func (a *deviceAudio) stepVolume(delta int) {
	a.mu.Lock()
	level := a.volume
	a.mu.Unlock()

	step := vol.Step(level) + delta
	a.setVolume(vol.Percent(step))
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
func (a *deviceAudio) applyConfig(c Config) {
	if c.DuckDb != nil {
		a.mu.Lock()
		a.duckDB = float64(*c.DuckDb)
		a.mu.Unlock()
	}
	a.applySendspinConfig(c)
}

// startCapture opens the microphone and streams it to the controller.
//
// The stream begins when the controller asks for it and stops when it says
// to stop, which is the order the protocol expects: a device that streams
// before it is asked is a device streaming to nobody.
//
// The microphone is opened once. The level meter reads the same frames
// rather than opening a second capture, because a sound card cannot be
// opened twice and ALSA says so by taking the process down.
func (a *deviceAudio) startCapture() {
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
	a.streaming = false
	ctx, cancel := context.WithCancel(context.Background())
	a.captureCancel = cancel
	a.mu.Unlock()

	// The wake word forgets what it heard before the microphone was
	// opened, because the audio of the turn that just finished is still
	// in its window and the reply is the panel talking.
	if a.onWakeReset != nil {
		a.onWakeReset()
	}

	a.log.Info("the microphone is opening")

	// The microphone is opened before this returns, so a failure is
	// reported here rather than on another goroutine, and so the caller
	// knows the capture has the card.
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
		// restart says the microphone stopped on its own rather than
		// being closed by whoever wanted it closed.
		restart := false
		defer func() {
			a.mu.Lock()
			a.capturing = false
			a.captureCancel = nil
			a.capture = nil
			wantMonitor := a.monitoring
			a.mu.Unlock()
			cancel()
			// The level meter takes the microphone back when the
			// controller has finished with it, because it was asked for
			// and there is nothing else to do with it.
			if wantMonitor {
				a.startMonitor()
			}
			// The microphone is opened again after the capture has been
			// cleaned up, because the capture has to be closed before
			// the sound card can be opened again.
			if restart {
				a.restartCapture()
			}
		}()

		defer reader.Close()

		// 100 milliseconds of audio at 16 kHz is 3200 bytes, which is one
		// of the capture frames' natural size.
		buf := make([]byte, captureRate/10*2)
		for {
			n, err := reader.Read(buf)
			if n > 0 {
				// The level is measured here rather than by the
				// wake word's gate, because the gate only sees the
				// frames it decides to score. A meter that only
				// moves during a turn is a meter that is dead the
				// rest of the time.
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

// pushEchoReference tells the echo canceller what the speaker is playing.
//
// The reference is the period as it goes to the card, stamped with when
// its last frame will be heard. The microphones take out what was heard
// over the stretch of time each of their reads covers.
//
// Without a clock the time is a guess, and a guess that is late misses
// the start of every echo — so the clocked output is asked when its next
// frame reaches the speaker, and the guess is only used when there is
// none.
func (a *deviceAudio) pushEchoReference(stereo []byte, clocked audio.ClockedOutput) {
	// The ported speaker feeds its own echo reference: it knows what it
	// sent the card and when that will be heard, and the microphone's
	// canceller takes it out of what the microphones hear.
	//
	// On a board whose card has a write back of what it plays, the
	// reference is the card's own copy, which is locked to the microphones
	// by the card's clock. Where there is none, the speaker's software
	// reference is used, which is what this used to do by hand.
}

// openMicrophone opens the microphone and returns it as the wake word wants to hear it.
//
// On the Echo Show 8 the card hands the microphones over at 96 kHz as
// four channels of 32-bit samples, and the card will not open for
// anything else. So the capture is opened in that format and the frames
// are put through the pipeline: the channels are averaged, the rate is
// brought down to 16 kHz, the panel's own voice is cancelled, and the
// speech is levelled and cleaned.
//
// On any other board the microphone is opened as it always was: at the
// rate the controller asks for, in the format the card offers.
func (a *deviceAudio) openMicrophone(ctx context.Context) (io.ReadCloser, error) {
	bridge, err := a.micBridgeFor(ctx)
	if err != nil {
		return nil, err
	}
	frames, stop := bridge.frames("device")
	return &micReader{bridge: bridge, frames: frames, stop: stop}, nil
}

// micBridgeFor opens the microphone through the ported stack.
func (a *deviceAudio) micBridgeFor(ctx context.Context) (*micBridge, error) {
	a.mu.Lock()
	bridge := a.micBridge
	a.mu.Unlock()
	if bridge != nil {
		return bridge, nil
	}

	// The device program's own backend holds the card, so that a
	// recording can stand in for it; the microphone is given that to
	// read.
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

// micReader is the microphone, handing on the frames the wake word hears.
//
// The frames come from the ported stack already mixed, cancelled, levelled
// and cleaned, at the rate the wake word wants them.
type micReader struct {
	bridge *micBridge
	frames <-chan []int16
	stop   func()
	closed bool
}

// Read reads one block of the wake word's audio.
func (m *micReader) Read(p []byte) (int, error) {
	if m.closed {
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

// Close releases the microphone.
func (m *micReader) Close() error {
	m.closed = true
	if m.stop != nil {
		m.stop()
	}
	return nil
}

// restartCapture opens the microphone again after it has stopped.
//
// The capture is what the wake word hears through, so a microphone that
// has stopped is a panel that has gone deaf for ever: the wake word
// never fires again and the only way out is to restart the application.
//
// A microphone that stops on its own is not unusual — the card can be
// taken by another program, or the sound server can restart — and the
// panel should not need restarting for it. So the capture is opened
// again, and the wake word is told, so it forgets the audio from
// before the gap.
//
// It is only reopened when something still wants it. A panel whose wake
// word has been switched off does not hold the microphone open.
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
}

// pushFrame sends one block of captured audio.
//
// The frames go to home assistant only while a turn is running. The rest
// of the time they are measured for the level meter and dropped.
func (a *deviceAudio) pushFrame(pcm []byte) {
	a.mu.Lock()
	streaming := a.streaming
	muted := a.muted
	a.mu.Unlock()

	// The hardware latch, when the board has one, is the truth about the
	// microphone. The board has already cut the microphones, so the
	// frames are silence anyway; silencing them here as well means
	// nothing downstream has to know that.
	if a.mute != nil {
		muted = a.mute.Get()
	}

	// A muted microphone hears nothing.
	//
	// The mute is the microphone's, not the speaker's: a person who
	// mutes the panel wants the room to stop hearing them, and they
	// still want to hear the music.
	//
	// So the audio is silenced before anything sees it: the wake word
	// does not fire and home assistant hears silence.
	if muted {
		pcm = make([]byte, len(pcm))
	}

	// The wake word hears every block. It is what decides whether a turn
	// starts, so it has to hear the room all the time — and its gate is
	// what keeps that cheap.
	if a.onWakeAudio != nil {
		a.onWakeAudio(pcm)
	}

	// The audio goes to home assistant, over the ESPHome connection.
	//
	// It only goes at all while a turn is running. A panel that streams
	// the room continuously is a panel that uploads the room, and the
	// wake word is local precisely so that the room stays in the room.
	if streaming && a.onSendMicAudio != nil {
		a.onSendMicAudio(pcm)
	}
}

// setListening opens or closes the microphone for the wake word.
//
// The microphone is open for as long as the wake word is listening, which
// is as long as the panel is switched on. The gate inside the wake word is
// what keeps that affordable: it scores only the frames that are loud
// enough to be somebody talking, so a silent room costs almost nothing.
func (a *deviceAudio) setListening(listening bool) {
	a.mu.Lock()
	a.listening = listening
	alreadyCapturing := a.capturing
	a.mu.Unlock()

	if listening {
		// The wake word needs the microphone. The meter and the wake
		// word share it, so there is only ever one capture.
		a.startCapture()
		return
	}
	if !alreadyCapturing {
		return
	}
	// The wake word has stopped, but a turn may still be running, and the
	// meter may still be wanted. The capture is only closed when nothing
	// wants it.
	a.maybeStopCapture()
}

// maybeStopCapture closes the microphone when nothing wants it.
//
// The microphone is wanted by the wake word, by the level meter and by a
// turn. It is closed only when none of them does.
func (a *deviceAudio) maybeStopCapture() {
	a.mu.Lock()
	wanted := a.listening || a.monitoring || a.streaming
	a.mu.Unlock()
	if !wanted {
		a.stopCapture()
	}
}

// setStreaming says whether the microphone's frames go to home assistant.
//
// It is on for the length of a turn and off the rest of the time. A panel
// that streams the room continuously is a panel that uploads the room, and
// the wake word is local precisely so that the room stays in the room.
func (a *deviceAudio) setStreaming(streaming bool) {
	a.mu.Lock()
	a.streaming = streaming
	a.mu.Unlock()

	// The sound card is told that the turn has started.
	//
	// A card made of files needs to know: the recording is the
	// microphone, and the microphone has to start at the beginning of
	// the turn. Started any earlier, the frames would be read and
	// thrown away, because the frames read before the turn are not
	// sent to home assistant — the room is the room's until somebody
	// says something.
	if streaming {
		if turn, ok := a.backend.(interface{ TurnStarted() }); ok {
			turn.TurnStarted()
		}
	}
}

// sendMicAudio sends a block of the microphone to home assistant.
//
// It is called for every block while a turn is running. The last block
// carries the end flag, which tells home assistant the utterance is over
// from this side — though it decides that for itself, with its own voice
// activity detection.
//
// The audio is signed 16-bit little-endian mono at 16 kHz, which is what
// home assistant expects.
func (a *deviceAudio) sendMicAudio(pcm []byte) {
	if a.onSendMicAudio != nil {
		a.onSendMicAudio(pcm)
	}
}

// announce plays what Home Assistant asked the panel to say.
//
// The request carries a URL, not the audio, so the panel fetches it and
// plays it. The audio is at the format the player said it takes, because
// Home Assistant runs it through its own proxy first.
//
// Home Assistant waits for the panel to say the announcement is finished,
// and that answer has to come after the speaker has drained it — the
// announcement is not over when the last byte arrives, but when the last
// of the words has been said.
func (a *deviceAudio) announce(mediaID, text string) {
	a.log.Info("the panel was asked to speak", "text", text, "media", mediaID)

	if mediaID == "" {
		// There is nothing to play, so the announcement is over.
		if a.onAnnounceFinished != nil {
			a.onAnnounceFinished()
		}
		return
	}

	// The announcement ends when the speaker has drained it. The reply's
	// own end is reported through the voiceEnded hook, and the
	// announcement is finished as well.
	//
	// It is a flag rather than a wrapped hook, so an announcement does
	// not leave the hook wrapped around for ever: a panel that has asked
	// for a hundred announcements would otherwise report a hundred ends
	// for each of them.
	a.mu.Lock()
	a.voiceAnnouncement = true
	a.voiceActive = true
	a.voiceStreaming = true
	a.mu.Unlock()

	a.ensureOutput()

	go a.playAnnouncement(mediaID)
}

// playAnnouncement fetches the announcement and plays it.
//
// The fetch is on its own goroutine, because the announcement is played
// while the fetch happens and the speaker must not wait for the network.
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

// endVoice says the voice has been played out.
//
// It is the reply's end, and the announcement's end when the voice was
// an announcement. Home Assistant waits for the announcement to be
// finished before it goes on, so the announcement is reported here and
// not when the last byte arrived.
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

// playReply plays the reply that came back from home assistant.
//
// The reply arrives over the ESPHome connection as a stream of chunks.
// The chunks are queued rather than written straight to the speaker,
// because the music is playing on the same speaker and the two have to
// be mixed.
//
// The reply arrives at the rate Home Assistant speaks at, which is not
// the rate the speaker runs at, so it is converted before it is queued.
// The final flag is only ever set for a reply that was cut short: Home
// Assistant says the reply is over with an event, and that is what ends
// a reply that was sent the ordinary way.
// playReply plays the reply that came back from home assistant.
//
// The reply arrives over the ESPHome connection as a stream of chunks.
// The chunks are queued rather than written straight to the speaker,
// because the music is playing on the same speaker and the two have to
// be mixed.
//
// The reply arrives at the rate Home Assistant speaks at, which is not
// the rate the speaker runs at, so it is converted before it is queued.
// The final flag is only ever set for a reply that was cut short: Home
// Assistant says the reply is over with an event, and that is what ends
// a reply that was sent the ordinary way.
func (a *deviceAudio) playReply(pcm []byte, final bool) {
	if len(pcm) > 0 {
		a.setMediaState(esphome.MediaPlayerState_MEDIA_PLAYER_STATE_PLAYING)
		// The reply has begun, so the panel's overlay is told. The
		// screen shows that the answer is coming before the words are
		// there, which is what a person waiting for an answer wants.
		//
		// It is told once: the reply's audio arrives in many frames and
		// the overlay is told once.
		a.mu.Lock()
		started := a.voiceStarted
		first := !a.voiceActive
		a.mu.Unlock()
		if first && started != nil {
			started()
		}
	}
	// The music is turned down under the reply, so the words can be
	// heard over it.
	a.setDucked(true)

	// The reply is converted before it is queued, and it is the
	// converted audio that is counted.
	//
	// The two have to be counted in the same units: the reply arrives
	// at sixteen kilohertz and is played at forty-eight, so counting
	// the arrival and comparing it with the queue would compare sixteen
	// kilohertz's worth of bytes with forty-eight's, and the progress
	// would always come out as nothing played.
	converted := a.resampleVoice(pcm)

	a.mu.Lock()
	a.spokenTotal += int64(len(converted))
	a.mu.Unlock()

	a.playVoice(converted)
	if final {
		a.finishVoice()
		a.setMediaState(esphome.MediaPlayerState_MEDIA_PLAYER_STATE_IDLE)
	}
}

// resampleVoice converts the reply to the rate the speaker plays at.
//
// The reply comes from Home Assistant at the rate it speaks at, and the
// speaker plays at its own rate. The reply is queued and taken out of
// the queue at the speaker's rate, so a reply that was not converted
// would be played at the wrong speed: three times too fast, and a
// chipmunk rather than a voice.
//
// The converter is kept between the chunks, because a reply that is
// converted one chunk at a time has to be interpolated across the chunk
// boundaries or every boundary is a click.
func (a *deviceAudio) resampleVoice(pcm []byte) []byte {
	if len(pcm) == 0 {
		return nil
	}

	a.mu.Lock()
	if a.voiceResampler == nil {
		a.voiceResampler = audio.NewResampler(voiceRate, playbackRate)
		// A new reply starts its count again.
		a.spokenTotal = 0
		// And the last reply's completion is not this one's.
		a.spokenComplete = false
	}
	resampler := a.voiceResampler
	a.mu.Unlock()

	return resampler.Convert(pcm)
}

// flushVoice finishes the reply's conversion.
//
// The last of the reply is held rather than interpolated, because there
// is no next sample to interpolate towards. Without this the reply
// loses the fraction of a millisecond at the end that the conversion
// was holding.
func (a *deviceAudio) flushVoice() []byte {
	a.mu.Lock()
	resampler := a.voiceResampler
	a.mu.Unlock()

	if resampler == nil {
		return nil
	}
	return resampler.Flush()
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

// spokenProgress says how much of the reply has been played, as a
// fraction between zero and one.
//
// The panel shows the answer's words as they are spoken, and this is
// what tells it how far along to show them: the audio arrives as it is
// generated and is played as it arrives, so the words are shown in step
// with the voice rather than all at once.
//
// It is the fraction of the reply's *audio* that has been played, and
// the words are a rough match for it: the text and the audio are the
// same reply, but they are not the same length.
func (a *deviceAudio) spokenProgress() float64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.spokenComplete {
		// The reply has been played out, so all of it has been said.
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

// PauseMedia pauses the music Home Assistant is playing.
//
// The audio that has already arrived stays where it is, so the track
// picks up where it left off rather than from the beginning.
//
// The fetch is left alone: the proxy is still sending, and the audio it
// sends is put in the queue. A pause that threw the audio away would be
// a stop.
func (a *deviceAudio) PauseMedia() {
	a.mu.Lock()
	if !a.mediaActive || a.mediaPaused {
		a.mu.Unlock()
		return
	}
	a.mediaPaused = true
	a.mu.Unlock()

	// The fetch stops reading, so the music that has arrived stays where
	// it is and the track picks up where it left off.
	a.pauseMediaFetch()

	a.log.Info("the music was paused")
	a.setMediaState(esphome.MediaPlayerState_MEDIA_PLAYER_STATE_PAUSED)
}

// ResumeMedia plays the music again after a pause.
func (a *deviceAudio) ResumeMedia() {
	a.mu.Lock()
	if !a.mediaActive || !a.mediaPaused {
		a.mu.Unlock()
		return
	}
	a.mediaPaused = false
	a.mu.Unlock()

	// The fetch goes on.
	a.resumeMediaFetch()

	a.log.Info("the music was played again")
	a.setMediaState(esphome.MediaPlayerState_MEDIA_PLAYER_STATE_PLAYING)
	a.ensureOutput()
}

// StopMedia stops the music Home Assistant is playing.
//
// The fetch is told to give up and the audio that has arrived is
// thrown away, so the track is not picked up again.
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
		// The converter is for one reply. The next reply starts at the
		// beginning of its own audio, and a converter carried over from
		// the last reply would interpolate across the two.
		a.voiceResampler = nil
	}
	a.mu.Unlock()
	if over {
		a.finishPlayback()
		// The music comes back up now the words have been said.
		a.setDucked(false)
		a.endVoice()
	}
}

// finishReply is called when the reply's audio has all arrived.
//
// It is the reply's end. The reply is not over when the last of the words
// has been spoken — the speaker still has them in it — so the reply is
// ended when the speaker has drained them, which is what the mixer does.
//
// Home Assistant sends the end of a reply as an event rather than as a
// flag on the last chunk of audio, so this is what ends a reply that was
// sent the ordinary way.
func (a *deviceAudio) finishReply() {
	// The tail of the reply is held by the converter, because there is
	// no next sample to interpolate towards. It is queued here so that
	// it is played with the rest of it.
	//
	// The queue is appended to directly rather than through playVoice,
	// because playVoice says the reply's frames are still arriving and
	// the reply has already been said to have ended.
	if tail := a.flushVoice(); len(tail) > 0 {
		a.mu.Lock()
		a.voice = append(a.voice, tail...)
		a.voiceActive = true
		// The tail is counted as part of the reply.
		//
		// The progress of the reply is measured against the audio that
		// has arrived, so everything that is played has to be counted.
		// The tail was not, so the progress was measured against less
		// audio than there was, and it could never reach the end: the
		// panel showed the answer's words as the audio was played, and
		// the last of the words never appeared.
		a.spokenTotal += int64(len(tail))
		a.mu.Unlock()
		a.ensureOutput()
	}
	a.finishVoice()
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
	a.voiceResampler = nil
	a.mu.Unlock()
	a.closeOutput()
	a.endVoice()
}

// markReplyPlayedOut says the reply has been played out.
//
// It is the moment the reply is really over: the speaker has drained
// it, so the words have been said. The last frame arriving is not the
// end, because the speaker still has the words in it.
//
// The speaker is idle now, and Home Assistant shows that on the player
// card: a player left on "playing" is a player that looks busy for
// ever.
func (a *deviceAudio) markReplyPlayedOut() {
	a.mu.Lock()
	a.voiceActive = false
	a.voiceResampler = nil
	// The reply is all played, so the progress of the reply is all of it.
	//
	// The panel shows the answer's words as the audio is played, and it
	// needs to be told that the reply is over. The progress is measured
	// against the audio that arrived, and the last of that is held by the
	// converter, so the progress could stop a little short of the end.
	// The words would then be missing their last one.
	//
	// So the reply that has been played out is the reply that has been
	// said, and the progress says so.
	a.spokenComplete = true
	a.mu.Unlock()

	a.finishPlayback()
	// The music comes back up now the words have been said.
	a.setDucked(false)
	a.setMediaState(esphome.MediaPlayerState_MEDIA_PLAYER_STATE_IDLE)
	a.endVoice()
	// The reply has been played out.
	if a.onReplyEnded != nil {
		a.onReplyEnded()
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
	// The ported speaker owns the card: it opens it, writes to it, and
	// knows when each frame will be heard. So this loop does not open the
	// card at all; it feeds the speaker's queues and the music source.
	//
	// The device program's own backend holds the card, so that a
	// recording can stand in for it; the speaker is given that to write
	// to.
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
	clocked, channels := a.openClocked(ctx)
	var writer io.WriteCloser

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
		} else if writer != nil {
			_ = writer.Close()
		}
		a.releaseOutput(generation)
		// The speaker is stopped with the loop, so that the next thing
		// that wants to make a sound finds the card closed and the
		// speaker's queues empty.
		a.stopPortedSpeaker()
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
		// The mute is the panel's own switch or the server's, and
		// either silences the music. The server's mute is not the
		// panel's switch, so they are kept apart and both are honoured.
		// The mute is the microphone's, so it does not silence the
		// speaker. The music server's own mute is the speaker's.
		muted := a.sendspinMuted
		ducked := a.ducked
		duckDB := a.duckDB
		// One period is taken from each queue, so a long reply is spread
		// over the periods that follow rather than played all at once.
		voice := a.takeAudio(&a.voice, periodBytes)
		test := a.takeAudio(&a.test, periodBytes)
		// The music Home Assistant is playing is taken from its own
		// queue. A paused track is not taken from, so the audio that
		// has arrived stays where it is and the track picks up where
		// it left off.
		var media []byte
		if !a.mediaPaused {
			media = a.takeAudio(&a.media, periodBytes)
			// The track is over when there is nothing left to play and
			// nothing more is coming.
			if len(media) == 0 && a.mediaActive && a.mediaStop == nil {
				a.mediaActive = false
				a.mediaPlaying = false
			}
		}
		a.mu.Unlock()

		// The ported speaker is the mixer. It holds the queues, applies
		// the gain and the ducking, runs the vendor's tuning, and writes
		// to the card.
		//
		// The reply, the test sound and the media are handed to it, and
		// the music is attached as a source it pulls from: the music is
		// placed by the server's clock, so it has to be asked for at the
		// moment it is due rather than queued.
		spk := a.portedSpeaker()
		if spk != nil {
			// The reply is mono at the controller's rate; the speaker
			// takes it and converts it.
			if len(voice) > 0 {
				spk.PlayVoice(monoSamples(voice))
			}
			if len(test) > 0 {
				spk.Play(monoSamples(test))
			}
			if len(media) > 0 {
				spk.Play(monoSamples(media))
			}
		}

		musicReady := player != nil && player.Active()
		if musicReady {
			// The music is attached to the speaker as a source it pulls
			// from: the frames are placed by the server's clock, so
			// they have to be asked for at the moment they are due.
			source := a.musicSourceFor(player)
			spk.Attach(source)
			source.renderDue()
		} else if spk != nil {
			spk.Detach(a.musicSource)
		}

		// The ported speaker does the rest: it holds the queues, applies
		// the gain and the ducking, runs the vendor's tuning, converts
		// the rate to the card's own and writes to it.
		//
		// So there is nothing to mix here and nothing to write. The
		// loop's work is to keep the queues fed and to know when the
		// card can be let go.
		_ = muted
		_ = ducked
		_ = duckDB
		_ = volume

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
		// The music Home Assistant is playing is something to play for too.
		// Without it the speaker would be released in the gap between two
		// periods of the track, and the next period would be played late and
		// out of order: a ticking instead of the music.
		hasMedia := len(a.media) > 0 || a.mediaActive
		empty := len(a.voice) == 0 && len(a.test) == 0 && !hasMedia &&
			(player == nil || !musicConnected(player))
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
			a.markReplyPlayedOut()
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
func (a *deviceAudio) openClocked(ctx context.Context) (audio.ClockedOutput, int) {
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
	out, err := a.backend.OpenClockedOutput(ctx, cardRate, 2)
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

// applyDuck turns the music down under a reply.
//
// The level is in decibels and it is negative, so the music is quieter
// than it would be rather than louder. A level of zero leaves the
// music alone.
//
// It is separate from the volume because the two are different things:
// the volume is how loud the music is and the duck is how much quieter
// it is while somebody is speaking.
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
}

// applyGain scales the audio by the device's volume.
//
// The scale is applied here rather than on the sound card so that the
// volume is the same for the voice and the music.
//
// It is the curve's gain and not the percentage itself: a percentage of a
// linear scale is not a percentage of how loud something sounds, and a
// half-dial that is a quarter of the power is a half-dial nobody can hear
// over the room. The curve is TECHO5's, and its top sits below unity so
// that the amplifier's own protection stays in front of the signal.
//
// The mixer is not touched by this: the device program owns the level, and
// a level applied in two places is a level nobody can reason about.
func applyGain(pcm []byte, volume int) {
	gain := vol.GainForPercent(volume)
	if gain == 1 {
		return
	}
	scale(pcm, float64(gain))
}

// scale multiplies every sample by gain, holding it inside int16.
func scale(pcm []byte, gain float64) {
	if gain == 1 {
		return
	}
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

// sendspinToken answers home assistant's request for the music player's
// pairing token.
//
// Home Assistant hands the token to a Sendspin server such as Music
// Assistant, which pairs with the player over it. That is what stops the
// same speaker appearing twice: the server pairs with the player that is
// already there rather than making one of its own.
func (a *deviceAudio) sendspinToken() (token, clientID string, ok bool) {
	a.mu.Lock()
	player := a.sendspin
	a.mu.Unlock()
	if player == nil {
		return "", "", false
	}
	return player.PairingToken(), player.ClientID(), true
}

// applySendspinConfig starts or stops the music player.
//
// The panel's own settings decide whether the device is a music player at
// all, what it is called, and whether it accepts a server that has not
// paired with it.
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
			// The instance is the player's name on the network, and it
			// has to be unique there: two players with the same
			// instance name are the same player as far as a music
			// server is concerned, and one of them is never found.
			//
			// It is the panel's own name with the machine's identity
			// mixed in, so two panels on one network are two players.
			Instance: sendspinInstance(name),
			Product:  "Home Panel",
			Version:  "0.1.0",
			Unpaired: unpaired,
			// The lead and the jitter buffer are how far ahead of the
			// clock the server sends, and how much audio is kept queued.
			//
			// They are small, and that is deliberate. The server holds
			// the audio back by the buffer before it sends it, so a
			// big buffer is a big delay before anything is heard — and
			// the player then compares the audio's own time with the
			// clock and finds the audio is not due yet, so it waits,
			// and the panel is silent for as long as the buffer is.
			//
			// The buffer only has to be big enough that the network
			// cannot outrun the speaker, and a quarter of a second is
			// enough for that.
			RequiredLeadTime: 250 * time.Millisecond,
			MinBuffer:        250 * time.Millisecond,
		})
		if err != nil {
			a.log.Error("the music player could not be started", "error", err)
			a.setSendspinError(err)
			return
		}
		player.OnVolume(func(level int) {
			a.setVolume(level)
		})
		// The music server's volume is the DEVICE's volume.
		//
		// One volume, whoever moves it: the panel's slider, the buttons on
		// the unit, Home Assistant or the music server. The server's
		// commands are handed to the device, which applies them to
		// everything it plays, and the device's level is reported back so
		// the server and the panel agree.
		//
		// Without this the music carried the server's own gain AND the
		// device's, so the music was quieter than everything else and the
		// dial did not say by how much.
		player.OnServerVolume(func(level int) {
			a.setVolume(level)
		})
		player.OnSettings(func(_ int, muted bool, _ int) {
			// The server can mute the music, and it is not the panel's
			// own switch. It is kept apart from it so that unmuting on
			// one does not unmute the other.
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
		// The player's identity is known now, so the device's
		// capabilities can say so.
		if a.onSendspinStarted != nil {
			a.onSendspinStarted()
		}
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
			if player == nil {
				continue
			}
			// The status is refreshed here as well as on the change
			// callback, because the things it reports — whether the
			// player is in step with its server, how much music is
			// queued — change without the session changing.
			a.setSendspinStatus(player.Status())
			if player.Active() {
				a.ensureOutput()
			}
		}
	}()
}

// setSendspinStatus records what the music player is doing.
//
// The status is the player's own: whether it is connected to a server,
// what it is playing, and so on. It is reported to the panel so a person
// can see whether the music works.
func (a *deviceAudio) setSendspinStatus(status sendspin.Status) {
	a.mu.Lock()
	a.sendspinStatus = status
	a.mu.Unlock()
	if a.onSendspinStatus != nil {
		a.onSendspinStatus(status)
	}
}

// setSendspinError records that the music player could not start.
func (a *deviceAudio) setSendspinError(err error) {
	a.log.Error("the music player failed", "error", err)
}

// sendspinStore is where the music player's identity is kept.
func (a *deviceAudio) sendspinStore() string {
	return filepath.Join(a.credentialsDir, "sendspin.json")
}

// sendspinPairingToken exposes the long-lived pairing token from its
// protected store without adding another copy to app settings.
func (a *deviceAudio) sendspinPairingToken() (string, error) {
	return sendspin.PairingToken(a.sendspinStore())
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

// sendspinInstance is the player's name on the network.
//
// It has to be unique on the network: two players with the same
// instance name are the same player as far as a music server is
// concerned, and one of them is never found.
//
// It is the panel's own name with the machine's identity mixed in, so
// two panels on one network are two players and the same panel is the
// same player every time.
func sendspinInstance(name string) string {
	sum := uint32(2166136261)
	for i := 0; i < len(name); i++ {
		sum = (sum ^ uint32(name[i])) * 16777619
	}
	// The machine's address is mixed in too, so two panels with the
	// same name on one network are still two players.
	if machine := machineAddress(); machine != "" {
		for i := 0; i < len(machine); i++ {
			sum = (sum ^ uint32(machine[i])) * 16777619
		}
	}
	return fmt.Sprintf("%s-%02x%02x", name, (sum>>8)&0xFF, sum&0xFF)
}

// machineAddress is the machine's own hardware address.
//
// It is read from the network interfaces, which is where the panel's
// own identity comes from too.
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
