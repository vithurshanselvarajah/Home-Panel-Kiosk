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
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/wilbowes/EchoMuse-SDK/clients/go/echomuse"
	"github.com/wilbowes/EchoMuse-SDK/clients/go/echomuse/display"
	"github.com/wilbowes/EchoMuse-SDK/clients/go/echomuse/protocol"
	"github.com/wilbowes/EchoMuse-SDK/clients/go/echomuse/state"
	"github.com/wilbowes/EchoMuse-SDK/clients/go/echomuse/turn"
	"github.com/wilbowes/EchoMuse-SDK/clients/go/echomuse/vad"

	"github.com/vithurshanselvarajah/Home-Panel-Kiosk/audio"
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
	// Listening is true while the microphone is open.
	Listening bool `json:"listening"`
	// Speaking is true while a response is playing.
	Speaking bool `json:"speaking"`
	// Thinking is true while the controller waits for Home Assistant.
	Thinking bool `json:"thinking"`
	// Muted is the device's own mute switch.
	Muted bool `json:"muted"`

	// Volume is the last volume level the device reported.
	Volume int `json:"volume"`

	// Level is the level of the microphone, as a percentage of full scale.
	// It is what a level meter draws.
	Level int `json:"level"`

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
// The phase says what the controller said about this device, and the link
// says whether the conversation is still open. A device that has reached
// the controller and is waiting for approval is not the same thing as a
// device that cannot reach the controller at all.
func connection(s state.Snapshot) (string, string) {
	switch s.Phase {
	case state.PhaseRefused:
		return "refused", "the controller refused this device's credentials"
	case state.PhasePending:
		return "pending", s.PhaseReason
	case state.PhaseReady:
		if s.LinkUp {
			return "connected", "the controller admitted this device"
		}
		return "disconnected", "the device is admitted but the link is down"
	}
	if s.LinkUp {
		return "connected", "the controller admitted this device"
	}
	if s.Approved {
		return "disconnected", "the device is admitted but the link is down"
	}
	return "disconnected", "the link to the controller is down"
}

// Config is the device configuration the application writes.
//
// Every field is a pointer, so an absent key means "the application did not
// set this" and the device keeps what it had. A plain value would make
// "unset" and "set to zero" the same thing.
type Config struct {
	Board             *string  `json:"board,omitempty"`
	WakeMode          *string  `json:"wakeMode,omitempty"`
	WakeThreshold     *float64 `json:"wakeThreshold,omitempty"`
	WakeWordEnabled   *bool    `json:"wakeWordEnabled,omitempty"`
	WakeSound         *bool    `json:"wakeSound,omitempty"`
	BargeIn           *bool    `json:"bargeIn,omitempty"`
	StartupVolume     *int     `json:"startupVolume,omitempty"`
	MicGainDb         *int     `json:"micGainDb,omitempty"`
	VadThreshold      *float64 `json:"vadThreshold,omitempty"`
	VadSpeechMs       *int     `json:"vadSpeechMs,omitempty"`
	VadSilenceMs      *int     `json:"vadSilenceMs,omitempty"`
	Volume            *int     `json:"volume,omitempty"`
	DuckDb            *float64 `json:"duckDb,omitempty"`
	MuteButton        *bool    `json:"muteButton,omitempty"`
	VolumeButtonSound *bool    `json:"volumeButtonSound,omitempty"`
	// AudioInput is the microphone to use. An empty name is the system
	// default.
	AudioInput *string `json:"audioInput,omitempty"`
	// AudioOutput is the speaker to use. An empty name is the system
	// default.
	AudioOutput *string `json:"audioOutput,omitempty"`
	// MonitorMic opens the microphone so its level can be shown, without
	// sending anything to the controller.
	MonitorMic *bool `json:"monitorMic,omitempty"`
	// TestSound is a counter. Every time it changes, the device plays a
	// short tone through its speaker.
	//
	// A counter rather than a flag, because a flag that stays set is
	// played once and then never again, and the settings page cannot
	// tell that the button did nothing.
	TestSound *int `json:"testSound,omitempty"`
}

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

