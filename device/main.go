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

type State struct {
	Connection string `json:"connection"`

	ConnectionReason string `json:"connection_reason"`

	Pairing bool `json:"pairing"`

	Phase string `json:"phase"`

	PhaseReason string `json:"phase_reason"`

	Listen string `json:"listen"`

	Listening bool `json:"listening"`

	Speaking bool `json:"speaking"`

	Thinking bool `json:"thinking"`

	Transcript string `json:"transcript"`

	Spoken string `json:"spoken"`

	SpokenProgress float64 `json:"spoken_progress"`

	Muted bool `json:"muted"`

	Volume int `json:"volume"`

	Level int `json:"-"`

	Pattern string `json:"pattern"`

	Palette []Colour `json:"palette"`

	AudioInputs []audio.Endpoint `json:"audio_inputs"`

	AudioOutputs []audio.Endpoint `json:"audio_outputs"`

	SelectedAudioInput string `json:"selected_audio_input"`

	SelectedAudioOutput string `json:"selected_audio_output"`

	Monitoring bool `json:"monitoring"`

	MusicState string `json:"music_state"`

	MediaState string `json:"media_state"`

	MusicPaired int `json:"music_paired"`

	MusicBuffered int64 `json:"music_buffered"`

	MusicSynced bool `json:"music_synced"`

	MusicTitle string `json:"music_title"`

	MusicArtist string `json:"music_artist"`

	MusicAlbum string `json:"music_album"`

	MusicArtwork string `json:"music_artwork"`

	MusicPosition int64 `json:"music_position"`

	MusicDuration int64 `json:"music_duration"`

	MusicPlaying bool `json:"music_playing"`

	MusicTransport []string `json:"music_transport,omitempty"`

	LastError string `json:"last_error"`

	UpdatedAt time.Time `json:"updated_at"`
}

type Colour struct {
	R int `json:"r"`
	G int `json:"g"`
	B int `json:"b"`
}

const configPollInterval = time.Second

func connection(s state.Snapshot) (string, string) {
	if s.LinkUp {
		return "connected", "home assistant is connected"
	}
	return "disconnected", "the link to home assistant is down"
}

type Config = config.Config

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

func keepSetting(path string, log *slog.Logger, update func(c *Config)) {
	if path == "" {
		return
	}
	if err := writeConfig(path, update); err != nil {
		log.Warn("the setting could not be kept", "error", err)
	}
}

const lightPollInterval = 2 * time.Second

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

			continue
		}
		control.SetLux(lux)

		if control.AutoBrightness() {
			ceiling := control.BrightnessCeiling()
			level := autobrightness.Level(ceiling, true, lux)
			control.SendCommand(ControlCommand{Action: "auto_brightness_level", Value: level})
		}
	}
}

