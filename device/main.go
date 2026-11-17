// Package main is the Home Panel's EchoMuse device.
//
// It is the device half of the EchoMuse protocol: it owns the link to the
// controller, the microphone, the speaker and the wake word, and it reports
// what the device is doing so the display can show it.
//
// It is a separate program from the display because the two want different
// things from the machine. The display is a web page; the page cannot hold
// an ALSA handle and mix a voice response into a Sendspin period without
// opening a competing speaker handle and losing synchronisation. So the
// audio stays here, where one process can own it.
//
// The state is written to a file that the display reads. The file is
// written atomically, so a reader never sees half of it.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/vithurshanselvarajah/Home-Panel-Kiosk/audio"
	"github.com/vithurshanselvarajah/Home-Panel-Kiosk/device/ambient"
	"github.com/vithurshanselvarajah/Home-Panel-Kiosk/device/autobrightness"
	"github.com/vithurshanselvarajah/Home-Panel-Kiosk/device/config"
	"github.com/vithurshanselvarajah/Home-Panel-Kiosk/device/esphome"
	"github.com/vithurshanselvarajah/Home-Panel-Kiosk/device/sendspin"
	"github.com/vithurshanselvarajah/Home-Panel-Kiosk/device/state"
)

// State is what the device reports.
//
// It is deliberately flat and small: a display wants to know what the
// device is doing, not to understand the protocol.
type State struct {
	// Connection is whether the device can reach a controller and has been
	// admitted by one. It is what a person asks about the device.
	Connection string `json:"connection"`
	// ConnectionReason says why, in words a person can read.
	ConnectionReason string `json:"connection_reason"`
	// Pairing is true while the device is asking to be admitted.
	Pairing bool `json:"pairing"`

	// Phase is what the device is doing right now.
	Phase string `json:"phase"`
	// PhaseReason says why.
	PhaseReason string `json:"phase_reason"`

	// Listen is what the device is doing with its microphone.
	Listen string `json:"listen"`
	// Listening is true while the controller is listening for a wake word
	// or for a person speaking.
	//
	// It is the controller's doing, not the microphone's. The microphone
	// is also open for the level meter alone, and a device that shows
	// itself as listening because a settings page asked to hear the room
	// is telling the user something untrue.
	Listening bool `json:"listening"`
	// Speaking is true while a response is playing.
	Speaking bool `json:"speaking"`
	// Thinking is true while the controller waits for Home Assistant.
	Thinking bool `json:"thinking"`
	// Transcript is what Home Assistant heard.
	//
	// The panel shows it while the assistant is thinking about it, so
	// the person can see that it heard what they said.
	Transcript string `json:"transcript"`
	// Spoken is what Home Assistant is saying.
	//
	// The panel shows it while the reply is being played.
	Spoken string `json:"spoken"`
	// SpokenProgress is how much of the reply has been played, between
	// zero and one.
	//
	// The panel shows the answer's words as they are spoken, and this
	// is what says how far along to show them.
	SpokenProgress float64 `json:"spoken_progress"`
	// Muted is the device's own mute switch.
	Muted bool `json:"muted"`

	// Volume is the last volume level the device reported.
	Volume int `json:"volume"`

	// Level is the level of the microphone, as a percentage of full scale.
	//
	// It is not in the state file. It is a live reading and not a state:
	// it changes on every frame of audio, and a file that changed ten
	// times a second would be written ten times a second. It has a file
	// of its own, written a few times a second.
	Level int `json:"-"`

	// Pattern is the ring's animation.
	Pattern string `json:"pattern"`
	// Palette is the ring's colours, in order.
	Palette []Colour `json:"palette"`

	// AudioInputs are the microphones this device could use.
	AudioInputs []audio.Endpoint `json:"audio_inputs"`
	// AudioOutputs are the speakers this device could use.
	AudioOutputs []audio.Endpoint `json:"audio_outputs"`
	// SelectedAudioInput is the microphone in use.
	SelectedAudioInput string `json:"selected_audio_input"`
	// SelectedAudioOutput is the speaker in use.
	SelectedAudioOutput string `json:"selected_audio_output"`
	// Monitoring is true while the microphone is open for the level meter
	// alone, so a person can see what the microphone hears.
	Monitoring bool `json:"monitoring"`

	// MusicState is what the music player is doing.
	MusicState string `json:"music_state"`
	// MediaState is what the media Home Assistant is playing is doing.
	//
	// It is the panel's own music, as opposed to the music server's,
	// and the two take turns.
	MediaState string `json:"media_state"`
	// MusicPaired is how many servers the player is paired with.
	MusicPaired int `json:"music_paired"`
	// MusicBuffered is how much music is queued, in milliseconds.
	MusicBuffered int64 `json:"music_buffered"`
	// MusicSynced says the player is in step with its server.
	MusicSynced bool `json:"music_synced"`
	// MusicTitle is what is playing.
	MusicTitle string `json:"music_title"`
	// MusicArtist is who is playing it.
	MusicArtist string `json:"music_artist"`
	// MusicAlbum is the album it is from.
	MusicAlbum string `json:"music_album"`
	// MusicArtwork is the picture of the album, if there is one.
	MusicArtwork string `json:"music_artwork"`
	// MusicPosition is how far into the track the music has got, in
	// milliseconds.
	MusicPosition int64 `json:"music_position"`
	// MusicDuration is how long the track is, in milliseconds. It is zero
	// for a live stream, which has no end.
	MusicDuration int64 `json:"music_duration"`
	// MusicPlaying says whether the music is playing or paused.
	MusicPlaying bool `json:"music_playing"`
	// MusicTransport is what the transport can do.
	//
	// It is empty when there is no music server, and the panel shows no
	// buttons rather than buttons that do nothing.
	MusicTransport []string `json:"music_transport,omitempty"`

	// LastError is the most recent error, if any.
	LastError string `json:"last_error"`

	// UpdatedAt is when this state was produced.
	UpdatedAt time.Time `json:"updated_at"`
}

// Colour is one indicator's colour.
type Colour struct {
	R int `json:"r"`
	G int `json:"g"`
	B int `json:"b"`
}

// configPollInterval is how often the configuration file is checked.
//
// The application writes it when the settings change, and the change is
// rare, so this only decides how quickly a change is noticed.
const configPollInterval = time.Second

// connection derives the connection state from a snapshot.
//
// The link says whether home assistant is talking to the panel. There is
// no approval step and no credential: home assistant finds the panel on
// the network and asks it for its key, and the panel either has one or
// it does not.
func connection(s state.Snapshot) (string, string) {
	if s.LinkUp {
		return "connected", "home assistant is connected"
	}
	return "disconnected", "the link to home assistant is down"
}

// Config is the device configuration the application writes.
//
// Config is what the device is told to do.
//
// It lives in the config package, because it is the panel's settings and
// not the program that runs them.
type Config = config.Config

// readConfig reads the device configuration.
func readConfig(path string) (Config, error) {
	var c Config
	data, err := os.ReadFile(path)
	if err != nil {
		return c, err
	}
	if err := json.Unmarshal(data, &c); err != nil {
		return c, fmt.Errorf("echomuse-device: decoding the configuration: %w", err)
	}
	return c, nil
}

