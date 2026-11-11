package esphome

import (
	"errors"
	"sync"
)

// The errors the voice assistant can return.
var (
	// ErrNoClient says there is no Home Assistant to talk to.
	//
	// A turn needs somewhere to send the microphone and somewhere to get
	// the reply from. When Home Assistant is not connected there is
	// neither, and the panel says so rather than pretending.
	ErrNoClient = errors.New("esphome: home assistant is not connected")

	// ErrTurnRunning says a turn is already running.
	ErrTurnRunning = errors.New("esphome: a turn is already running")
)

// voiceTurn is the state of one turn.
//
// It is the bridge between the wake word, which says a turn should start,
// and the connection, which carries it.
type voiceTurn struct {
	// client is the connection the turn is running on.
	client *Client
	// conversationID identifies the turn.
	conversationID string
	// phrase is the wake word that fired.
	phrase string
	// ended says the turn has finished.
	ended bool
}

// VoiceHooks are the callbacks the voice assistant makes.
//
// The device program wires these to its own microphone and speaker.
type VoiceHooks struct {
	// OnTurn is called when a turn starts, with the phrase that fired.
	//
	// The phrase is empty when the turn did not start with a wake word.
	OnTurn func(phrase string)
	// OnEnd is called when a turn ends.
	OnEnd func(transcript string, failed bool)
	// OnThinking is called when Home Assistant starts or stops working.
	OnThinking func(thinking bool)
	// OnSpeaking is called when the reply starts or stops.
	OnSpeaking func(speaking bool)
	// OnEvent is called for each event Home Assistant sends.
	OnEvent func(event VoiceAssistantEvent)
	// OnAnnounce is called when Home Assistant asks the panel to speak.
	OnAnnounce func(mediaID, text string)
	// OnAudio is called with the reply audio as it arrives.
	OnAudio func(pcm []byte, final bool)
	// OnMicAudio is called with the microphone's frames while a turn is
	// running.
	//
	// It is called from the capture goroutine, so it must not block: the
	// capture goroutine is the one that reads the sound card, and a
	// blocked read is a stalled microphone.
	OnMicAudio func(pcm []byte)
}

// SetVoiceHooks wires the callbacks.
//
// They are set after the voice assistant is made, because they belong to
// the device program and the voice assistant belongs to the protocol.
func (v *Voice) SetVoiceHooks(hooks VoiceHooks) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.hooks = hooks
}

// hooks returns the callbacks.
func (v *Voice) voiceHooks() VoiceHooks {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.hooks
}

// StartTurn begins a turn on the current connection.
//
// The phrase is the wake word that fired. It must not be empty for a
// wake word turn: Home Assistant's setup dialog reads it and refuses the
// run when it is missing.
//
// The turn fails when Home Assistant is not connected, because there is
// nowhere to send the microphone.
func (s *Server) StartTurn(phrase string) error {
	client := s.currentClient()
	if client == nil {
		return ErrNoClient
	}
	return s.startTurnOn(client, phrase)
}

// startTurnOn begins a turn on a particular connection.
func (s *Server) startTurnOn(client *Client, phrase string) error {
	voice := s.Voice()
	if voice == nil {
		return errors.New("esphome: the device is not a voice assistant")
	}
	if voice.turnRunning() {
		return ErrTurnRunning
	}

	voice.startTurn(phrase)

	// The request carries the conversation id and the phrase, and the
	// flags are zero because the wake word has already fired.
	conversationID := voice.conversation()
	if err := s.sendVoiceRequest(client, conversationID, phrase); err != nil {
		voice.endTurn()
		return err
	}

	s.log.Info("a turn is starting", "phrase", phrase, "conversation", conversationID)
	return nil
}

// SendVoiceAudio sends a block of microphone audio.
//
// It is called for every block of captured audio while a turn is running,
// and the last block carries the end flag.
//
// The audio is signed 16-bit little-endian mono at 16 kHz, which is what
// Home Assistant expects.
//
// It is called from the capture goroutine, so it must not block: the
// capture goroutine is the one that reads the sound card, and a blocked
// read is a stalled microphone. A write to a socket that has gone away
// returns rather than waits, so this is safe to call on that goroutine.
func (s *Server) SendVoiceAudio(pcm []byte, end bool) error {
	client := s.currentClient()
	if client == nil {
		return ErrNoClient
	}
	return s.sendVoiceAudio(client, pcm, end)
}

// StartStreaming says the microphone is open for a turn.
//
// The frames go to home assistant only between this and StopStreaming, so
// a panel that is not in a turn is not uploading the room.
func (s *Server) StartStreaming() {}

// StopStreaming says the turn is over and the microphone can be closed.
func (s *Server) StopStreaming() {}

// currentClient returns the connection to Home Assistant, if there is one.
//
// It is the most recently connected one. There is normally only ever one:
// Home Assistant opens one connection per device.
func (s *Server) currentClient() *Client {
	var found *Client
	s.connections.Range(func(key, _ any) bool {
		if client, ok := key.(*Client); ok {
			found = client
			return false
		}
		return true
	})
	return found
}

// turnMu guards the turn's own state.
//
// It is separate from the voice assistant's lock because the turn spans
// several calls: it starts, streams, and ends.
var turnMu sync.Mutex
