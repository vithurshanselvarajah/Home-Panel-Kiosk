package esphome

import (
	"errors"
)

var (
	ErrNoClient = errors.New("esphome: home assistant is not connected")

	ErrTurnRunning = errors.New("esphome: a turn is already running")
)

type VoiceHooks struct {
	OnTurn func(phrase string)

	OnEnd func(transcript string, failed bool)

	OnThinking func(thinking bool)

	OnSpeaking func(speaking bool)

	OnEvent func(event VoiceAssistantEvent)

	OnAnnounce func(mediaID, text string)

	OnAudio func(pcm []byte, final bool)

	OnFlush func()

	OnPlayedOut func()

	OnReplyEnd func()

	OnMicAudio func(pcm []byte)

	OnUtteranceEnd func()
}

func (v *Voice) SetVoiceHooks(hooks VoiceHooks) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.hooks = hooks
}

func (v *Voice) voiceHooks() VoiceHooks {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.hooks
}

func (s *Server) StartTurn(phrase string) error {
	client := s.currentClient()
	if client == nil {
		return ErrNoClient
	}
	return s.startTurnOn(client, phrase)
}

func (s *Server) startTurnOn(client *Client, phrase string) error {
	voice := s.Voice()
	if voice == nil {
		return errors.New("esphome: the device is not a voice assistant")
	}
	if voice.turnRunning() {

		voice.cutOff()
	}

	voice.startTurn(phrase)

	conversationID := voice.conversation()
	if err := s.sendVoiceRequest(client, conversationID, phrase); err != nil {
		voice.endTurn()
		return err
	}

	s.log.Info("a turn is starting", "phrase", phrase, "conversation", conversationID)
	return nil
}

func (s *Server) SendVoiceAudio(pcm []byte, end bool) error {
	client := s.currentClient()
	if client == nil {
		return ErrNoClient
	}
	return s.sendVoiceAudio(client, pcm, end)
}

func (s *Server) SendVoiceEnd() error {
	client := s.currentClient()
	if client == nil {
		return ErrNoClient
	}
	return s.sendVoiceAudio(client, nil, true)
}

func (s *Server) StartStreaming() {}

func (s *Server) StopStreaming() {}

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