func main() {
	var (
		controller = flag.String("controller", "", "controller URL; empty discovers by mDNS")
		deviceID   = flag.String("device-id", "home-panel", "stable device identity")
		board      = flag.String("board", "home-panel", "board name, shown in Home Assistant")
		creds      = flag.String("credentials-dir", "", "where the link credential lives")
		stateFile  = flag.String("state-file", "", "write the state here as JSON")
		configFile = flag.String("config-file", "", "read the device configuration from here")
		pair       = flag.Bool("pair", false, "ask the controller to admit this device")
		mic        = flag.String("mic", "", "the microphone to use; empty is the system default")
		speaker    = flag.String("speaker", "", "the speaker to use; empty is the system default")
		verbose    = flag.Bool("verbose", false, "log at debug level")
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

	backend := audio.New(*mic, *speaker)

	device, err := echomuse.New(echomuse.Options{
		DeviceID:       *deviceID,
		Version:        "0.1.0",
		Board:          *board,
		Capabilities:   capabilities(),
		ControllerURL:  *controller,
		CredentialsDir: *creds,
		Logger:         log,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "echomuse-device: %v\n", err)
		os.Exit(1)
	}

	ring := display.NewTracker()
	guard := turn.New(turn.Options{
		Device:   device,
		Detector: vad.New(vad.Default()),
		Log:      log,
	})

	// The audio is the device's own. It is one object, because the voice
	// and the music share one speaker and must not open it twice.
	speaker_ := &deviceAudio{
		backend:        backend,
		device:         device,
		guard:          guard,
		log:            log,
		credentialsDir: *creds,
	}

	device.OnConfig(func(c protocol.Config) {
		log.Info("the controller pushed a configuration", "model", c.OwwModel, "threshold", c.OwwThreshold)
		speaker_.applyConfig(c)
	})
	device.OnMicStart(func(lockMic bool) {
		log.Info("the controller asked for the microphone", "bounded", lockMic)
		speaker_.startCapture(lockMic)
	})
	device.OnMicStop(func() {
		log.Info("the controller stopped the microphone")
		speaker_.stopCapture()
	})
	device.OnSpeaker(func(pcm []byte) {
		speaker_.playVoice(pcm)
	})
	device.OnSpeakerEOS(func() {
		log.Info("the response finished")
		speaker_.finishVoice()
	})
	device.OnDuck(func(on bool) {
		speaker_.setDucked(on)
	})
	device.OnFlush(func(kind string) {
		if kind == "speaker" {
			speaker_.flush()
		}
	})
	device.OnVolumeSet(func(level int) {
		speaker_.setVolume(level)
	})
	device.OnSendspinTokenRequest(func() {
		speaker_.answerSendspinToken()
	})
	device.OnCue(func(cue string) {
		log.Info("the controller asked for a cue", "cue", cue)
	})

	reporter := &stateReporter{
		log:    log,
		device: device,
		ring:   ring,
		guard:  guard,
		audio:  speaker_,
	}
	if *stateFile != "" {
		reporter.file = *stateFile
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	// The audio devices are found before anything is published, so the
	// first state the application reads already carries them.
	reporter.discoverAudio()

	// A device that has never been seen arrives as pending, and the
	// dashboard offers an approval for it. Asking to pair puts the request
	// in front of an administrator instead.
	if *pair {
		device.StartPairing()
	}

	go reporter.run(ctx)
	go watchConfig(ctx, *configFile, guard, reporter, speaker_, log)

	go func() {
		<-ctx.Done()
		device.Stop()
		speaker_.stop()
	}()

	if err := device.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		fmt.Fprintf(os.Stderr, "echomuse-device: %v\n", err)
		os.Exit(1)
	}
}

// capabilities are what this device implements.
//
// Every one of them must be true of the device: announcing a capability and
// not implementing it produces a device that scores, stays silent and looks
// broken.
func capabilities() []protocol.Capability {
	return []protocol.Capability{
		protocol.CapMic,
		protocol.CapSpeaker,
		protocol.CapPairing,
		protocol.CapButtons,
		protocol.CapOWWLocalOnly,
	}
}

// stateReporter turns the SDK's state into the small shape a display wants,
// and publishes it.
type stateReporter struct {
	log    *slog.Logger
	device *echomuse.Device
	ring   *display.Tracker
	guard  *turn.Guard
	audio  *deviceAudio

	// file is where the state is written, if anywhere.
	file string

	mu sync.Mutex
	// last is the state that was published, so a change is not published
	// twice.
	last State
	// haveLast is whether anything has been published yet.
	haveLast bool
}

// discoverAudio finds the audio endpoints the machine has.
//
// A machine with no sound card is not an error: the device still works as a
// display, it just cannot hear or speak.
func (r *stateReporter) discoverAudio() {
	inputs, outputs, err := audio.List()
	if err != nil {
		r.log.Info("no audio devices were found", "error", err)
		return
	}
	r.audio.setEndpoints(inputs, outputs)
	r.log.Info("the audio devices were found", "inputs", len(inputs), "outputs", len(outputs))
}

func (r *stateReporter) run(ctx context.Context) {
	r.device.State().OnChange(func(s state.Snapshot) {
		r.publish(s)
	})
	// The ring's animation changes on its own, so it is sampled rather than
	// waited for.
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.publish(r.device.State().Snapshot())
		}
	}
}