// writeConfig writes the device configuration.
//
// It writes the file through a temporary one and renames it into place,
// so a reader never sees half of it.
//
// The file is the panel's: it is written by the panel when the panel's
// settings change. But some of the settings in it belong to the device
// program — the wake word's sensitivity, the ducking, the level — and
// the panel does not know about them. So the device program writes them
// back, and the two write to the same file at different times.
//
// The settings the device program owns have a file of their own, which
// only the device program writes. The panel's file is left alone: the
// panel owns it and the panel writes it, and nothing else touches it.
//
// So the two never write the same file, and neither can lose the
// other's change.
func writeConfig(path string, update func(c *Config)) error {
	if path == "" {
		return nil
	}
	c, err := readConfig(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	update(&c)

	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	temporary := fmt.Sprintf("%s.%d.tmp", path, os.Getpid())
	if err := os.WriteFile(temporary, data, 0o600); err != nil {
		return err
	}
	if err := os.Rename(temporary, path); err != nil {
		os.Remove(temporary)
		return err
	}
	return nil
}

// keepSetting writes a setting the device program owns to the
// configuration file.
//
// The write is not waited for: a setting is a reading, and the device
// program should not stop what it is doing for it. A failure is logged
// rather than returned, because there is nowhere for it to go: the
// setting is in force either way, and it is only its keeping that is
// lost.
func keepSetting(path string, log *slog.Logger, update func(c *Config)) {
	if path == "" {
		return
	}
	if err := writeConfig(path, update); err != nil {
		log.Warn("the setting could not be kept", "error", err)
	}
}

// lightPollInterval is how often the light is read.
//
// It is slow: the sensor's own integration time is a quarter of a
// second, and the room's light does not change faster than a person
// moves through it.
const lightPollInterval = 2 * time.Second

// watchLight reads the room's light and keeps the screen's brightness
// to it.
//
// The reading is published so Home Assistant can show it, and the
// brightness is worked out from it when the automatic brightness is on.
//
// The brightness is the panel's to write: it owns the backlight and
// knows about the relight that a screen coming back from off needs. So
// the level is asked for rather than written here.
func watchLight(ctx context.Context, light *ambient.Sensor, control *ControlPlane, log *slog.Logger) {
	if light == nil {
		return
	}
	ticker := time.NewTicker(lightPollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		lux, ok, err := light.Lux()
		if err != nil {
			log.Debug("the light could not be read", "error", err)
			continue
		}
		if !ok {
			// The gain has just moved, so the counts are from the old
			// gain. The next reading is the one to report.
			continue
		}
		control.SetLux(lux)
		// The screen's brightness follows the room when the automatic
		// brightness is on.
		//
		// The level is asked of the panel rather than written here: the
		// panel owns the backlight and knows about the relight that a
		// screen coming back from off needs.
		if control.AutoBrightness() {
			ceiling := control.BrightnessCeiling()
			level := autobrightness.Level(ceiling, true, lux)
			control.SendCommand(ControlCommand{Action: "auto_brightness_level", Value: level})
		}
	}
}

// defaultVolume is the level the speaker is at on a panel that has never
// had a level.
//
// There is no startup volume to choose any more: the speaker keeps the
// level it was last at, so a panel that has had a level comes back at
// it. This is where a panel that has not starts.
const defaultVolume = 50

// bargeInThreshold is the score the wake word has to reach while the
// panel is speaking.
//
// It is above the wake word's own threshold because the panel has no
// echo cancellation: the echo of the reply is louder at the microphone
// than a person talking over it, and a lower bar would make the panel
// interrupt itself.
//
// With echo cancellation the bar goes below the wake word's own, which
// is what makes interrupting easy.
const bargeInThreshold = 0.85

func main() {
	hostName, err := os.Hostname()
	if err != nil || hostName == "" {
		hostName = "home-panel"
	}
	var (
		deviceID      = flag.String("device-id", hostName, "stable device identity")
		board         = flag.String("board", hostName, "board name, shown in Home Assistant")
		creds         = flag.String("credentials-dir", "", "where the link credential lives")
		stateFile     = flag.String("state-file", "", "write the state here as JSON")
		levelFile     = flag.String("level-file", "", "write the live microphone level here; empty derives it from the state file")
		configFile    = flag.String("config-file", "", "read the device configuration from here")
		settingsFile  = flag.String("settings-file", "", "keep the settings the device program owns in here")
		mic           = flag.String("mic", "", "the microphone to use; empty is the system default")
		speaker       = flag.String("speaker", "", "the speaker to use; empty is the system default")
		micFile       = flag.String("mic-file", "", "play this recording as the microphone instead of using a sound card")
		speakerFile   = flag.String("speaker-file", "", "write the speaker's audio to this file instead of using a sound card")
		verbose       = flag.Bool("verbose", false, "log at debug level")
		esphomePort   = flag.Int("esphome-port", 0, "the port the Home Assistant API is on; zero picks one")
		esphomeKey    = flag.String("esphome-key", "", "the base64 encryption key for the Home Assistant API")
		esphomeName   = flag.String("esphome-name", hostName, "the name Home Assistant knows the panel by")
		controlSocket = flag.String("control-socket", "", "the socket the panel talks to this program over")
	)
	flag.Parse()

	level := slog.LevelInfo
	if *verbose {
		level = slog.LevelDebug
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))
	slog.SetDefault(log)

	if *creds == "" {
		fmt.Fprintln(os.Stderr, "echomuse-device: --credentials-dir is required")
		os.Exit(2)
	}

	// The sound card, or the files that stand in for it.
	//
	// A recording played as the microphone is how the panel is tested
	// without a room and without a card: the microphone really is the
	// recording, so what goes to Home Assistant is what the recording
	// says, and the whole pipeline is exercised.
	backend := audioBackend(audio.New(*mic, *speaker))
	if *micFile != "" || *speakerFile != "" {
		backend = newFileBackend(*micFile, *speakerFile)
		log.Info("the sound card is replaced by files", "microphone", *micFile, "speaker", *speakerFile)
	}

	// The identity the credentials belong to wins over the one that was
	// asked for. The controller issues a link credential to one device,
	// and a device that presents it under a different name is a different
	// device — so the controller creates one, and the dashboard fills up
	// with copies of the same panel.
	identity := resolveIdentity(*creds, *deviceID)
	if identity != *deviceID {
		log.Info("the device keeps the identity its credentials belong to", "asked", *deviceID, "identity", identity)
	}

	// The panel is a voice satellite to home assistant, so the wake word
	// is local and the microphone is only opened when the room is worth
	// listening to.
	//
	// The wake word is optional. The model and the runtime are installed
	// by hand, and a panel without them still works: it is a screen with
	// some switches, and home assistant can ask it to speak.
	wake, wakeErr := openWakeWord(log)
	if wakeErr != nil {
		log.Info("the local wake word is not available", "reason", wakeErr)
	}

	// The ring is the panel's own: the wake word fires, the turn starts,
	// and the panel knows what it is doing at every point.
	tracker := state.NewTracker()

	// The audio is the panel's own. It is one object, because the voice
	// and the music share one speaker and must not open it twice.
	speaker_ := &deviceAudio{
		backend:        backend,
		log:            log,
		credentialsDir: *creds,
		// The wake word starts on, so the copy of its answer starts on
		// too. A copy that started off would say the wake word was off
		// on a panel that was listening.
		wakeWordEnabled: true,
		// The speaker starts at half. There is no startup volume to
		// choose any more: the speaker keeps the level it was last at,
		// and a panel that has never had a level starts here.
		volume: defaultVolume,
	}

	// The microphone's mute is the hardware latch, where the board has
	// one. The latch is the source of truth: the button on the unit
	// moves it, and it says whether the microphones are cut.
	muteController := newMuteController(log)
	speaker_.mute = muteController

	// The reporter is made before the callbacks are wired, because the
	// callbacks tell it when a reply starts and ends and it is what
	// publishes that.
	reporter := &stateReporter{
		log:   log,
		state: tracker,
		audio: speaker_,
		wake:  wake,
	}
	// The live state and the live level are written into memory.
	//
	// They are a live reading and not a record: they change as the
	// device listens and speaks, and they are worth nothing once the
	// process is gone. Writing them to the flash would wear the flash
	// out for nothing — a device on eMMC is written to a few thousand
	// times over its life.
	//
	// The path given is used when it is already in memory. When it is
	// not — a development run, or a system with no memory filesystem —
	// a directory in memory is found, and the ordinary path is the last
	// resort.
	statePath := *stateFile
	levelPath := *levelFile
	if statePath != "" && !isMemory(path.Dir(statePath)) {
		statePath = runtimePath(identity, "state.json", statePath)
	}
	switch {
	case levelPath != "" && !isMemory(path.Dir(levelPath)):
		levelPath = runtimePath(identity, "level", levelPath)
	case statePath != "":
		levelPath = runtimePath(identity, "level", statePath+".level")
	}
	reporter.file = statePath
	reporter.levelFile = levelPath

	// The reply ends when the speaker has drained it, which is later than
	// the end of the stream. The mixer is what knows when that is.
	speaker_.voiceEnded = reporter.replyEnded
	// The reply begins when the first of its audio arrives, which is
	// before the end of the stream and before the answer has all been
	// written. The panel's overlay is drawn from this, so the screen has
	// to know a reply is coming before the words are there.
	speaker_.voiceStarted = reporter.replyStarted

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	// The audio devices are found before anything is published, so the
	// first state the application reads already carries them.
	reporter.discoverAudio()

	// The configuration is read before the control plane starts.
	//
	// The music player is started by the configuration, and the player's
	// identity goes in the device's capabilities. Home Assistant reads
	// the capabilities when it connects, and a music server pairs with
	// the player using the token Home Assistant reads from there. So
	// the player has to be running before Home Assistant connects, and
	// the capabilities have to be right the first time they are asked
	// for.
	//
	// The configuration is read again by the watcher, which applies any
	// change to it.
	if *configFile != "" {
		if c, err := readConfig(*configFile); err == nil {
			applySendspinOnly(c, speaker_, log)
		} else if !errors.Is(err, os.ErrNotExist) {
			log.Warn("the device configuration could not be read", "error", err)
		}
	}

	// The light sensor, where the board has one.
	//
	// It is not an error for a board to have none: the screen's
	// brightness is then set by hand and nothing else.
	light, lightErr := ambient.Open()
	if lightErr != nil {
		log.Info("the light sensor is not available", "reason", lightErr)
		light = nil
	} else {
		log.Info("the light sensor is ready")
	}

	// The control plane: the Home Assistant connection and the socket
	// the panel talks over.
	control := startControlPlane(ctx, log, *esphomePort, *esphomeKey, *esphomeName, *board, *controlSocket, speaker_, wake, light)
	defer control.Close()

	// The settings the device program owns are read back, so a panel
	// that is restarted comes back at the settings that were chosen
	// rather than at the defaults.
	//
	// They are read before the control plane's callbacks are wired, so
	// a setting that was kept is the setting in force before Home
	// Assistant asks about it.
	if *settingsFile != "" {
		if c, err := readConfig(*settingsFile); err == nil {
			applyVoiceSettings(c, speaker_, wake, control, log)
		} else if !errors.Is(err, os.ErrNotExist) {
			log.Warn("the kept settings could not be read", "error", err)
		}
	}

	// The drawer says whether the panel can reach Home Assistant, and
	// the link is what says. Nothing else knows it.
	control.OnLink(reporter.setLink)
	// The transcript comes from the voice assistant, which the control
	// plane owns.
	reporter.transcript = control.Transcript
	reporter.spoken = control.Spoken
	reporter.spokenProgress = control.SpokenProgress

	// The voice assistant is the control plane's. The panel asks it to
	// start a turn when the wake word fires, and it streams the
	// microphone to home assistant for the length of the turn.
	control.OnVoice(esphome.VoiceHooks{
		OnTurn:     reporter.turnStarted,
		OnEnd:      reporter.turnEnded,
		OnThinking: reporter.setThinking,
		OnSpeaking: reporter.setSpeaking,
		// The reply's audio comes back on the connection, and the speaker
		// is what plays it.
		OnAudio: speaker_.playReply,
		// The reply's end is said with an event, not with the audio, so
		// the speaker is told separately that there will be no more.
		OnReplyEnd: speaker_.finishReply,
		// The reply is over when the speaker has drained it, and the
		// speaker is the one that knows.
		OnPlayedOut: control.ReplyPlayedOut,
		// A reply that was interrupted is dropped rather than waited
		// for: the person said the wake word again and meant it.
		OnFlush: speaker_.flush,
		// Home Assistant asking the panel to speak is answered by playing
		// the audio that follows, and by telling it the announcement is
		// finished when the speaker has drained it.
		//
		// Without the answer, Home Assistant waits for it, the wait
		// times out, and the connection is dropped.
		OnAnnounce: speaker_.announce,
		// The microphone's frames go to home assistant while the turn is
		// running. They are sent from the capture goroutine, which is
		// where they are captured, so nothing is buffered on the way.
		OnMicAudio: speaker_.sendMicAudio,
		// The end of the utterance goes to home assistant as well, with
		// the end flag on the audio. It is what closes the pipeline on
		// the device's side.
		OnUtteranceEnd: func() {
			if err := control.SendVoiceEnd(); err != nil {
				log.Debug("the end of the utterance could not be sent", "error", err)
			}
		},
	})

	// The music player's identity goes in the device's capabilities.
	//
	// It is how a music server tells that the player it found on the
	// network is the player Home Assistant already knows about, and so
	// does not make one of its own. It is also how the music server gets
	// the token it pairs with, which Home Assistant reads from here.
	//
	// The player is not running yet: it is started when the
	// configuration is read, and the configuration is read after this.
	// So the capabilities are filled in when the player starts, and the
	// capabilities are sent again when they have changed.
	publishSendspin := func() {
		player := speaker_.sendspinPlayer()
		if player == nil {
			return
		}
		control.SetSendspin(esphome.NewSendspin(player.ClientID(), player.PairingToken()))
	}
	publishSendspin()
	speaker_.onSendspinStarted = publishSendspin
	control.OnSendspinStarted(publishSendspin)

	// The capabilities are asked for when Home Assistant connects, and
	// the music player's identity is in them. So the capabilities are
	// asked for again when they have changed, or Home Assistant would
	// be told about a player that was not there yet.
	control.OnSendspinStarted(func() {
		control.PushCapabilities()
	})

	// The speaker. It is the entity the reply to a voice turn comes back
	// on, and the one Music Assistant plays music through.
	//
	// The speaker owns the volume. Everything else follows it: the
	// panel's own gain, the music server and the system mixer. There is
	// one volume and the speaker is where it lives.
	control.OnMediaPlayer(MediaPlayerHooks{
		OnPlay: func(url string, announcement bool) {
			go speaker_.playMediaURL(ctx, url, announcement)
		},
		OnCommand: func(command esphome.MediaPlayerCommand) {
			// The command goes to whichever music is playing, and only
			// to that one.
			//
			// There are two kinds of music: the music Home Assistant
			// is playing, which the device holds, and the music a
			// music server is playing, which the server has the
			// transport for.
			//
			// Sending the command to both would pause one while the
			// other is playing, and the one that was not asked about
			// would be paused anyway. So the command goes to the one
			// that is playing.
			//
			// The music server's music comes first: it has priority,
			// and it is the music the panel is playing when both
			// somehow are.
			if player := speaker_.sendspinPlayer(); player != nil && player.Active() {
				switch command {
				case esphome.MediaPlayerCommand_MEDIA_PLAYER_COMMAND_PLAY:
					player.Transport("play")
				case esphome.MediaPlayerCommand_MEDIA_PLAYER_COMMAND_PAUSE:
					player.Transport("pause")
				case esphome.MediaPlayerCommand_MEDIA_PLAYER_COMMAND_STOP:
					player.Transport("stop")
				}
				return
			}
			switch command {
			case esphome.MediaPlayerCommand_MEDIA_PLAYER_COMMAND_PLAY:
				speaker_.ResumeMedia()
			case esphome.MediaPlayerCommand_MEDIA_PLAYER_COMMAND_PAUSE:
				speaker_.PauseMedia()
			case esphome.MediaPlayerCommand_MEDIA_PLAYER_COMMAND_STOP:
				speaker_.StopMedia()
			}
		},
		OnVolume: func(volume float32) {
			// Home Assistant asked for a level. The speaker owns the
			// volume, and the panel's own gain follows it, so the music
			// and the reply are played at the level Home Assistant
			// shows.
			speaker_.setVolume(int(volume * 100))
		},
		OnMute: func(muted bool) {
			// The media player's mute is not acted on.
			//
			// There is one mute on this device and it is the
			// microphone's, and it is the hardware latch's: only the
			// button on top of the unit moves it. The speaker has a
			// level rather than a mute, and a level of zero is
			// silence when a person wants it.
			log.Info("the speaker's mute was ignored", "muted", muted)
		},
	})
	// The reply is played on the speaker, so the speaker's state follows
	// it. Home Assistant's player card shows what the speaker is doing,
	// and a reply that does not show is a reply that looks like nothing
	// happened.
	speaker_.onMediaState = func(state esphome.MediaPlayerState) {
		control.SetMediaPlayerState(state)
	}

	// The two music sources take turns.
	//
	// Home Assistant's music and a music server's music are both music,
	// and playing both at once is neither of them. So when one starts,
	// the other is stopped.
	//
	// The microphone and the reply are never stopped: they are the point
	// of the panel.
	speaker_.onMusicStarted = func(source string) {
		switch source {
		case "home-assistant":
			// Home Assistant's music is playing, so the music server's
			// music is paused rather than stopped: the server is still
			// playing it and will say so again.
			if player := speaker_.sendspinPlayer(); player != nil {
				player.Transport("pause")
			}
		case "server":
			// A music server is playing, so Home Assistant's music stops.
			speaker_.StopMedia()
		}
	}

	// The music player's status is part of the state, so a change in it
	// has to reach the panel.
	// The speaker says when the reply has been played out, because it
	// is the speaker that knows: the reply is over when the speaker has
	// drained it and not when the last frame arrived.
	speaker_.onReplyEnded = func() {
		control.ReplyPlayedOut()
	}

	// The counters of the last log line, so that the status is only
	// written down when it has moved on.
	var lastChunks int
	var lastSamples int64
	var lastLog time.Time

	speaker_.onSendspinStatus = func(status sendspin.Status) {
		// The player's counters, for the log.
		//
		// The status is read many times a second, so it is only written
		// down when something has moved on or once a minute: a line
		// every time would bury the journal.
		if status.Player.Chunks != lastChunks || status.Player.Samples != lastSamples {
			lastChunks, lastSamples = status.Player.Chunks, status.Player.Samples
			lastLog = time.Now()
		} else if time.Since(lastLog) < time.Minute {
			return
		}
		log.Info("[sendspin] status", "state", status.State, "buffered", status.BufferedMs,
			"snaps", status.Player.Snaps, "corrections", status.Player.Corrections,
			"underruns", status.Player.Underruns, "lateDrops", status.Player.LateDrops,
			"overruns", status.Player.BufferOverruns, "lastErrUs", status.Player.LastErrorUs,
			"chunks", status.Player.Chunks, "samples", status.Player.Samples,
			"maxChunk", status.Player.MaxChunk, "backwards", status.Player.Backwards,
			"notDue", status.Player.NotDue, "tooLate", status.Player.TooLate,
			"empty", status.Player.Empty, "played", status.Player.Played,
			"frames", status.Player.Frames, "leadUs", status.Player.LeadUs,
			"spanUs", status.Player.SpanUs, "headAgeUs", status.Player.HeadAgeUs,
			"tailAgeUs", status.Player.TailAgeUs, "offsetUs", status.Player.OffsetUs,
			"driftPpm", status.Player.DriftPpm, "rms", status.Player.RMS,
			"speed", status.Metadata.Speed, "title", status.Metadata.Title)
		// A music server that starts playing takes the turn from Home
		// Assistant's music.
		//
		// It is said once, when the music starts and not while it
		// plays: the status is read many times a second and the music
		// server's music does not keep starting.
		if status.State == "playing" {
			speaker_.mu.Lock()
			playing := speaker_.sendspinPlaying
			speaker_.sendspinPlaying = true
			speaker_.mu.Unlock()
			if !playing && speaker_.onMusicStarted != nil {
				speaker_.onMusicStarted("server")
			}
		} else {
			speaker_.mu.Lock()
			speaker_.sendspinPlaying = false
			speaker_.mu.Unlock()
		}
		reporter.publish(reporter.state.Snapshot())
	}

	// The transport buttons are on the panel and the transport is on the
	// music server, so a press travels through here.
	//
	// The server decides which commands it will accept, and the panel is
	// told the list, so a command it does not want is never sent.
	control.OnTransport(func(command TransportCommand) {
		// The command goes to whichever music it is for.
		//
		// The panel's own music is the audio the device holds, so it can
		// be paused and played again. The music server's music is the
		// server's, and the server has the transport.
		if command.Source == "home" {
			switch command.Action {
			case "play":
				speaker_.ResumeMedia()
			case "pause":
				speaker_.PauseMedia()
			case "stop":
				speaker_.StopMedia()
			}
			return
		}

		player := speaker_.sendspinPlayer()
		if player == nil {
			return
		}
		// The command goes to the server only if the server says it
		// takes it.
		//
		// The server is the one that has the music, and it says what
		// it will do with it. A command it does not want is a command
		// it refuses, and the panel's buttons are drawn from the same
		// list, so the button and the music agree.
		//
		// A seek is a special case: the server's list says "seek" for a
		// server that will take one, and the panel's list is what the
		// drawer uses to decide whether the scrubber works.
		if !player.CanTransport(command.Action) {
			return
		}
		switch command.Action {
		case "play":
			player.Transport("play")
		case "pause":
			player.Transport("pause")
		case "next":
			player.Transport("next")
		case "previous":
			player.Transport("previous")
		case "seek":
			player.SeekTo(command.PositionMs)
		}
	})

	// The speaker's entity follows the panel's volume.
	//
	// The panel's gain and the speaker's level are one setting: the
	// panel plays the music and the reply at the level the speaker
	// says, and the panel's own slider says so too.
	speaker_.onVolume = func(volume float32) {
		control.SetMediaPlayerVolume(volume)
	}

	// The panels follow the speaker's level.
	//
	// The level is the device program's, so the panels are told where it
	// is rather than each keeping its own idea of it. The mixer is left
	// alone: the level is applied in the samples, on a curve, and a level
	// applied in two places is a level nobody can reason about.
	speaker_.onPanelsVolume = func(level int) {
		control.SendVolume(level)
	}

	// The wake word hears every block the microphone captures.
	//
	// The microphone is open for as long as the wake word is listening,
	// which is as long as the panel is switched on — so this is where the
	// level comes from too.
	// The announcement is finished when the speaker has drained it, and
	// Home Assistant has to be told.
	speaker_.onAnnounceFinished = func() {
		control.AnnounceFinished()
	}

	speaker_.onWakeAudio = func(pcm []byte) {
		wake.Push(pcm, time.Now())
	}

	// The microphone is opened again after every turn, and the wake
	// word forgets what it heard before that. The reply is the panel
	// talking, and the detector's window is two seconds long, so without
	// this the panel can hear itself.
	speaker_.onWakeReset = func() {
		wake.Reset()
	}

	// The wake word needs the microphone, so it is opened for as long as
	// the wake word is listening.
	if wake != nil && wake.Enabled() {
		speaker_.setListening(true)
	}

	// The wake word is reported to home assistant, so its picker shows
	// the model the panel is listening for and whether it is on.
	//
	// The flags ride the device information, which is a one-shot at
	// connect, so this is read when the connection is made rather than
	// every time the state changes.
	reportWakeWord := func() {
		model := ""
		if wake != nil {
			model = wake.Model()
		}
		control.SetWakeWord(model, wake.Ready(), wake.Enabled())
	}
	reportWakeWord()

	// When home assistant's picker switches the wake word, the panel
	// follows it: the same switch, reached from the other side.
	control.OnWakeWord(func(enabled bool) {
		wake.SetEnabled(enabled)
		speaker_.setListening(enabled && wake.Ready())
	})

	// The voice settings, as Home Assistant sees them.
	//
	// They used to live in the device's own configuration file, which
	// meant Home Assistant could neither read them nor change them.
	// The wake word's bar is dropped while the panel is speaking, so
	// that a person can interrupt a reply.
	//
	// The echo of the panel's own voice is about twenty-five decibels
	// louder at the microphone than the person talking over it, so
	// without this a person who interrupts is not heard at all.
	wake.SetBargeThreshold(bargeInThreshold, speaker_.speaking)

	// The settings the device program owns are kept.
	//
	// They are set from Home Assistant, and Home Assistant is not
	// always there: a panel that is restarted with the link down would
	// come back at the defaults rather than at the settings that were
	// chosen. So each one is written to the configuration file when it
	// changes, and read back at the next start.
	control.OnWakeThreshold(func(threshold float64) {
		wake.SetThreshold(threshold)
		speaker_.setWakeThreshold(threshold)
		log.Info("the wake word sensitivity was set", "threshold", threshold)
		keepSetting(*settingsFile, log, func(c *Config) {
			c.WakeThreshold = &threshold
		})
	})
	control.OnWakeEnabled(func(enabled bool) {
		wake.SetEnabled(enabled)
		speaker_.setListening(enabled && wake.Ready())
		speaker_.setWakeEnabled(enabled)
		log.Info("the wake word was switched", "enabled", enabled)
		keepSetting(*settingsFile, log, func(c *Config) {
			c.WakeWordEnabled = &enabled
		})
	})
	control.OnDucking(func(db float64) {
		speaker_.setDuckDb(db)
		log.Info("the ducking was set", "decibels", db)
		keepSetting(*settingsFile, log, func(c *Config) {
			c.DuckDb = &db
		})
	})
	// The speaker's own tuning and the microphone's own settings.
	//
	// Each is kept, so that a panel that is restarted comes back with the
	// sound it was set to rather than the default.
	// The audio stack's own settings are the panel's settings page's, and
	// they come to the device program in its configuration file. They are
	// not kept here: the two files would fight, and this one is read
	// after the configuration, so the kept copy would win.
	control.OnSpeakerTuning(func(on bool) {
		speaker_.setSpeakerTuning(on)
		log.Info("the vendor's tuning was switched", "on", on)
	})
	control.OnSpeakerGain(func(db int) {
		speaker_.setSpeakerGain(db)
		log.Info("the amplifier's gain was set", "decibels", db)
	})
	control.OnSpeakerStereo(func(on bool) {
		speaker_.setSpeakerStereo(on)
		log.Info("stereo was switched", "on", on)
	})
	control.OnSpeakerBass(func(db float64) {
		speaker_.setSpeakerBass(db)
		log.Info("the bass shelf was set", "decibels", db)
	})
	control.OnSpeakerTreble(func(db float64) {
		speaker_.setSpeakerTreble(db)
		log.Info("the treble shelf was set", "decibels", db)
	})
	control.OnClearerSpeech(func(on bool) {
		speaker_.setClearerSpeech(on)
		log.Info("clearer speech was switched", "on", on)
	})
	control.OnLouderTop(func(on bool) {
		speaker_.setLouderTop(on)
		log.Info("louder top volume was switched", "on", on)
	})
	control.OnMicCancel(func(on bool) {
		speaker_.setMicCancel(on)
		log.Info("echo cancellation was switched", "on", on)
	})
	control.OnMicLeveling(func(on bool) {
		speaker_.setMicLeveling(on)
		log.Info("the microphone's automatic gain was switched", "on", on)
	})
	control.OnMicDenoise(func(on bool) {
		speaker_.setMicDenoise(on)
		log.Info("noise reduction was switched", "on", on)
	})
	control.OnMicSensitivity(func(db int) {
		speaker_.setMicSensitivity(db)
		log.Info("the microphone's sensitivity was set", "decibels", db)
	})
	// The automatic brightness is kept, so a panel that is restarted
	// comes back with the screen following the room or not, as it was.
	control.OnAutoBrightness(func(on bool) {
		log.Info("the automatic brightness was switched", "on", on)
		keepSetting(*settingsFile, log, func(c *Config) {
			c.AutoBrightness = &on
		})
	})
	// The mute is the hardware latch's, and only the hardware moves it.
	//
	// The button on top of the unit sets the latch, and the latch is
	// one-way from software: the button is also the only thing that
	// releases it. So there is nothing for the panel or for Home
	// Assistant to switch — they are told where the latch is, and the
	// microphone is cut or not cut by the hardware.
	//
	// The mute is the microphone's and not the speaker's: a person who
	// mutes the panel wants the room to stop hearing them, and they
	// still want to hear the music.

	// The latch is followed, and the button's light follows the latch.
	//
	// The button on the unit is the latch's, so a press on it is a
	// change of the mute rather than an event to handle: the latch
	// reports where it ended up and everything else follows.
	muteController.OnChange(func(muted bool) {
		log.Info("the microphone mute changed", "muted", muted)
		// The light follows the latch, so the button shows what is
		// happening rather than what was last asked for.
		if err := setMicMuteLED(muted); err != nil {
			log.Debug("the mute light could not be set", "error", err)
		}
		reporter.setMuted(muted)
	})
	go muteController.Watch(ctx)

	// The light starts where the latch is, so a unit that was muted
	// before the program started shows the light rather than a silent
	// mute that looks like an open microphone.
	if err := setMicMuteLED(muteController.Get()); err != nil {
		log.Debug("the mute light could not be set", "error", err)
	}

	// The panel's volume slider is a control surface for the device
	// program's level.
	//
	// The level is applied in one place — in the samples, on a curve —
	// and the mixer is left alone. A level applied in two places is a
	// level nobody can reason about: the same slider position sounds
	// different depending on which path the audio took.
	control.OnVolume(func(level int) {
		speaker_.setVolume(level)
		// The level is kept, so a panel that is restarted comes back at
		// the level it was at.
		keepSetting(*settingsFile, log, func(c *Config) {
			c.Volume = &level
		})
	})

	// The microphone's frames go to home assistant over the ESPHome
	// connection. The device program owns both, so it does not have to
	// hand the frames across a boundary to get them there.
	speaker_.onSendMicAudio = func(pcm []byte) {
		if err := control.SendVoiceAudio(pcm, false); err != nil {
			log.Debug("the microphone did not reach home assistant", "error", err)
		}
	}

	// The wake word fires, the turn starts. The panel opens the
	// microphone for the turn and closes it when home assistant says the
	// utterance is over.
	startTurn := func(phrase string) {
		log.Info("the wake word fired", "phrase", phrase)
		if err := control.StartTurn(phrase); err != nil {
			log.Warn("the turn could not be started", "error", err)
		}
	}
	if wake != nil {
		wake.onCross = startTurn
	}
	// Something else can start a turn too: an automation, or a person
	// pressing a button on the screen. It is the same turn.
	control.OnListen(startTurn)

	go reporter.run(ctx)
	go watchConfig(ctx, *configFile, reporter, speaker_, wake, control, log)
	go watchLight(ctx, light, control, log)

	// The buttons on top of the device.
	//
	// They are read here rather than by a script on the side, because the
	// level is the device program's and the mute is the hardware latch's:
	// both are this program's business.
	//
	// A volume button moves the level one step along the curve, so a
	// press is the same size of step wherever the level is — which is
	// what makes the buttons and the panel's slider the same setting.
	deviceButtons := newButtons(log)
	deviceButtons.onVolume = func(delta int) {
		speaker_.stepVolume(delta)
	}
	deviceButtons.onMute = func() {
		// The latch is the hardware's and the kernel has already flipped
		// it. The state is read back so that what is reported is where
		// the latch actually is.
		log.Info("the mute button was pressed", "muted", muteController.Get())
	}
	if err := deviceButtons.Start(ctx); err != nil {
		log.Warn("the buttons could not be read", "error", err)
	} else {
		go func() {
			if err := deviceButtons.Run(ctx); err != nil && ctx.Err() == nil {
				log.Warn("the buttons stopped", "error", err)
			}
		}()
		defer deviceButtons.Close()
	}

	<-ctx.Done()
	// Stop capture before closing the scorer. The capture goroutine may
	// still be finishing a frame, and Scorer.Close waits for any inference
	// already in flight before releasing the native ONNX sessions.
	speaker_.stop()
	// The ported speaker is closed with the rest: it holds the card, and
	// a card that is still open when the program starts again is a card
	// that ALSA asserts on.
	speaker_.stopPortedSpeaker()
	if wake != nil {
		wake.Close()
	}
	// The latch's node and line are released, so a program that starts
	// again finds them free.
	if err := muteController.Close(); err != nil {
		log.Debug("the microphone latch could not be released", "error", err)
	}
	// The memory the live state lived in is given back at once, rather than
	// when the last reference to it goes. The kernel would do it anyway,
	// but a device that is restarted often would leave a directory behind
	// each time.
	clearRuntime(identity)
}

