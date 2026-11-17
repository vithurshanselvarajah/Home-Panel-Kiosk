package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"github.com/vithurshanselvarajah/Home-Panel-Kiosk/audio/asp"
	"github.com/vithurshanselvarajah/Home-Panel-Kiosk/audio/mic/framework"
	"github.com/vithurshanselvarajah/Home-Panel-Kiosk/audio/speaker"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"sync"

	"github.com/vithurshanselvarajah/Home-Panel-Kiosk/device/esphome"
)

// The control plane.
//
// The panel and the device program are two processes: the panel is a
// web page and the device program owns the sound card and the link to
// Home Assistant. They are told about each other through a socket.
//
// A socket rather than a file. The state changes when the device does
// something, and a file that is polled is a file that is read over and
// over to find out whether it changed, and the panel is a display that
// only redraws when it is told something changed. With a socket the
// change arrives the moment it happens.
//
// The socket is in memory, because it is a live reading and not a
// record, and the device is on eMMC.

// ControlCommand is what the device program asks the panel to do.
//
// The names are the ones the panel has always used, so an automation
// that was written against the old broker still works.
type ControlCommand struct {
	// Action is what to do.
	Action string `json:"action"`
	// Value is the value, for the commands that take one.
	Value any `json:"value,omitempty"`
	// Setting is the panel setting a Home Assistant configuration entity changes.
	Setting string `json:"setting,omitempty"`
	// Mode is the mode, for the commands that take one.
	Mode string `json:"mode,omitempty"`
	// URL is the address, for the command that takes one.
	URL string `json:"url,omitempty"`
	// Title is the title of a notification.
	Title string `json:"title,omitempty"`
	// Message is the text of a notification.
	Message string `json:"message,omitempty"`
	// Duration is how long a notification stays.
	Duration int `json:"duration,omitempty"`
	// Priority is how a notification matters.
	Priority string `json:"priority,omitempty"`
}

// ControlStateUpdate is what the panel tells the device program.
//
// The panel is the one that knows what it is doing: it is the thing
// being displayed. The device program turns this into the entities
// Home Assistant sees.
type ControlStateUpdate struct {
	// Action is "state". It is there so the two directions can be told
	// apart on the same socket.
	Action string `json:"action"`
	// State is the state of the panel.
	State ControlState `json:"value"`
}

// ControlState is what the device program tells the panel.
//
// It is the state of the panel's own entities, so the panel can draw
// them and Home Assistant can see them.
type ControlState struct {
	// Mode is what the panel is showing.
	Mode string `json:"mode"`
	// DisplayIsOff says whether the screen is off.
	DisplayIsOff bool `json:"displayIsOff"`
	// KeyboardVisible says whether the touch keyboard is up.
	KeyboardVisible bool `json:"keyboardVisible"`
	// SidePanelOpen says whether the drawer is open.
	SidePanelOpen bool `json:"sidePanelOpen"`
	// DisplayMode is whether the panel is fullscreen or in a window.
	DisplayMode string `json:"displayMode"`
	// Brightness is the screen brightness.
	Brightness int `json:"brightness"`
	// Volume is the volume of the room.
	//
	// It is the speaker's volume, and the panel's own slider sets it.
	// A panel that has not read the mixer sends nothing rather than
	// zero: zero is a volume, and sending it would silence the speaker.
	Volume *int `json:"volume,omitempty"`
	// LastActivity is when somebody last did something on the panel.
	LastActivity string `json:"lastActivity"`
	// Version is the version of the application.
	Version                string `json:"version"`
	IdleToSlideshowMinutes int    `json:"idleToSlideshowMinutes"`
	SlideshowToOffMinutes  int    `json:"slideshowToOffMinutes"`
	TouchKeyboardEnabled   bool   `json:"touchKeyboardEnabled"`
	MusicEnabled           bool   `json:"musicEnabled"`
	MusicUnpaired          bool   `json:"musicUnpaired"`
}

