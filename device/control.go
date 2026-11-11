package main

import (
	"bufio"
	"encoding/json"
	"fmt"
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

// ControlCommand is what the panel asks the device program to do.
//
// The names are the ones the MQTT command contract used, so the panel's
// own code does not change when the transport does.
type ControlCommand struct {
	// Action is what to do.
	Action string `json:"action"`
	// Value is the value, for the commands that take one.
	Value any `json:"value,omitempty"`
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
	Volume int `json:"volume"`
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
	// mu guards the state and the listener.
	mu sync.Mutex
	// listener is the socket, when it is listening.
	listener net.Listener
	// state is what the panel is told.
	state ControlState
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
}

// NewControlServer makes the control server.
func NewControlServer(options ControlOptions) *ControlServer {
	if options.Log == nil {
		options.Log = slog.Default()
	}
	return &ControlServer{
		socket:    options.Socket,
		registry:  options.Registry,
		log:       options.Log,
		onCommand: options.OnCommand,
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
			return
		}
		go s.serve(conn)
	}
}

// serve handles one panel.
func (s *ControlServer) serve(conn net.Conn) {
	defer conn.Close()
	s.connections.Store(conn, struct{}{})
	defer s.connections.Delete(conn)

	// The panel is told the state as soon as it arrives, so it does not
	// have to wait for something to change.
	s.sendState(conn)

	// The reader is made once and kept: a reader made per command
	// would throw away whatever it had read ahead.
	reader := bufio.NewReader(conn)
	for {
		command, err := readCommand(reader)
		if err != nil {
			return
		}
		s.handle(command)
	}
}

// handle applies a command from the panel.
func (s *ControlServer) handle(command ControlCommand) {
	s.log.Debug("the panel asked for something", "action", command.Action)
	if s.onCommand != nil {
		s.onCommand(command)
	}
}

// SetState records the state of the panel and tells every panel.
//
// This is how the panel's own state reaches Home Assistant: the
// entities are changed here, and the change is pushed.
func (s *ControlServer) SetState(state ControlState) {
	s.mu.Lock()
	changed := s.state != state
	s.state = state
	s.mu.Unlock()
	if !changed {
		return
	}
	s.applyEntities(state)
	s.connections.Range(func(key, _ any) bool {
		conn, ok := key.(net.Conn)
		if !ok {
			return true
		}
		if err := s.writeState(conn); err != nil {
			s.log.Debug("the state could not be sent", "error", err)
		}
		return true
	})
}

// State returns the state of the panel.
func (s *ControlServer) State() ControlState {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state
}

// applyEntities changes the entities to match the state of the panel.
//
// The entity that carries the state is a sensor whose state is the
// state as a whole, because the protocol has no entity for "the state
// of a panel" and a sensor is the only thing that can hold a value
// that is read rather than written.
func (s *ControlServer) applyEntities(state ControlState) {
	if s.registry == nil {
		return
	}
	for _, entity := range s.registry.All() {
		if entity.ObjectID != controlStateObjectID {
			continue
		}
		encoded, err := json.Marshal(state)
		if err != nil {
			continue
		}
		s.registry.SetState(entity.Key, esphome.EntityValue{
			Kind:  esphome.KindSensor,
			State: string(encoded),
		})
	}
}

// sendState tells one panel what the state is.
func (s *ControlServer) sendState(conn net.Conn) error {
	return s.writeState(conn)
}

// writeState tells one panel what the state is.
func (s *ControlServer) writeState(conn net.Conn) error {
	state := s.State()
	payload, err := json.Marshal(state)
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

// controlStateObjectID is the entity that carries the state of the
// panel.
//
// It is a sensor rather than something that can be written, because
// the state is the panel's to report and not the panel's to be told.
const controlStateObjectID = "panel_state"

// readCommand reads one command from the panel.
//
// The commands are newline separated JSON. A line that is not JSON is
// skipped rather than ending the connection: a panel that is out of step
// should not take the link down.
func readCommand(reader *bufio.Reader) (ControlCommand, error) {
	for {
		line, err := reader.ReadBytes('\n')
		if err != nil {
			return ControlCommand{}, err
		}
		if len(line) == 0 {
			continue
		}
		var command ControlCommand
		if err := json.Unmarshal(line, &command); err != nil {
			continue
		}
		return command, nil
	}
}