// stateReporter turns the panel's state into the small shape a display
// wants, and publishes it.
type stateReporter struct {
	log   *slog.Logger
	state *state.Tracker
	audio *deviceAudio
	// wake is the local wake word, when there is one.
	wake *wakeWord
	// transcript says what Home Assistant heard.
	//
	// It is a function because the voice assistant belongs to the
	// control plane, which is made after this.
	transcript func() string
	// spoken says what Home Assistant is saying.
	spoken func() string
	// spokenProgress says how much of the reply has been played.
	spokenProgress func() float64

	// file is where the state is written, if anywhere.
	file string
	// levelFile is where the live microphone level is written.
	//
	// It is a file of its own because the level is not a state: it
	// changes ten times a second, and a write for every change would
	// write to the flash ten times a second for ever.
	levelFile string

	mu sync.Mutex
	// writeMu guards the writes to the state file.
	//
	// The state is published from several places at once, and they all
	// write the same file. Without this they would write through the
	// same temporary file, and one of them would rename it away from
	// under the other.
	writeMu sync.Mutex
	// last is the state that was published, so a change is not published
	// twice.
	last State
	// haveLast is whether anything has been published yet.
	haveLast bool
	// speaking is whether a reply is playing.
	//
	// It is kept here rather than taken from the voice assistant, because
	// the voice assistant clears it when the reply has all arrived, and
	// the reply is not over until the speaker has drained it. A reply
	// that stays on the screen for the life of the process is a reply
	// that looks like it is still talking.
	speaking bool
	// listening is whether the microphone is open for a turn.
	listening bool
	// lastLevel is the level that was last written to the level file.
	lastLevel int
	// lastLevelWrite is when the level file was last written.
	//
	// The level is a live reading and not a state: it changes ten times
	// a second, and a write for every change would write to the flash ten
	// times a second for ever. A device on eMMC cannot do that — it wears
	// the flash out for nothing.
	lastLevelWrite time.Time
}

