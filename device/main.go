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
	"github.com/vithurshanselvarajah/Home-Panel-Kiosk/device/config"
	"github.com/vithurshanselvarajah/Home-Panel-Kiosk/device/esphome"
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

func main() {
	var (
		deviceID      = flag.String("device-id", "home-panel", "stable device identity")
		board         = flag.String("board", "home-panel", "board name, shown in Home Assistant")
		creds         = flag.String("credentials-dir", "", "where the link credential lives")
		stateFile     = flag.String("state-file", "", "write the state here as JSON")
		levelFile     = flag.String("level-file", "", "write the live microphone level here; empty derives it from the state file")
		configFile    = flag.String("config-file", "", "read the device configuration from here")
		mic           = flag.String("mic", "", "the microphone to use; empty is the system default")
		speaker       = flag.String("speaker", "", "the speaker to use; empty is the system default")
		verbose       = flag.Bool("verbose", false, "log at debug level")
		esphomePort   = flag.Int("esphome-port", 0, "the port the Home Assistant API is on; zero picks one")
		esphomeKey    = flag.String("esphome-key", "", "the base64 encryption key for the Home Assistant API")
		esphomeName   = flag.String("esphome-name", "home-panel", "the name Home Assistant knows the panel by")
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

	backend := audio.New(*mic, *speaker)

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
	}

	// The reporter is made before the callbacks are wired, because the
	// callbacks tell it when a reply starts and ends and it is what
	// publishes that.
	reporter := &stateReporter{
		log:    log,
		state:  tracker,
		audio:  speaker_,
		wake:   wake,
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

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	// The audio devices are found before anything is published, so the
	// first state the application reads already carries them.
	reporter.discoverAudio()

	// The control plane: the Home Assistant connection and the socket
	// the panel talks over.
	control := startControlPlane(ctx, log, *esphomePort, *esphomeKey, *esphomeName, *board, *controlSocket)
	defer control.Close()

	// The voice assistant is the control plane's. The panel asks it to
	// start a turn when the wake word fires, and it streams the
	// microphone to home assistant for the length of the turn.
	control.OnVoice(esphome.VoiceHooks{
		OnTurn:     reporter.turnStarted,
		OnEnd:      reporter.turnEnded,
		OnThinking: reporter.setThinking,
		OnSpeaking: reporter.setSpeaking,
		OnAudio:    speaker_.playReply,
	})

	// The wake word fires, the turn starts. The panel opens the
	// microphone for the turn and closes it when home assistant says the
	// utterance is over.
	if wake != nil {
		wake.onCross = func(phrase string) {
			log.Info("the wake word fired", "phrase", phrase)
			if err := control.StartTurn(phrase); err != nil {
				log.Warn("the turn could not be started", "error", err)
			}
		}
	}

	go reporter.run(ctx)
	go watchConfig(ctx, *configFile, reporter, speaker_, wake, log)

	go func() {
		<-ctx.Done()
		speaker_.stop()
		// The memory the live state lived in is given back at once, rather
		// than when the last reference to it goes. The kernel would do it
		// anyway, but a device that is restarted often would leave a
		// directory behind each time.
		clearRuntime(identity)
	}()

	<-ctx.Done()
}

// stateReporter turns the panel's state into the small shape a display
// wants, and publishes it.
type stateReporter struct {
	log   *slog.Logger
	state *state.Tracker
	audio *deviceAudio
	// wake is the local wake word, when there is one.
	wake *wakeWord

	// file is where the state is written, if anywhere.
	file string
	// levelFile is where the live microphone level is written.
	//
	// It is a file of its own because the level is not a state: it
	// changes ten times a second, and a write for every change would
	// write to the flash ten times a second for ever.
	levelFile string

	mu sync.Mutex
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
}

// turnEnded records that a turn finished.
//
// The microphone is closed, because home assistant has heard what it
// needed and the room is the panel's own again.
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
	r.audio.stopCapture()
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
func (r *stateReporter) setSpeaking(speaking bool) {
	r.mu.Lock()
	r.speaking = speaking
	r.mu.Unlock()

	r.state.SetSpeaking(speaking)
	if speaking {
		r.state.SetPhase(state.PhaseSpeaking, "")
	}
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
}

// replyEnded records that a reply finished.
//
// It is called when the reply ends and when the link drops, so the
// indicator does not stay on speaking for the life of the process.
func (r *stateReporter) replyEnded() {
	r.mu.Lock()
	r.speaking = false
	r.mu.Unlock()
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
	// with it.
	r.speaking = r.speaking && s.LinkUp
	speaking := r.speaking
	listening := r.listening && s.LinkUp
	r.mu.Unlock()

	next := State{
		Connection:       conn,
		ConnectionReason: reason,
		Phase:            string(s.Phase),
		PhaseReason:      s.PhaseReason,
		Listen:           string(s.Listen),
		// The device is listening while a turn is running. The
		// microphone is open for the turn and no longer, and the level
		// meter opens it too, so the meter is left out of it.
		Listening: listening && !r.audio.monitoringMic(),
		Speaking:  speaking,
		Thinking:  s.Thinking,
		Muted:     s.Muted,
		Volume:    s.Volume,
		Level:     r.wakeLevel(),
		LastError: s.LastError,
		UpdatedAt: time.Now().UTC(),
	}

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
	if a.Monitoring != b.Monitoring {
		return false
	}
	if a.Volume != b.Volume || a.Pattern != b.Pattern || a.LastError != b.LastError {
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

// applyConfig applies the configuration to the panel's settings.
//
// The configuration is a set of instructions, not a picture of the panel:
// a key that is absent was not set, and the panel keeps what it had.
func applyConfig(c Config, reporter *stateReporter, audio *deviceAudio, wake *wakeWord, log *slog.Logger) {
	if c.Volume != nil {
		audio.setVolume(*c.Volume)
		log.Info("the volume was set", "volume", *c.Volume)
	}
	if c.StartupVolume != nil {
		audio.setVolume(*c.StartupVolume)
		log.Info("the startup volume was set", "volume", *c.StartupVolume)
	}
	if c.DuckDb != nil {
		audio.setDuckDb(float64(*c.DuckDb))
		log.Info("the ducking was set", "decibels", *c.DuckDb)
	}
	if c.WakeWordEnabled != nil {
		wake.SetEnabled(*c.WakeWordEnabled)
		log.Info("the wake word was switched", "enabled", *c.WakeWordEnabled)
	}
	if c.WakeThreshold != nil {
		wake.SetThreshold(*c.WakeThreshold)
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
	if c.MuteButton != nil {
		audio.setMuted(*c.MuteButton)
		log.Info("the mute switch was turned on or off", "muted", *c.MuteButton)
	}
	if c.SendspinEnabled != nil || c.SendspinName != nil || c.SendspinUnpaired != nil {
		audio.applySendspinConfig(c)
	}
	if c.TestSound != nil {
		audio.setTestSound(*c.TestSound)
	}
}

// watchConfig applies the configuration file whenever it changes.
func watchConfig(ctx context.Context, path string, reporter *stateReporter, audio *deviceAudio, wake *wakeWord, log *slog.Logger) {
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
			applyConfig(c, reporter, audio, wake, log)
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
			applyConfig(c, reporter, audio, wake, log)
		}
	}
}