// ControlServer is the socket the panel and the device program share.
//
// The device program owns the Home Assistant connection and the
// entities that go with it. The panel asks for changes here and is
// told what the state is.
type ControlServer struct {
	// socket is where the socket is.
	socket string
	// registry holds the entities Home Assistant sees.
	registry *esphome.Registry
	// brightnessCeiling is the brightness the person set, as a
	// percentage: the brightest the panel is allowed to be. It is what the
	// brightness entity reports, because with the automatic brightness on
	// the level the screen is at is not what the person set.
	brightnessCeiling int
	// log is where the messages go.
	log *slog.Logger
	// onCommand is called when the panel asks for something.
	onCommand func(ControlCommand)
	// sendspinPairingToken reads the pairing token from the device's Sendspin store.
	sendspinPairingToken func() (string, error)
	setCameraPassword    func(string)
	onCameraConnected    func(string)
	cameraPassword       string
	// onMute is gone: the panel cannot mute the microphone.
	//
	// The mute is a hardware latch on this board, and the latch is
	// one-way from software: the button on top of the unit sets it and
	// only the button releases it. A panel that could ask for the
	// microphones to be cut would be a panel that could cut them and
	// then not bring them back, so the panel is told where the latch
	// is and nothing more.
	// onVolume is called when the panel reports a volume.
	//
	// The panel's volume is the speaker's volume, and the speaker is
	// what Home Assistant shows. So the panel's volume is handed on, and
	// the speaker takes it.
	//
	// The two do not chase each other, because each only acts on what
	// it has just been told: the panel sends its volume when it changes
	// and the speaker sends its volume when it changes, and a change
	// settles rather than going round for ever.
	onVolume func(level int)
	// onBrightness is called when the panel reports a brightness.
	//
	// The brightness the person set is the ceiling the automatic
	// brightness works under, so the device program needs to hear it.
	onBrightness func(percent int)
	// onTransport is called when the panel asks for the music transport
	// to be driven.
	//
	// The transport belongs to the music server and the panel has the
	// buttons, so the request goes here and is passed on to the server.
	onTransport func(command TransportCommand)
	// onListen is called when something asks the device to start a
	// turn.
	//
	// The wake word normally starts one, and the wake word is local.
	// But a panel can be asked to start one by something else — an
	// automation, or a person pressing a button on the screen — and the
	// turn is the same either way.
	onListen func(phrase string)
	// mu guards the state and the listener.
	mu sync.Mutex
	// listener is the socket, when it is listening.
	listener net.Listener
	// state is what the panel is told.
	state ControlState
	// connected says whether Home Assistant is talking to the device
	// program.
	connected bool
	// connections are the panels that are connected.
	connections sync.Map
	// writeMu guards the writes on each connection.
	writeMu sync.Mutex
}

// ControlOptions is what the control server is made with.
type ControlOptions struct {
	// Socket is where the socket is.
	Socket string
	// Registry holds the entities Home Assistant sees.
	Registry *esphome.Registry
	// Log is where the messages go.
	Log *slog.Logger
	// OnCommand is called when the panel asks for something.
	OnCommand func(ControlCommand)
	// SendspinPairingToken returns the pairing token without copying it into app settings.
	SendspinPairingToken func() (string, error)
	// SetCameraPassword applies the camera password received over the private local socket.
	SetCameraPassword func(string)
	OnCameraConnected func(string)
}

// NewControlServer makes the control server.
func NewControlServer(options ControlOptions) *ControlServer {
	if options.Log == nil {
		options.Log = slog.Default()
	}
	return &ControlServer{
		socket:               options.Socket,
		registry:             options.Registry,
		log:                  options.Log,
		onCommand:            options.OnCommand,
		sendspinPairingToken: options.SendspinPairingToken,
		setCameraPassword:    options.SetCameraPassword,
		onCameraConnected:    options.OnCameraConnected,
	}
}