// levelWriteInterval is how often the level file is written.
//
// The level is a live reading and it moves on every frame, so it cannot
// be written on every change. Four times a second is quick enough for a
// meter — a level that moved faster than that could not be read anyway —
// and slow enough that the flash is not written for ever.
const levelWriteInterval = 250 * time.Millisecond

// discoverAudio finds the audio endpoints the machine has.
//
// A machine with no sound card is not an error: the device still works as a
// display, it just cannot hear or speak.
//
// When nobody has chosen a microphone, one is chosen here: the first
// capture device that opens. The system default is not taken on trust,
// because on the Echo Show 8 it is a dummy in front of the real
// microphone array and cannot be opened for the card's own format.
func (r *stateReporter) discoverAudio() {
	inputs, outputs, err := audio.List()
	if err != nil {
		r.log.Info("no audio devices were found", "error", err)
		return
	}
	r.audio.setEndpoints(inputs, outputs)
	r.log.Info("the audio devices were found", "inputs", len(inputs), "outputs", len(outputs))

	if err := r.audio.selectInput(""); err != nil {
		r.log.Warn("no microphone could be opened", "error", err)
		return
	}
	chosen, _ := r.audio.backend.SelectedAudioDevices()
	r.log.Info("the microphone was chosen", "device", chosen)
}

