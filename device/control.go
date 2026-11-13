package main

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

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
	Setting         string `json:"setting,omitempty"`
	CameraRequestID string `json:"cameraRequestId,omitempty"`
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
	StartupVolume          int    `json:"startupVolume"`
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
	// log is where the messages go.
	log *slog.Logger
	// onCommand is called when the panel asks for something.
	onCommand func(ControlCommand)
	// sendspinPairingToken reads the pairing token from the device's Sendspin store.
	sendspinPairingToken func() (string, error)
	// onMute is called when the panel asks for the microphone to be
	// muted.
	//
	// The mute is the microphone's: a person who mutes the panel wants
	// the room to stop hearing them, and they still want to hear the
	// music.
	onMute func(muted bool)
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
	writeMu        sync.Mutex
	cameraMu       sync.Mutex
	cameraRequests map[string]chan cameraResult
	cameraSequence atomic.Uint64
}

type cameraResult struct {
	data []byte
	err  error
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
	case "camera_frame":
		s.cameraMu.Lock()
		result := s.cameraRequests[message.CameraRequestID]
		delete(s.cameraRequests, message.CameraRequestID)
		s.cameraMu.Unlock()
		if result != nil {
			var data []byte
			var err error
			if len(message.CameraData) > 6*1024*1024 {
				err = fmt.Errorf("camera image exceeds 4 MiB")
			} else {
				data, err = base64.StdEncoding.DecodeString(message.CameraData)
			}
			if len(data) > 4*1024*1024 {
				err = fmt.Errorf("camera image exceeds 4 MiB")
			}
			if message.CameraError != "" {
				err = fmt.Errorf("%s", message.CameraError)
			}
			result <- cameraResult{data: data, err: err}
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
			handler(message.Command)
		}
	case "mute":
		// The panel mutes the microphone.
		//
		// The mute is the microphone's, and the device is the one that
		// owns the microphone, so the panel asks for it rather than
		// doing it itself.
		s.mu.Lock()
		handler := s.onMute
		s.mu.Unlock()
		if handler != nil {
			handler(message.Muted)
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
	Muted           bool   `json:"muted,omitempty"`
	CameraRequestID string `json:"cameraRequestId,omitempty"`
	CameraData      string `json:"cameraData,omitempty"`
	CameraError     string `json:"cameraError,omitempty"`
	RequestID       string `json:"requestId,omitempty"`
	SendspinToken   string `json:"sendspinToken,omitempty"`
	Error           string `json:"error,omitempty"`
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

// SetMuteHandler sets what is called when the panel asks for the
// microphone to be muted.
func (s *ControlServer) SetMuteHandler(fn func(muted bool)) {
	s.mu.Lock()
	s.onMute = fn
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

func (s *ControlServer) SetState(state ControlState) {
	s.mu.Lock()
	changed := s.state != state
	volumeChanged := s.state.Volume == nil != (state.Volume == nil) ||
		(state.Volume != nil && s.state.Volume != nil && *s.state.Volume != *state.Volume)
	s.state = state
	follower := s.onVolume
	s.mu.Unlock()
	if !changed {
		return
	}
	s.applyEntities(state)

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

// SetVoiceSettings puts the voice settings into the entities.
//
// The voice settings are the device's own rather than the panel's, so
// they are not in the panel's state. They are pushed into the entities
// so Home Assistant can read them, and so a change made in Home
// Assistant is not undone by the next state the panel sends.
func (s *ControlServer) SetVoiceSettings(threshold float64, wakeEnabled bool, duckDB float64, muted bool) {
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
		case "muted":
			s.registry.SetState(entity.Key, esphome.EntityValue{
				Kind: esphome.KindSwitch, Bool: muted,
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
			s.registry.SetState(entity.Key, esphome.EntityValue{
				Kind:   esphome.KindNumber,
				Number: float64(state.Brightness),
			})
		case "idle_slideshow_minutes":
			s.registry.SetState(entity.Key, esphome.EntityValue{Kind: esphome.KindNumber, Number: float64(state.IdleToSlideshowMinutes)})
		case "slideshow_off_minutes":
			s.registry.SetState(entity.Key, esphome.EntityValue{Kind: esphome.KindNumber, Number: float64(state.SlideshowToOffMinutes)})
		case "touch_keyboard_enabled":
			s.registry.SetState(entity.Key, esphome.EntityValue{Kind: esphome.KindSwitch, Bool: state.TouchKeyboardEnabled})
		case "startup_volume":
			s.registry.SetState(entity.Key, esphome.EntityValue{Kind: esphome.KindNumber, Number: float64(state.StartupVolume)})
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

// CaptureCamera asks the panel for one JPEG from the host's default camera.
func (s *ControlServer) CaptureCamera(ctx context.Context) ([]byte, error) {
	id := strconv.FormatUint(s.cameraSequence.Add(1), 10)
	result := make(chan cameraResult, 1)
	s.cameraMu.Lock()
	if s.cameraRequests == nil {
		s.cameraRequests = make(map[string]chan cameraResult)
	}
	s.cameraRequests[id] = result
	s.cameraMu.Unlock()
	s.SendCommand(ControlCommand{Action: "camera_capture", CameraRequestID: id})
	select {
	case response := <-result:
		return response.data, response.err
	case <-ctx.Done():
		s.cameraMu.Lock()
		delete(s.cameraRequests, id)
		s.cameraMu.Unlock()
		return nil, ctx.Err()
	case <-time.After(10 * time.Second):
		s.cameraMu.Lock()
		delete(s.cameraRequests, id)
		s.cameraMu.Unlock()
		return nil, fmt.Errorf("camera capture timed out")
	}
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