// Listen starts serving the socket.
//
// The socket is removed first if it is there: a socket that is left
// behind by a program that did not shut down properly would stop the
// new one from binding it.
func (s *ControlServer) Listen() error {
	if s.socket == "" {
		return fmt.Errorf("home-panel: the control socket has no name")
	}
	if err := os.MkdirAll(filepath.Dir(s.socket), 0o700); err != nil {
		return fmt.Errorf("home-panel: the control directory could not be made: %w", err)
	}
	if err := os.Remove(s.socket); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("home-panel: the old control socket could not be removed: %w", err)
	}
	listener, err := net.Listen("unix", s.socket)
	if err != nil {
		return fmt.Errorf("home-panel: the control socket could not be opened: %w", err)
	}
	s.mu.Lock()
	s.listener = listener
	s.mu.Unlock()

	go s.accept(listener)
	return nil
}

// accept takes panels as they arrive.
func (s *ControlServer) accept(listener net.Listener) {
	for {
		conn, err := listener.Accept()
		if err != nil {
			s.log.Debug("the control socket stopped taking panels", "error", err)
			return
		}
		go s.serve(conn)
	}
}

// serve handles one panel.
func (s *ControlServer) serve(conn net.Conn) {
	defer conn.Close()
	s.log.Debug("a panel connected")
	s.connections.Store(conn, struct{}{})
	defer s.connections.Delete(conn)

	// The panel is told the link state as soon as it arrives, so it
	// does not have to wait for Home Assistant to come and go before it
	// knows where things stand.
	s.mu.Lock()
	connected := s.connected
	s.mu.Unlock()
	_ = s.writeMessage(conn, ControlMessage{Action: "link", Connected: connected})
	if s.onCameraConnected != nil {
		s.mu.Lock()
		password := s.cameraPassword
		s.mu.Unlock()
		s.onCameraConnected(password)
	}

	// The reader is made once and kept: a reader made per message
	// would throw away whatever it had read ahead.
	reader := bufio.NewReader(conn)
	for {
		message, err := readMessage(reader)
		if err != nil {
			s.log.Debug("the panel's connection ended", "error", err)
			return
		}
		// A panic in the handling takes the whole program down with it,
		// because a panic in a goroutine is not recovered by the code
		// that started it. It is recovered here instead, and the panel is
		// disconnected so that it reconnects to a program that is
		// working.
		func() {
			defer func() {
				if recovered := recover(); recovered != nil {
					s.log.Error("the panel's message could not be handled", "panic", recovered)
				}
			}()
			s.handle(conn, message)
		}()
	}
}

// handle acts on a message from the panel.
//
// The panel sends its state, and the device program turns it into the
// entities Home Assistant sees. It also asks for the music transport to
// be driven, which is the one thing the panel asks for: the transport
// belongs to the music server, and the panel is the one with the
// buttons.
func (s *ControlServer) handle(conn net.Conn, message ControlMessage) {
	switch message.Action {
	case "state":
		s.SetState(message.State)
	case "camera_password":
		s.mu.Lock()
		s.cameraPassword = message.CameraPassword
		password := s.cameraPassword
		s.mu.Unlock()
		if s.setCameraPassword != nil {
			s.setCameraPassword(password)
		}
	case "sendspin_token_request":
		token := ""
		var err error
		if s.sendspinPairingToken == nil {
			err = fmt.Errorf("Sendspin pairing is unavailable")
		} else {
			token, err = s.sendspinPairingToken()
		}
		response := ControlMessage{Action: "sendspin_token_response", RequestID: message.RequestID, SendspinToken: token}
		if err != nil {
			response.Error = err.Error()
		}
		if err := s.writeMessage(conn, response); err != nil {
			s.log.Debug("the Sendspin pairing token could not be returned", "error", err)
		}
	case "transport":
		s.mu.Lock()
		handler := s.onTransport
		s.mu.Unlock()
		if handler != nil {
			// The transport is written down, because a button that does
			// nothing is the hardest kind of fault to see: the panel
			// says nothing, the device program says nothing, and the
			// music carries on.
			s.log.Info("the panel asked for the transport", "action", message.Command.Action,
				"source", message.Command.Source)
			handler(message.Command)
		}
	case "mute":
		// A mute from the panel is ignored.
		//
		// The mute is the hardware latch's, and the latch is one-way
		// from software, so there is nothing the panel could ask for
		// that the hardware would do.
	case "volume":
		// The panel sets the volume.
		//
		// The level is the device program's, the same way the mute is:
		// it applies the level to everything it plays — the reply, the
		// music and the test sound — so the panel asks for it rather
		// than reaching for the mixer itself.
		s.mu.Lock()
		handler := s.onVolume
		s.mu.Unlock()
		if handler != nil {
			handler(message.Volume)
		}
	case "listen":
		// The panel is asking the device to start listening.
		//
		// It is the wake word that normally starts a turn, and the wake
		// word is local. But a panel can be asked to start one by
		// something else — an automation, or a person pressing a
		// button on the screen — and the turn is the same either way.
		s.mu.Lock()
		handler := s.onListen
		s.mu.Unlock()
		if handler != nil {
			handler(message.Phrase)
		}
	default:
		s.log.Debug("the panel sent something unexpected", "action", message.Action)
	}
}