func (r *stateReporter) run(ctx context.Context) {
	r.state.OnChange(func(s state.Snapshot) {
		r.publish(s)
	})
	// The level meter moves on its own, so it is sampled rather than
	// waited for. A hundredth of a second is quick enough for a meter and
	// slow enough not to be a busy loop.
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.publish(r.state.Snapshot())
		}
	}
}

// turnStarted records that a turn began.
//
// The microphone is opened for the turn, so the panel is listening for
// as long as the turn lasts and not a moment longer.
func (r *stateReporter) turnStarted(phrase string) {
	r.mu.Lock()
	r.listening = true
	r.mu.Unlock()

	r.state.SetPhase(state.PhaseListening, "the wake word fired")
	r.state.SetListening(true)
	r.audio.startCapture()
	r.audio.setStreaming(true)
}

// turnEnded records that a turn finished.
//
// The microphone stays open, because the wake word is what decides
// whether the next turn happens, and a closed microphone is a deaf
// panel. What stops is the sending: the room goes to home assistant
// while a turn is running and nowhere else.
func (r *stateReporter) turnEnded(transcript string, failed bool) {
	r.mu.Lock()
	r.listening = false
	r.mu.Unlock()

	r.state.SetListening(false)
	if failed {
		r.state.SetPhase(state.PhaseIdle, "the turn failed")
	} else {
		r.state.SetPhase(state.PhaseIdle, "")
	}
	// The sending stops, and the microphone is left to the wake word.
	audio := r.audio
	audio.setStreaming(false)
	audio.maybeStopCapture()
}