const defaultVolume = 50

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

	backend := audioBackend(audio.New(*mic, *speaker))
	if *micFile != "" || *speakerFile != "" {
		backend = newFileBackend(*micFile, *speakerFile)
		log.Info("the sound card is replaced by files", "microphone", *micFile, "speaker", *speakerFile)
	}

	identity := resolveIdentity(*creds, *deviceID)
	if identity != *deviceID {
		log.Info("the device keeps the identity its credentials belong to", "asked", *deviceID, "identity", identity)
	}

	wake, wakeErr := openWakeWord(log)
	if wakeErr != nil {
		log.Info("the local wake word is not available", "reason", wakeErr)
	}

	tracker := state.NewTracker()

	speaker_ := &deviceAudio{
		backend:        backend,
		log:            log,
		credentialsDir: *creds,

		wakeWordEnabled: true,

		volume: defaultVolume,
	}

	muteController := newMuteController(log)
	speaker_.mute = muteController

	reporter := &stateReporter{
		log:   log,
		state: tracker,
		audio: speaker_,
		wake:  wake,
	}

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

	speaker_.voiceEnded = reporter.replyEnded

	speaker_.voiceStarted = reporter.replyStarted

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	reporter.discoverAudio()

	if *configFile != "" {
		if c, err := readConfig(*configFile); err == nil {
			applySendspinOnly(c, speaker_, log)
		} else if !errors.Is(err, os.ErrNotExist) {
			log.Warn("the device configuration could not be read", "error", err)
		}
	}

	light, lightErr := ambient.Open()
	if lightErr != nil {
		log.Info("the light sensor is not available", "reason", lightErr)
		light = nil
	} else {
		log.Info("the light sensor is ready")
	}

	control := startControlPlane(ctx, log, *esphomePort, *esphomeKey, *esphomeName, *board, *controlSocket, speaker_, wake, light)
	defer control.Close()

	if *settingsFile != "" {
		if c, err := readConfig(*settingsFile); err == nil {
			applyVoiceSettings(c, speaker_, wake, control, log)
		} else if !errors.Is(err, os.ErrNotExist) {
			log.Warn("the kept settings could not be read", "error", err)
		}
	}

	control.OnLink(reporter.setLink)

	reporter.transcript = control.Transcript
	reporter.spoken = control.Spoken
	reporter.spokenProgress = control.SpokenProgress

	control.OnVoice(esphome.VoiceHooks{
		OnTurn:     reporter.turnStarted,
		OnEnd:      reporter.turnEnded,
		OnThinking: reporter.setThinking,
		OnSpeaking: reporter.setSpeaking,

		OnAudio: speaker_.playReply,

		OnReplyEnd: speaker_.finishReply,

		OnPlayedOut: control.ReplyPlayedOut,

		OnFlush: speaker_.flush,

		OnAnnounce: speaker_.announce,

		OnMicAudio: speaker_.sendMicAudio,

		OnUtteranceEnd: func() {
			if err := control.SendVoiceEnd(); err != nil {
				log.Debug("the end of the utterance could not be sent", "error", err)
			}
		},
	})

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

	control.OnSendspinStarted(func() {
		control.PushCapabilities()
	})

	control.OnMediaPlayer(MediaPlayerHooks{
		OnPlay: func(url string, announcement bool) {
			go speaker_.playMediaURL(ctx, url, announcement)
		},
		OnCommand: func(command esphome.MediaPlayerCommand) {

			if player := speaker_.sendspinPlayer(); player != nil && player.Streaming() {
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

			level := int(volume * 100)
			speaker_.setVolume(level)

			keepSetting(*settingsFile, log, func(c *Config) {
				c.Volume = &level
			})
		},
		OnMute: func(muted bool) {

			log.Info("the speaker's mute was ignored", "muted", muted)
		},
	})

	speaker_.onMediaState = func(state esphome.MediaPlayerState) {
		control.SetMediaPlayerState(state)
	}

	speaker_.onMusicStarted = func(source string) {
		switch source {
		case "home-assistant":

			if player := speaker_.sendspinPlayer(); player != nil {
				player.Transport("pause")
			}
		case "server":

			speaker_.StopMedia()
		}
	}

	speaker_.onReplyEnded = func() {
		control.ReplyPlayedOut()
	}

	var lastLog time.Time

	speaker_.onSendspinStatus = func(status sendspin.Status) {

		if time.Since(lastLog) < time.Second {
			return
		}
		lastLog = time.Now()
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

	control.OnTransport(func(command TransportCommand) {

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

	speaker_.onVolume = func(volume float32) {
		control.SetMediaPlayerVolume(volume)
	}

	speaker_.onPanelsVolume = func(level int) {
		control.SendVolume(level)
	}

	speaker_.onAnnounceFinished = func() {
		control.AnnounceFinished()
	}

	speaker_.onWakeAudio = func(pcm []byte) {
		wake.Push(pcm, time.Now())
	}

	speaker_.onWakeReset = func() {
		wake.Reset()
	}

	if wake != nil && wake.Enabled() {
		speaker_.setListening(true)
	}

	reportWakeWord := func() {
		model := ""
		if wake != nil {
			model = wake.Model()
		}
		control.SetWakeWord(model, wake.Ready(), wake.Enabled())
	}
	reportWakeWord()

	control.OnWakeWord(func(enabled bool) {
		wake.SetEnabled(enabled)
		speaker_.setWakeEnabled(enabled)
		speaker_.setListening(enabled && wake.Ready())

		control.PublishVoiceSettings()
	})

	wake.SetBargeThreshold(bargeInThreshold, speaker_.speaking)

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
	control.OnLouderSpeech(func(on bool) {
		speaker_.setLouderSpeech(on)
		log.Info("louder speech was switched", "on", on)
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

	control.OnAutoBrightness(func(on bool) {
		log.Info("the automatic brightness was switched", "on", on)
		keepSetting(*settingsFile, log, func(c *Config) {
			c.AutoBrightness = &on
		})
	})

	muteController.OnChange(func(muted bool) {
		log.Info("the microphone mute changed", "muted", muted)

		if err := setMicMuteLED(muted); err != nil {
			log.Debug("the mute light could not be set", "error", err)
		}
		reporter.setMuted(muted)
	})
	go muteController.Watch(ctx)

	if err := setMicMuteLED(muteController.Get()); err != nil {
		log.Debug("the mute light could not be set", "error", err)
	}

	control.OnVolume(func(level int) {
		speaker_.setVolume(level)

		keepSetting(*settingsFile, log, func(c *Config) {
			c.Volume = &level
		})
	})

	speaker_.onSendMicAudio = func(pcm []byte) {
		if err := control.SendVoiceAudio(pcm, false); err != nil {
			log.Debug("the microphone did not reach home assistant", "error", err)
		}
	}

	startTurn := func(phrase string) {
		log.Info("the wake word fired", "phrase", phrase)
		if err := control.StartTurn(phrase); err != nil {
			log.Warn("the turn could not be started", "error", err)
		}
	}
	if wake != nil {
		wake.onCross = startTurn
	}

	control.OnListen(startTurn)

	go reporter.run(ctx)
	go watchConfig(ctx, *configFile, reporter, speaker_, wake, control, log)
	go watchLight(ctx, light, control, log)

	deviceButtons := newButtons(log)
	deviceButtons.onVolume = func(delta int) {
		speaker_.stepVolume(delta)
	}
	deviceButtons.onMute = func() {

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

	speaker_.stop()

	speaker_.stopPortedSpeaker()
	if wake != nil {
		wake.Close()
	}

	if err := muteController.Close(); err != nil {
		log.Debug("the microphone latch could not be released", "error", err)
	}

	clearRuntime(identity)
}

type stateReporter struct {
	log   *slog.Logger
	state *state.Tracker
	audio *deviceAudio

	wake *wakeWord

	transcript func() string

	spoken func() string

	spokenProgress func() float64

	file string

	levelFile string

	mu sync.Mutex

	writeMu sync.Mutex

	last State

	haveLast bool

	speaking bool

	listening bool

	lastLevel int

	lastLevelWrite time.Time
}

const levelWriteInterval = 250 * time.Millisecond

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

func (r *stateReporter) turnStarted(phrase string) {
	r.mu.Lock()
	r.listening = true
	r.mu.Unlock()

	r.state.SetPhase(state.PhaseListening, "the wake word fired")
	r.state.SetListening(true)
	r.audio.startCapture()
	r.audio.setStreaming(true)
}

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

	audio := r.audio
	audio.setStreaming(false)
	audio.maybeStopCapture()
}

func (r *stateReporter) setLink(up bool) {
	r.state.SetLink(up)
}

func (r *stateReporter) setThinking(thinking bool) {
	r.state.SetThinking(thinking)
	if thinking {
		r.state.SetPhase(state.PhaseThinking, "home assistant is working on it")
	}
}

func (r *stateReporter) setSpeaking(speaking bool) {
	if !speaking {
		return
	}
	r.replyStarted()
}

func (r *stateReporter) setMuted(muted bool) {
	r.state.SetMuted(muted)
}

func (r *stateReporter) wakeLevel() int {
	if r.wake == nil {
		return 0
	}
	return r.wake.Amplitude()
}

func (r *stateReporter) replyStarted() {
	r.mu.Lock()
	r.speaking = true
	r.mu.Unlock()

	r.state.SetSpeaking(true)
	r.state.SetPhase(state.PhaseSpeaking, "")
}

func (r *stateReporter) replyEnded() {
	r.mu.Lock()
	r.speaking = false
	r.mu.Unlock()

	r.state.SetSpeaking(false)
	r.state.SetPhase(state.PhaseIdle, "")
}

func (r *stateReporter) publish(s state.Snapshot) {
	conn, reason := connection(s)

	if !s.LinkUp {
		r.replyEnded()
		r.mu.Lock()
		r.listening = false
		r.mu.Unlock()
		r.state.SetListening(false)
	}

	r.mu.Lock()

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

	r.audio.fillState(&next)

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

func (r *stateReporter) writeLevel(level int) {
	if r.levelFile == "" {
		return
	}
	r.mu.Lock()
	now := time.Now()
	due := now.Sub(r.lastLevelWrite) >= levelWriteInterval
	moved := abs(level-r.lastLevel) > levelTolerance
	if due {

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

const levelTolerance = 1

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

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

	if a.Transcript != b.Transcript || a.Spoken != b.Spoken {
		return false
	}

	if a.SpokenProgress != b.SpokenProgress {
		return false
	}
	if a.Monitoring != b.Monitoring {
		return false
	}
	if a.Volume != b.Volume || a.Pattern != b.Pattern || a.LastError != b.LastError {
		return false
	}

	if a.MusicState != b.MusicState || a.MusicPaired != b.MusicPaired ||
		a.MusicBuffered != b.MusicBuffered || a.MusicSynced != b.MusicSynced {
		return false
	}

	if a.MusicTitle != b.MusicTitle || a.MusicArtist != b.MusicArtist ||
		a.MusicAlbum != b.MusicAlbum || a.MusicArtwork != b.MusicArtwork ||
		a.MusicPlaying != b.MusicPlaying {
		return false
	}
	if a.MusicPosition != b.MusicPosition || a.MusicDuration != b.MusicDuration {
		return false
	}

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

func applySendspinOnly(c Config, audio *deviceAudio, log *slog.Logger) {
	if c.SendspinEnabled == nil && c.SendspinName == nil && c.SendspinUnpaired == nil {
		return
	}
	audio.applySendspinConfig(c)
	log.Info("the music player was configured")
}

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

	audio.applyAudioSettings(c)

	if control != nil {
		control.SetAudioSettings()
	}

	if c.WakeWordEnabled != nil {
		wake.SetEnabled(*c.WakeWordEnabled)
		audio.setWakeEnabled(*c.WakeWordEnabled)
		log.Info("the wake word was switched", "enabled", *c.WakeWordEnabled)

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

func watchConfig(ctx context.Context, path string, reporter *stateReporter, audio *deviceAudio, wake *wakeWord, control *ControlPlane, log *slog.Logger) {
	if path == "" {
		return
	}

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