// ControlMessage is what the panel sends.
//
// It is one shape for both directions, so the socket is one stream
// rather than two.
type ControlMessage struct {
	// Action is what the message is about.
	Action string `json:"action"`
	// State is the state of the panel, for the state message.
	State ControlState `json:"value"`
	// Command is the transport command, for the transport message.
	Command TransportCommand `json:"command"`
	// Phrase is the wake word that started the turn, for the listen
	// message.
	Phrase string `json:"phrase,omitempty"`
	// Connected says whether Home Assistant is talking to the device
	// program, for the link message.
	//
	// It is always sent, even when it is false, because "not connected"
	// is the answer half the time.
	Connected bool `json:"connected"`
	// Muted says whether the microphone is muted, for the mute message.
	//
	// The mute is the microphone's, so the panel asks the device
	// program for it: the device program owns the microphone.
	Muted bool `json:"muted,omitempty"`
	// Volume is the level the panel asks for, for the volume message.
	//
	// The volume is the device program's, the same way the mute is: the
	// device program owns the speaker and applies the level to
	// everything it plays. The panel is a control surface for it.
	Volume         int    `json:"volume,omitempty"`
	CameraPassword string `json:"cameraPassword,omitempty"`
	RequestID      string `json:"requestId,omitempty"`
	SendspinToken  string `json:"sendspinToken,omitempty"`
	Error          string `json:"error,omitempty"`
}

// TransportCommand is what the panel asks of the music transport.
//
// The names are the ones the panel uses for its buttons, and they are
// turned into the server's own names on the way through.
type TransportCommand struct {
	// Action is what to do.
	//
	// It is "play", "pause", "next", "previous" or "seek".
	Action string `json:"action"`
	// Source says which music the command is for.
	//
	// It is "server" for the music a music server is playing and
	// "home" for the music Home Assistant is playing. The two are
	// different things and each has its own transport.
	Source string `json:"source,omitempty"`
	// PositionMs is where to seek to, in milliseconds. It is only used
	// by a seek.
	PositionMs int64 `json:"positionMs,omitempty"`
}

// SetConnected records whether Home Assistant is talking to the device
// program.
//
// The panel shows this in the drawer, and it cannot find out for itself:
// the socket between the panel and the device program is on this
// machine and is up whatever Home Assistant is doing.
func (s *ControlServer) SetConnected(connected bool) {
	s.mu.Lock()
	changed := s.connected != connected
	s.connected = connected
	s.mu.Unlock()
	if !changed {
		return
	}
	s.connections.Range(func(key, _ any) bool {
		conn, ok := key.(net.Conn)
		if !ok {
			return true
		}
		if err := s.writeMessage(conn, ControlMessage{Action: "link", Connected: connected}); err != nil {
			s.log.Debug("the link state could not be sent", "error", err)
		}
		return true
	})
}