// setLink records whether the link to Home Assistant is up.
//
// It is the connection the drawer shows. Without it the drawer says
// "Disconnected" for ever, because nothing else sets it.
func (r *stateReporter) setLink(up bool) {
	r.state.SetLink(up)
}

// setThinking records that Home Assistant is working on the reply.
func (r *stateReporter) setThinking(thinking bool) {
	r.state.SetThinking(thinking)
	if thinking {
		r.state.SetPhase(state.PhaseThinking, "home assistant is working on it")
	}
}

// setSpeaking records that the reply is playing.
//
// It is kept here rather than taken from the voice assistant, because
// the voice assistant clears it when the reply has all arrived, and the
// reply is not over until the speaker has drained it.
//
// So the end is not taken from it either. The voice assistant saying
// that the reply has all arrived is not the reply ending: the last of
// the words is said after the last frame has arrived. The reply ends
// when the speaker has drained it, which is replyEnded.
func (r *stateReporter) setSpeaking(speaking bool) {
	if !speaking {
		return
	}
	r.replyStarted()
}

// setMuted records that the microphone is muted.
//
// It goes into the state, because the panel shows it: the drawer says
// when the device is muted, and a panel that is muted and says it is
// online is saying the device is working while it cannot hear.
func (r *stateReporter) setMuted(muted bool) {
	r.state.SetMuted(muted)
}

