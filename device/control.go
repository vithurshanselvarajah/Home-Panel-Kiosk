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

type ControlCommand struct {
	Action string `json:"action"`

	Value any `json:"value,omitempty"`

	Setting string `json:"setting,omitempty"`

	Mode string `json:"mode,omitempty"`

	URL string `json:"url,omitempty"`

	Title string `json:"title,omitempty"`

	Message string `json:"message,omitempty"`

	Duration int `json:"duration,omitempty"`

	Priority string `json:"priority,omitempty"`
}

type ControlStateUpdate struct {
	Action string `json:"action"`

	State ControlState `json:"value"`
}

type ControlState struct {
	Mode string `json:"mode"`

	DisplayIsOff bool `json:"displayIsOff"`

	KeyboardVisible bool `json:"keyboardVisible"`

	SidePanelOpen bool `json:"sidePanelOpen"`

	DisplayMode string `json:"displayMode"`

	Brightness int `json:"brightness"`

	Volume *int `json:"volume,omitempty"`

	LastActivity string `json:"lastActivity"`

	Version                string `json:"version"`
	IdleToSlideshowMinutes int    `json:"idleToSlideshowMinutes"`
	SlideshowToOffMinutes  int    `json:"slideshowToOffMinutes"`
	TouchKeyboardEnabled   bool   `json:"touchKeyboardEnabled"`
	MusicEnabled           bool   `json:"musicEnabled"`
	MusicUnpaired          bool   `json:"musicUnpaired"`
}

type ControlServer struct {
	socket string

	registry *esphome.Registry

	brightnessCeiling int

	log *slog.Logger

	onCommand func(ControlCommand)

	sendspinPairingToken func() (string, error)
	setCameraPassword    func(string)
	onCameraConnected    func(string)
	cameraPassword       string

	onVolume func(level int)

	onBrightness func(percent int)

	onTransport func(command TransportCommand)

	onListen func(phrase string)

	mu sync.Mutex

	listener net.Listener

	state ControlState

	connected bool

	connections sync.Map

	writeMu sync.Mutex
}

type ControlOptions struct {
	Socket string

	Registry *esphome.Registry

	Log *slog.Logger

	OnCommand func(ControlCommand)

	SendspinPairingToken func() (string, error)

	SetCameraPassword func(string)
	OnCameraConnected func(string)
}

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

func (s *ControlServer) serve(conn net.Conn) {
	defer conn.Close()
	s.log.Debug("a panel connected")
	s.connections.Store(conn, struct{}{})
	defer s.connections.Delete(conn)

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

	reader := bufio.NewReader(conn)
	for {
		message, err := readMessage(reader)
		if err != nil {
			s.log.Debug("the panel's connection ended", "error", err)
			return
		}

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
			err = fmt.Errorf("sendspin pairing is unavailable")
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

			s.log.Info("the panel asked for the transport", "action", message.Command.Action,
				"source", message.Command.Source)
			handler(message.Command)
		}
	case "mute":
		// A mute from the panel is ignored. The mute is the hardware
		// latch's, and the device program reads the latch rather than
		// being told what it is, so the panel cannot put it out of step
		// with the button on the unit.

	case "volume":

		s.mu.Lock()
		handler := s.onVolume
		s.mu.Unlock()
		if handler != nil {
			handler(message.Volume)
		}
	case "listen":

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

type ControlMessage struct {
	Action string `json:"action"`

	State ControlState `json:"value"`

	Command TransportCommand `json:"command"`

	Phrase string `json:"phrase,omitempty"`

	Connected bool `json:"connected"`

	Muted bool `json:"muted,omitempty"`

	Volume         int    `json:"volume,omitempty"`
	CameraPassword string `json:"cameraPassword,omitempty"`
	RequestID      string `json:"requestId,omitempty"`
	SendspinToken  string `json:"sendspinToken,omitempty"`
	Error          string `json:"error,omitempty"`
}

type TransportCommand struct {
	Action string `json:"action"`

	Source string `json:"source,omitempty"`

	PositionMs int64 `json:"positionMs,omitempty"`
}

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

func (s *ControlServer) SetListenHandler(fn func(phrase string)) {
	s.mu.Lock()
	s.onListen = fn
	s.mu.Unlock()
}

func (s *ControlServer) SetVolumeHandler(fn func(level int)) {
	s.mu.Lock()
	s.onVolume = fn
	s.mu.Unlock()
}

func (s *ControlServer) SetTransportHandler(fn func(command TransportCommand)) {
	s.mu.Lock()
	s.onTransport = fn
	s.mu.Unlock()
}

func (s *ControlServer) SetVolumeFollower(fn func(level int)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onVolume = fn
}

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

		s.applyEntities(state)
		return
	}
	s.applyEntities(state)

	if brightnessChanged && brightnessFollower != nil {
		brightnessFollower(state.Brightness)
	}

	if volumeChanged && state.Volume != nil && *state.Volume > 0 && follower != nil {
		follower(*state.Volume)
	}
}

func (s *ControlServer) State() ControlState {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state
}

func (s *ControlServer) SetBrightnessCeiling(percent int) {
	s.mu.Lock()
	s.brightnessCeiling = percent
	s.mu.Unlock()
}

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

func (s *ControlServer) SetAudioSettings(set framework.Settings, spk *speaker.Player) {
	if s.registry == nil {
		return
	}

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

			if state.Mode == "dashboard" || state.Mode == "slideshow" {
				s.registry.SetState(entity.Key, esphome.EntityValue{
					Kind: esphome.KindSelect,
					Text: state.Mode,
				})
			}
		case "brightness":

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