// writeMessage writes one message to one panel.
func (s *ControlServer) writeMessage(conn net.Conn, message ControlMessage) error {
	payload, err := json.Marshal(message)
	if err != nil {
		return err
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	_, err = conn.Write(append(payload, '\n'))
	return err
}

// SetState records the state of the panel.
//
// This is how the panel's own state reaches Home Assistant: the
// entities are changed here, and the change is pushed.
// SetListenHandler sets what is called when something asks the device
// to start a turn.
func (s *ControlServer) SetListenHandler(fn func(phrase string)) {
	s.mu.Lock()
	s.onListen = fn
	s.mu.Unlock()
}

// SetVolumeHandler sets what is called when the panel asks for a volume.
//
// The level is the device program's, because it applies the level to
// everything it plays. The panel is a control surface for it.
func (s *ControlServer) SetVolumeHandler(fn func(level int)) {
	s.mu.Lock()
	s.onVolume = fn
	s.mu.Unlock()
}

// SetTransportHandler sets what is called when the panel asks for the
// music transport to be driven.
func (s *ControlServer) SetTransportHandler(fn func(command TransportCommand)) {
	s.mu.Lock()
	s.onTransport = fn
	s.mu.Unlock()
}

// SetVolumeFollower sets what is called when the panel reports a volume.
//
// The panel's volume is the speaker's volume, and the speaker is what
// Home Assistant shows. So the panel's volume is handed on, and the
// speaker takes it.
//
// The two do not chase each other, because each only acts on what it has
// just been told: the panel sends its volume when it changes and the
// speaker sends its volume when it changes, and a change settles rather
// than going round for ever.
func (s *ControlServer) SetVolumeFollower(fn func(level int)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onVolume = fn
}

// SetBrightnessFollower sets what is called when the panel reports a
// brightness.
//
// The brightness the person set is the ceiling the automatic brightness
// works under, so the device program needs to hear it.
func (s *ControlServer) SetBrightnessFollower(fn func(percent int)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onBrightness = fn
}

func (s *ControlServer) SetState(state ControlState) {
	s.mu.Lock()
	changed := s.state != state
	volumeChanged := s.state.Volume == nil != (state.Volume == nil) ||
		(state.Volume != nil && s.state.Volume != nil && *s.state.Volume != *state.Volume)
	brightnessChanged := s.state.Brightness != state.Brightness
	s.state = state
	follower := s.onVolume
	brightnessFollower := s.onBrightness
	s.mu.Unlock()
	if !changed {
		// The entities are published even when the state has not changed.
		//
		// They carry a guess until the panel first reports, and a
		// reconnecting panel with an identical state publishes nothing.
		// Home Assistant would then be left with the guess for ever,
		// and the guess is not what the panel is doing.
		s.applyEntities(state)
		return
	}
	s.applyEntities(state)

	// The brightness is handed on when it changes, and only then: it is
	// the ceiling the automatic brightness works under.
	if brightnessChanged && brightnessFollower != nil {
		brightnessFollower(state.Brightness)
	}

	// The volume is handed on when it changes, and only then: a change
	// that is passed on every time would go round for ever between the
	// panel and the speaker.
	//
	// A panel that has not read the mixer yet reports no volume, and a
	// panel that reports none is not saying the volume is zero. It is
	// saying it does not know, and telling the speaker that would
	// silence it.
	if volumeChanged && state.Volume != nil && *state.Volume > 0 && follower != nil {
		follower(*state.Volume)
	}
}

// State returns the state of the panel.
func (s *ControlServer) State() ControlState {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state
}

// SetBrightnessCeiling records the brightness the person set.
//
// It is what the brightness entity reports: with the automatic brightness
// on, the level the screen is at is the room's light and not the person's
// setting, and publishing that would mean Home Assistant sets sixty and
// sees twelve back.
func (s *ControlServer) SetBrightnessCeiling(percent int) {
	s.mu.Lock()
	s.brightnessCeiling = percent
	s.mu.Unlock()
}

// SetLux publishes the room's light.
//
// It is a reading, so it is published as it is read.
func (s *ControlServer) SetLux(lux float64) {
	if s.registry == nil {
		return
	}
	for _, entity := range s.registry.All() {
		if entity.ObjectID != "light_lux" {
			continue
		}
		s.registry.SetState(entity.Key, esphome.EntityValue{
			Kind: esphome.KindSensor, Number: lux,
		})
	}
}

// SetAutoBrightness publishes whether the screen follows the room.
func (s *ControlServer) SetAutoBrightness(on bool) {
	if s.registry == nil {
		return
	}
	for _, entity := range s.registry.All() {
		if entity.ObjectID != "auto_brightness" {
			continue
		}
		s.registry.SetState(entity.Key, esphome.EntityValue{
			Kind: esphome.KindSwitch, Bool: on,
		})
	}
}

// SetVoiceSettings puts the voice settings into the entities.
//
// The voice settings are the device's own rather than the panel's, so
// they are not in the panel's state. They are pushed into the entities
// so Home Assistant can read them, and so a change made in Home
// Assistant is not undone by the next state the panel sends.
//
// The mute is not among them: it is the hardware latch, and software
// cannot release it, so there is no switch to offer.
func (s *ControlServer) SetVoiceSettings(threshold float64, wakeEnabled bool, duckDB float64) {
	if s.registry == nil {
		return
	}
	for _, entity := range s.registry.All() {
		switch entity.ObjectID {
		case "wake_word_threshold":
			s.registry.SetState(entity.Key, esphome.EntityValue{
				Kind: esphome.KindNumber, Number: threshold * 100,
			})
		case "wake_word_enabled":
			s.registry.SetState(entity.Key, esphome.EntityValue{
				Kind: esphome.KindSwitch, Bool: wakeEnabled,
			})
		case "music_ducking_db":
			s.registry.SetState(entity.Key, esphome.EntityValue{
				Kind: esphome.KindNumber, Number: duckDB,
			})
		}
	}
}

// SetAudioSettings tells Home Assistant what the speaker and the
// microphone are actually doing.
//
// Every setting the panel can change is reported back, so that the entity
// shows what the device is using rather than what was last asked of it. A
// setting that is only read and never written back is a setting Home
// Assistant cannot see: it sets the bass shelf and the slider keeps the
// value it had, and nothing says the two disagree.
//
// The values come from the ported stack's own settings, which are what the
// speaker and the microphone are actually running with.
func (s *ControlServer) SetAudioSettings(set framework.Settings, spk *speaker.Player) {
	if s.registry == nil {
		return
	}
	// The shelves and the stereo come from the player, not from the
	// settings: the player is what is in force. The settings hold what was
	// asked for, and the player settles it — a board that cannot do stereo
	// takes it off, and the shelves live only in the player.
	var clearer, louder bool
	var tone asp.Tone
	var stereo bool
	if spk != nil {
		clearer, louder = spk.ClearerSpeech(), spk.LouderTop()
		tone = spk.Tone()
		stereo = spk.Stereo()
	}
	for _, entity := range s.registry.All() {
		switch entity.ObjectID {
		case "speaker_tuning":
			on := set.Speaker.ASP
			if spk != nil {
				on = spk.ASP()
			}
			s.registry.SetState(entity.Key, esphome.EntityValue{
				Kind: esphome.KindSwitch, Bool: on,
			})
		case "speaker_gain_db":
			gain := set.Speaker.GainDB
			if spk != nil {
				gain = spk.GainDB()
			}
			s.registry.SetState(entity.Key, esphome.EntityValue{
				Kind: esphome.KindNumber, Number: float64(gain),
			})
		case "speaker_stereo":
			s.registry.SetState(entity.Key, esphome.EntityValue{
				Kind: esphome.KindSwitch, Bool: stereo,
			})
		case "speaker_bass_db":
			s.registry.SetState(entity.Key, esphome.EntityValue{
				Kind: esphome.KindNumber, Number: tone.Bass,
			})
		case "speaker_treble_db":
			s.registry.SetState(entity.Key, esphome.EntityValue{
				Kind: esphome.KindNumber, Number: tone.Treble,
			})
		case "speaker_clearer_speech":
			s.registry.SetState(entity.Key, esphome.EntityValue{
				Kind: esphome.KindSwitch, Bool: clearer,
			})
		case "speaker_louder_top":
			s.registry.SetState(entity.Key, esphome.EntityValue{
				Kind: esphome.KindSwitch, Bool: louder,
			})
		case "speaker_louder_speech":
			s.registry.SetState(entity.Key, esphome.EntityValue{
				Kind: esphome.KindSwitch, Bool: louderSpeechOn.Load(),
			})
		case "mic_cancel":
			s.registry.SetState(entity.Key, esphome.EntityValue{
				Kind: esphome.KindSwitch, Bool: set.Microphone.Cancel,
			})
		case "mic_leveling":
			s.registry.SetState(entity.Key, esphome.EntityValue{
				Kind: esphome.KindSwitch, Bool: set.Microphone.Leveling,
			})
		case "mic_denoise":
			s.registry.SetState(entity.Key, esphome.EntityValue{
				Kind: esphome.KindSwitch, Bool: set.Microphone.Denoise,
			})
		case "mic_sensitivity_db":
			s.registry.SetState(entity.Key, esphome.EntityValue{
				Kind: esphome.KindNumber, Number: float64(set.Microphone.Sensitivity),
			})
		}
	}
}

// applyEntities changes the entities to match the state of the panel.
//
// Two of them carry what the panel is doing. The display mode is a
// select, so it shows the mode the panel is in rather than the one it
// was told to be in. The state as a whole goes into a sensor, because
// the protocol has no entity for "the state of a panel" and a sensor is
// the only thing that can hold a value that is read rather than
// written.
func (s *ControlServer) applyEntities(state ControlState) {
	if s.registry == nil {
		return
	}
	for _, entity := range s.registry.All() {
		switch entity.ObjectID {
		case "drawer_open":
			s.registry.SetState(entity.Key, esphome.EntityValue{
				Kind: esphome.KindBinarySensor,
				Bool: state.SidePanelOpen,
			})
		case "keyboard_visible":
			s.registry.SetState(entity.Key, esphome.EntityValue{
				Kind: esphome.KindBinarySensor,
				Bool: state.KeyboardVisible,
			})
		case "last_activity":
			// The time is a piece of text, because the protocol's
			// sensor carries a number and a moment in time is not one.
			s.registry.SetState(entity.Key, esphome.EntityValue{
				Kind: esphome.KindTextSensor,
				Text: state.LastActivity,
			})
		case "version":
			s.registry.SetState(entity.Key, esphome.EntityValue{
				Kind: esphome.KindTextSensor,
				Text: state.Version,
			})
		case "display_mode":
			s.registry.SetState(entity.Key, esphome.EntityValue{
				Kind: esphome.KindSelect,
				Text: state.DisplayMode,
			})
		case "screen":
			s.registry.SetState(entity.Key, esphome.EntityValue{
				Kind: esphome.KindSwitch,
				Bool: !state.DisplayIsOff,
			})
		case "view":
			// The panel can be showing an address, which is not one of
			// the options the select has. The select cannot say a value
			// that is not one of its options, so it keeps the last one
			// it had rather than saying something that is not true.
			if state.Mode == "dashboard" || state.Mode == "slideshow" {
				s.registry.SetState(entity.Key, esphome.EntityValue{
					Kind: esphome.KindSelect,
					Text: state.Mode,
				})
			}
		case "brightness":
			// The ceiling, not the level the screen is at.
			//
			// With the automatic brightness on, the screen is dimmed to
			// the room's light, so the level the driver is at is not
			// what the person set. Publishing that would mean Home
			// Assistant sets sixty and sees twelve back.
			brightness := state.Brightness
			if s.brightnessCeiling > 0 {
				brightness = s.brightnessCeiling
			}
			s.registry.SetState(entity.Key, esphome.EntityValue{
				Kind:   esphome.KindNumber,
				Number: float64(brightness),
			})
		case "idle_slideshow_minutes":
			s.registry.SetState(entity.Key, esphome.EntityValue{Kind: esphome.KindNumber, Number: float64(state.IdleToSlideshowMinutes)})
		case "slideshow_off_minutes":
			s.registry.SetState(entity.Key, esphome.EntityValue{Kind: esphome.KindNumber, Number: float64(state.SlideshowToOffMinutes)})
		case "touch_keyboard_enabled":
			s.registry.SetState(entity.Key, esphome.EntityValue{Kind: esphome.KindSwitch, Bool: state.TouchKeyboardEnabled})
		case "music_enabled":
			s.registry.SetState(entity.Key, esphome.EntityValue{Kind: esphome.KindSwitch, Bool: state.MusicEnabled})
		case "music_unpaired":
			s.registry.SetState(entity.Key, esphome.EntityValue{Kind: esphome.KindSwitch, Bool: state.MusicUnpaired})
		}
	}
}

// SendCommand asks the panel to do something.
//
// The panel is what does it: the device program cannot turn the
// screen off or open a dashboard, and the panel knows how. The command
// goes to every panel that is connected, and there is one.
func (s *ControlServer) SendCommand(command ControlCommand) {
	s.log.Debug("a command is sent to the panels", "action", command.Action)
	s.connections.Range(func(key, _ any) bool {
		conn, ok := key.(net.Conn)
		if !ok {
			return true
		}
		if err := s.writeCommand(conn, command); err != nil {
			s.log.Debug("the command could not be sent", "error", err)
		}
		return true
	})
}

// writeCommand asks one panel to do something.
func (s *ControlServer) writeCommand(conn net.Conn, command ControlCommand) error {
	payload, err := json.Marshal(command)
	if err != nil {
		return err
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	_, err = conn.Write(append(payload, '\n'))
	return err
}

// SendVolume tells the panels what the level is.
//
// The device program owns the level, and the panel shows it. So when the
// level changes — from the physical buttons, or from Home Assistant — the
// panels are told, and the slider follows.
//
// The panel's own change is not sent back: it is already there, and the
// two telling each other about the same change for ever is what the
// follower's settling is there to stop.
func (s *ControlServer) SendVolume(level int) {
	s.connections.Range(func(key, _ any) bool {
		conn, ok := key.(net.Conn)
		if !ok {
			return true
		}
		if err := s.writeMessage(conn, ControlMessage{Action: "volume", Volume: level}); err != nil {
			s.log.Debug("the volume could not be sent to the panel", "error", err)
		}
		return true
	})
}

// Close stops serving the socket.
func (s *ControlServer) Close() {
	s.mu.Lock()
	listener := s.listener
	s.listener = nil
	s.mu.Unlock()
	if listener != nil {
		listener.Close()
	}
	if s.socket != "" {
		os.Remove(s.socket)
	}
}

// readMessage reads one message from the panel.
//
// The messages are newline separated JSON. A line that is not JSON is
// skipped rather than ending the connection: a panel that is out of
// step should not take the link down.
func readMessage(reader *bufio.Reader) (ControlMessage, error) {
	for {
		line, err := reader.ReadBytes('\n')
		if err != nil {
			return ControlMessage{}, err
		}
		if len(line) == 0 {
			continue
		}
		var message ControlMessage
		if err := json.Unmarshal(line, &message); err != nil {
			continue
		}
		return message, nil
	}
}