// wakeLevel returns the microphone level, as a percentage.
//
// It is the gate's measurement: the gate notices when somebody is
// talking, which is exactly what a level meter shows.
func (r *stateReporter) wakeLevel() int {
	if r.wake == nil {
		return 0
	}
	return r.wake.Amplitude()
}

// replyStarted records that a reply began.
func (r *stateReporter) replyStarted() {
	r.mu.Lock()
	r.speaking = true
	r.mu.Unlock()

	r.state.SetSpeaking(true)
	r.state.SetPhase(state.PhaseSpeaking, "")
}

// replyEnded records that a reply finished.
//
// It is called when the reply ends and when the link drops, so the
// indicator does not stay on speaking for the life of the process.
func (r *stateReporter) replyEnded() {
	r.mu.Lock()
	r.speaking = false
	r.mu.Unlock()

	r.state.SetSpeaking(false)
	r.state.SetPhase(state.PhaseIdle, "")
}

// publish writes the state if it changed.
func (r *stateReporter) publish(s state.Snapshot) {
	conn, reason := connection(s)

	// A link that drops ends the reply and the turn with it. A reply that
	// is left showing for the life of the process is a reply that looks
	// like it is still talking.
	if !s.LinkUp {
		r.replyEnded()
		r.mu.Lock()
		r.listening = false
		r.mu.Unlock()
		r.state.SetListening(false)
	}

	r.mu.Lock()
	// The reply's own life is tracked here, because the voice
	// assistant's flag is not enough on its own: it is cleared when the
	// reply has all arrived, and the reply is not over until the speaker
	// has drained it.
	//
	// The link's flag still has to agree: it says a reply is playing
	// while the frames are arriving, and the reply's own flag says it
	// while they are being played. Both have to be true.
	//
	// A link that is down cannot be playing a reply, so the reply ends
	// with it — but the flag is not cleared here. It is cleared by the
	// end of the reply, which happens on its own account, and clearing
	// it here as well would lose a reply that is still playing.
	speaking := r.speaking && s.LinkUp
	listening := r.listening && s.LinkUp
	r.mu.Unlock()

	transcript := ""
	if r.transcript != nil {
		transcript = r.transcript()
	}
	spoken := ""
	if r.spoken != nil {
		spoken = r.spoken()
	}
	progress := 0.0
	if r.spokenProgress != nil {
		progress = r.spokenProgress()
	}

	next := State{
		Connection:       conn,
		ConnectionReason: reason,
		Phase:            string(s.Phase),
		PhaseReason:      s.PhaseReason,
		Listen:           string(s.Listen),
		// The device is listening while a turn is running. The
		// microphone is open for the turn and no longer, and the level
		// meter opens it too, so the meter is left out of it.
		Listening:      listening && !r.audio.monitoringMic(),
		Speaking:       speaking,
		Thinking:       s.Thinking,
		Transcript:     transcript,
		Spoken:         spoken,
		SpokenProgress: progress,
		Muted:          s.Muted,
		Volume:         s.Volume,
		Level:          r.wakeLevel(),
		LastError:      s.LastError,
		UpdatedAt:      time.Now().UTC(),
	}

	// The music player's own state, and what it is playing, is filled in
	// by the speaker, which owns the player.

	r.audio.fillState(&next)

	// The level is a live reading and not part of the state. It has a
	// file of its own, written a few times a second.
	level := next.Level
	next.Level = 0

	r.mu.Lock()
	if r.haveLast && same(r.last, next) {
		r.mu.Unlock()
		r.writeLevel(level)
		return
	}
	r.last = next
	r.haveLast = true
	r.mu.Unlock()

	if r.file != "" {
		r.writeFile(next)
	}
	r.writeLevel(level)
}

// writeLevel writes the live level to its own file.
//
// It is written at a fixed rate rather than on every change, because the
// level changes on every frame of audio and the flash is not worth a
// write for every one of them.
//
// The write is skipped when the level has not moved far enough to see.
func (r *stateReporter) writeLevel(level int) {
	if r.levelFile == "" {
		return
	}
	r.mu.Lock()
	now := time.Now()
	due := now.Sub(r.lastLevelWrite) >= levelWriteInterval
	moved := abs(level-r.lastLevel) > levelTolerance
	if due {
		// The clock moves on whether the level moved or not, so a quiet
		// room does not write the file over and over.
		r.lastLevelWrite = now
	}
	if due && moved {
		r.lastLevel = level
	}
	r.mu.Unlock()
	if !due || !moved {
		return
	}
	data := []byte(strconv.Itoa(level))
	if err := writeInPlace(r.levelFile, data); err != nil {
		r.log.Debug("the level could not be written", "error", err)
	}
}

// writeInPlace writes to a file without replacing it.
//
// The file is opened and truncated, so the inode stays the same and the
// filesystem does not have to change the directory. Replacing it is two
// writes and a directory change for a couple of bytes, and the directory
// change is what the flash feels most.
func writeInPlace(path string, data []byte) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if _, err := file.Write(data); err != nil {
		file.Close()
		return err
	}
	return file.Close()
}

// levelTolerance is how much the microphone level may change before the
// state is written again.
//
// A percent of full scale is what the level is, so one is the smallest
// change anybody can see. The meter is drawn from the file, so a level
// that moves within one percent is the same reading.
const levelTolerance = 1

// abs is the distance between two numbers.
func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// same compares two states, ignoring the timestamp.
//
// The level is not compared at all: it is not part of the state.
func same(a, b State) bool {
	if a.Connection != b.Connection || a.Phase != b.Phase || a.Listen != b.Listen {
		return false
	}
	if a.Pairing != b.Pairing {
		return false
	}
	if a.Listening != b.Listening || a.Speaking != b.Speaking || a.Thinking != b.Thinking || a.Muted != b.Muted {
		return false
	}
	// The words are part of the state, because the panel shows them.
	//
	// Without this the state is not rewritten when the words change,
	// and the panel is never told what was heard or what is being said.
	if a.Transcript != b.Transcript || a.Spoken != b.Spoken {
		return false
	}
	// The progress of the reply moves as it is spoken, and the panel
	// shows the words in step with it.
	if a.SpokenProgress != b.SpokenProgress {
		return false
	}
	if a.Monitoring != b.Monitoring {
		return false
	}
	if a.Volume != b.Volume || a.Pattern != b.Pattern || a.LastError != b.LastError {
		return false
	}
	// The music player's state is part of the state, so a change in it is
	// a change in the state. Without this the state is not rewritten when
	// the music connects, and the panel is never told.
	if a.MusicState != b.MusicState || a.MusicPaired != b.MusicPaired ||
		a.MusicBuffered != b.MusicBuffered || a.MusicSynced != b.MusicSynced {
		return false
	}
	// What is playing is part of the state too, so the panel's drawer
	// shows the track, and the position moves with it.
	if a.MusicTitle != b.MusicTitle || a.MusicArtist != b.MusicArtist ||
		a.MusicAlbum != b.MusicAlbum || a.MusicArtwork != b.MusicArtwork ||
		a.MusicPlaying != b.MusicPlaying {
		return false
	}
	if a.MusicPosition != b.MusicPosition || a.MusicDuration != b.MusicDuration {
		return false
	}
	// The music Home Assistant is playing is part of the state too.
	//
	// The panel's drawer shows it beside the music server's music, and
	// without this the state is not rewritten when the music starts or
	// stops, and the panel is never told.
	if a.MediaState != b.MediaState {
		return false
	}
	if len(a.MusicTransport) != len(b.MusicTransport) {
		return false
	}
	for i := range a.MusicTransport {
		if a.MusicTransport[i] != b.MusicTransport[i] {
			return false
		}
	}
	if a.SelectedAudioInput != b.SelectedAudioInput || a.SelectedAudioOutput != b.SelectedAudioOutput {
		return false
	}
	if len(a.AudioInputs) != len(b.AudioInputs) || len(a.AudioOutputs) != len(b.AudioOutputs) {
		return false
	}
	for i := range a.AudioInputs {
		if a.AudioInputs[i] != b.AudioInputs[i] {
			return false
		}
	}
	for i := range a.AudioOutputs {
		if a.AudioOutputs[i] != b.AudioOutputs[i] {
			return false
		}
	}
	if len(a.Palette) != len(b.Palette) {
		return false
	}
	for i := range a.Palette {
		if a.Palette[i] != b.Palette[i] {
			return false
		}
	}
	return true
}