// publish writes the state if it changed.
func (r *stateReporter) publish(s state.Snapshot) {
	conn, reason := connection(s)
	view := r.ring.View()

	next := State{
		Connection:       conn,
		ConnectionReason: reason,
		Pairing:          r.device.PairingOpen(),
		Phase:            string(s.Phase),
		PhaseReason:      s.PhaseReason,
		Listen:           string(s.Listen),
		Listening:        s.Listening,
		Speaking:         s.Speaking,
		Thinking:         s.Thinking,
		Muted:            s.Muted,
		Volume:           s.Volume,
		Level:            r.guard.Detector().Level(),
		Pattern:          string(view.Pattern),
		LastError:        s.LastError,
		UpdatedAt:        time.Now().UTC(),
	}
	for _, c := range view.Palette {
		next.Palette = append(next.Palette, Colour{R: int(c.R), G: int(c.G), B: int(c.B)})
	}

	r.audio.fillState(&next)

	r.mu.Lock()
	if r.haveLast && same(r.last, next) {
		r.mu.Unlock()
		return
	}
	r.last = next
	r.haveLast = true
	r.mu.Unlock()

	if r.file != "" {
		r.writeFile(next)
	}
}

// same compares two states, ignoring the timestamp.
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
	if a.Volume != b.Volume || a.Level != b.Level || a.Pattern != b.Pattern || a.LastError != b.LastError {
		return false
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
	temporary := r.file + ".tmp"
	if err := os.WriteFile(temporary, data, 0o644); err != nil {
		r.log.Error("the state could not be written", "error", err)
		return
	}
	if err := os.Rename(temporary, r.file); err != nil {
		r.log.Error("the state could not be published", "error", err)
	}
}

// applyConfig applies the configuration to the device's settings.
//
// The configuration is a set of instructions, not a picture of the device:
// a key that is absent was not set, and the device keeps what it had.
func applyConfig(c Config, guard *turn.Guard, reporter *stateReporter, audio *deviceAudio, log *slog.Logger) {
	if c.VadThreshold != nil || c.VadSpeechMs != nil || c.VadSilenceMs != nil {
		cfg := guard.Detector().Config()
		if c.VadThreshold != nil {
			cfg.Threshold = *c.VadThreshold
		}
		if c.VadSpeechMs != nil {
			cfg.SpeechMs = *c.VadSpeechMs
		}
		if c.VadSilenceMs != nil {
			cfg.SilenceMs = *c.VadSilenceMs
		}
		guard.Configure(cfg)
		log.Info("the speech detector was reconfigured",
			"threshold", cfg.Threshold, "speech_ms", cfg.SpeechMs, "silence_ms", cfg.SilenceMs)
	}
	if c.Volume != nil {
		audio.setVolume(*c.Volume)
		log.Info("the volume was set", "volume", *c.Volume)
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
	if c.TestSound != nil {
		audio.setTestSound(*c.TestSound)
	}
}

// watchConfig applies the configuration file whenever it changes.
func watchConfig(ctx context.Context, path string, guard *turn.Guard, reporter *stateReporter, audio *deviceAudio, log *slog.Logger) {
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
			applyConfig(c, guard, reporter, audio, log)
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
			applyConfig(c, guard, reporter, audio, log)
		}
	}
}