// writeFile writes the state atomically, so a reader never sees half of it.
//
// The state is published from three places at once: the tracker, when
// something changes; the ticker, every hundredth of a second; and the
// music player's status, as often as it changes. They all write the same
// file.
//
// A shared temporary file does not work: one writer renames it away and
// the next one finds nothing there and the rename fails. So the writes
// are done one at a time, under a lock of their own, and each one writes
// through a temporary file named after the writer.
func (r *stateReporter) writeFile(s State) {
	data, err := json.Marshal(s)
	if err != nil {
		r.log.Error("the state could not be encoded", "error", err)
		return
	}
	if err := os.MkdirAll(filepath.Dir(r.file), 0o700); err != nil {
		r.log.Error("the state directory could not be created", "error", err)
		return
	}

	r.writeMu.Lock()
	defer r.writeMu.Unlock()

	temporary := fmt.Sprintf("%s.%d.tmp", r.file, os.Getpid())
	if err := os.WriteFile(temporary, data, 0o644); err != nil {
		r.log.Error("the state could not be written", "error", err)
		return
	}
	if err := os.Rename(temporary, r.file); err != nil {
		os.Remove(temporary)
		r.log.Error("the state could not be published", "error", err)
	}
}

// applySendspinOnly applies the music player's part of the
// configuration.
//
// It is done before the control plane starts, because the player's
// identity goes in the device's capabilities and Home Assistant reads
// those when it connects.
func applySendspinOnly(c Config, audio *deviceAudio, log *slog.Logger) {
	if c.SendspinEnabled == nil && c.SendspinName == nil && c.SendspinUnpaired == nil {
		return
	}
	audio.applySendspinConfig(c)
	log.Info("the music player was configured")
}

// applyConfig applies the configuration to the panel's settings.
//
// The configuration is a set of instructions, not a picture of the panel:
// a key that is absent was not set, and the panel keeps what it had.
// applyVoiceSettings applies the voice settings that were kept.
//
// These are the settings the device program owns: the wake word's
// sensitivity, whether it is on, and how far the music ducks under a
// reply. They are set from Home Assistant, and Home Assistant is not
// always there, so they are kept and read back.
func applyVoiceSettings(c Config, audio *deviceAudio, wake *wakeWord, control *ControlPlane, log *slog.Logger) {
	if c.Volume != nil {
		audio.setVolume(*c.Volume)
		log.Info("the volume was kept", "volume", *c.Volume)
	}
	if c.WakeThreshold != nil {
		wake.SetThreshold(*c.WakeThreshold)
		audio.setWakeThreshold(*c.WakeThreshold)
		log.Info("the wake word threshold was kept", "threshold", *c.WakeThreshold)
	}
	if c.WakeWordEnabled != nil {
		wake.SetEnabled(*c.WakeWordEnabled)
		audio.setWakeEnabled(*c.WakeWordEnabled)
		audio.setListening(*c.WakeWordEnabled && wake.Ready())
		log.Info("the wake word was kept", "enabled", *c.WakeWordEnabled)
	}
	if c.DuckDb != nil {
		audio.setDuckDb(*c.DuckDb)
		log.Info("the ducking was kept", "decibels", *c.DuckDb)
	}
	if c.AutoBrightness != nil {
		control.SetAutoBrightness(*c.AutoBrightness)
		log.Info("the automatic brightness was kept", "on", *c.AutoBrightness)
	}
}

func applyConfig(c Config, reporter *stateReporter, audio *deviceAudio, wake *wakeWord, control *ControlPlane, log *slog.Logger) {
	if c.Volume != nil {
		audio.setVolume(*c.Volume)
		log.Info("the volume was set", "volume", *c.Volume)
	}
	if c.DuckDb != nil {
		audio.setDuckDb(float64(*c.DuckDb))
		log.Info("the ducking was set", "decibels", *c.DuckDb)
	}
	// The speaker's own tuning, and the microphone's own settings.
	//
	// These are the audio stack's settings rather than the panel's, and
	// they are what make the speaker sound the way the vendor's own
	// firmware makes it sound.
	audio.applyAudioSettings(c)

	if c.WakeWordEnabled != nil {
		wake.SetEnabled(*c.WakeWordEnabled)
		audio.setWakeEnabled(*c.WakeWordEnabled)
		log.Info("the wake word was switched", "enabled", *c.WakeWordEnabled)
		// The microphone follows the wake word: it is open for as long as
		// the wake word is listening, and closed when it is not.
		audio.setListening(*c.WakeWordEnabled && wake.Ready())
	}
	if c.WakeThreshold != nil {
		wake.SetThreshold(*c.WakeThreshold)
		audio.setWakeThreshold(*c.WakeThreshold)
		log.Info("the wake word threshold was set", "threshold", *c.WakeThreshold)
	}
	if c.AudioInput != nil {
		if err := audio.selectInput(*c.AudioInput); err != nil {
			log.Warn("the microphone could not be chosen", "device", *c.AudioInput, "error", err)
		} else {
			// The device that is in use is reported rather than the one
			// that was asked for, because an empty setting means the
			// system's default and saying so is more use than an empty
			// name is.
			log.Info("the microphone was set", "device", audio.currentInput())
		}
	}
	if c.AudioOutput != nil {
		if err := audio.selectOutput(*c.AudioOutput); err != nil {
			log.Warn("the speaker could not be chosen", "device", *c.AudioOutput, "error", err)
		} else {
			log.Info("the speaker was set", "device", audio.currentOutput())
		}
	}
	if c.MonitorMic != nil {
		audio.setMonitoring(*c.MonitorMic)
		log.Info("the microphone monitor was turned on or off", "enabled", *c.MonitorMic)
	}
	if c.SendspinEnabled != nil || c.SendspinName != nil || c.SendspinUnpaired != nil {
		audio.applySendspinConfig(c)
	}
	if c.CameraEnabled != nil {
		control.SetCameraEnabled(*c.CameraEnabled)
	}
	if c.TestSound != nil {
		audio.setTestSound(*c.TestSound)
	}
}

// watchConfig applies the configuration file whenever it changes.
func watchConfig(ctx context.Context, path string, reporter *stateReporter, audio *deviceAudio, wake *wakeWord, control *ControlPlane, log *slog.Logger) {
	if path == "" {
		return
	}

	// The configuration is applied once before the watch starts, so the
	// settings a device was configured with are in force from the first
	// frame rather than from whenever the file happened to change.
	last := ""
	if data, err := os.ReadFile(path); err == nil {
		last = string(data)
		if c, err := readConfig(path); err == nil {
			log.Info("read the device configuration", "file", path)
			applyConfig(c, reporter, audio, wake, control, log)
		} else {
			log.Warn("the device configuration could not be read", "error", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		log.Warn("the device configuration could not be read", "error", err)
	}

	ticker := time.NewTicker(configPollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			data, err := os.ReadFile(path)
			if err != nil {
				if !errors.Is(err, os.ErrNotExist) {
					log.Warn("the device configuration could not be read", "error", err)
				}
				continue
			}
			raw := string(data)
			if raw == last {
				continue
			}
			last = raw

			c, err := readConfig(path)
			if err != nil {
				log.Warn("the device configuration could not be read", "error", err)
				continue
			}
			log.Info("the device configuration changed")
			applyConfig(c, reporter, audio, wake, control, log)
		}
	}
}
