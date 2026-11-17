package esphome

import (
	"google.golang.org/protobuf/proto"
)

const (
	TypeSubscribeVoiceAssistantRequest uint32 = 89

	TypeVoiceAssistantRequest uint32 = 90

	TypeVoiceAssistantResponse uint32 = 91

	TypeVoiceAssistantEventResponse uint32 = 92

	TypeVoiceAssistantAnnounceRequest uint32 = 119

	TypeVoiceAssistantAnnounceFinished uint32 = 120

	TypeVoiceAssistantConfigurationRequest uint32 = 121

	TypeVoiceAssistantConfigurationResponse uint32 = 122

	TypeVoiceAssistantSetConfiguration uint32 = 123

	TypeVoiceAssistantAudio uint32 = 106

	TypeVoiceAssistantTimerEventResponse uint32 = 115
)

func (s *Server) handleVoiceMessage(client *Client, messageType uint32, payload []byte) (bool, error) {
	switch messageType {
	case TypeSubscribeVoiceAssistantRequest:

		s.log.Debug("home assistant subscribed to the voice events")
		return true, nil

	case TypeVoiceAssistantEventResponse:
		event := &VoiceAssistantEventResponse{}
		if err := proto.Unmarshal(payload, event); err != nil {
			return true, err
		}
		if s.voice != nil {
			s.voice.handleVoiceEvent(event)
		}
		return true, nil

	case TypeVoiceAssistantAnnounceRequest:
		announce := &VoiceAssistantAnnounceRequest{}
		if err := proto.Unmarshal(payload, announce); err != nil {
			return true, err
		}
		if s.voice != nil {
			s.voice.handleAnnounce(announce)
		}
		return true, nil

	case TypeVoiceAssistantConfigurationRequest:

		response := &VoiceAssistantConfigurationResponse{
			AvailableWakeWords: []*VoiceAssistantWakeWord{},
			ActiveWakeWords:    []string{},
			MaxActiveWakeWords: 1,
		}
		if s.voice != nil {
			response = s.voice.handleConfigurationRequest()
		}
		return true, client.write(TypeVoiceAssistantConfigurationResponse, response)

	case TypeVoiceAssistantSetConfiguration:
		configuration := &VoiceAssistantSetConfiguration{}
		if err := proto.Unmarshal(payload, configuration); err != nil {
			return true, err
		}
		if s.voice != nil {
			if err := s.voice.handleSetConfiguration(configuration); err != nil {
				s.log.Warn("the wake word could not be switched", "error", err)
			}
		}
		return true, nil

	case TypeVoiceAssistantAudio:

		audio := &VoiceAssistantAudio{}
		if err := proto.Unmarshal(payload, audio); err != nil {
			return true, err
		}
		s.pushReplyAudio(audio)
		return true, nil

	case TypeVoiceAssistantResponse:

		response := &VoiceAssistantResponse{}
		if err := proto.Unmarshal(payload, response); err != nil {
			return true, err
		}
		s.log.Debug("home assistant accepted the turn", "error", response.GetError())
		return true, nil
	}

	return false, nil
}

func (s *Server) pushReplyAudio(audio *VoiceAssistantAudio) {
	data := audio.GetData()
	if len(data) == 0 && !audio.GetEnd() {
		return
	}
	s.log.Debug("the reply audio arrived", "bytes", len(data), "end", audio.GetEnd())
	if s.voice != nil {
		s.voice.handleReplyAudio(data, audio.GetEnd())
	}
}

func (s *Server) sendVoiceRequest(client *Client, conversationID, phrase string) error {
	return client.write(TypeVoiceAssistantRequest, &VoiceAssistantRequest{
		Start:          true,
		ConversationId: conversationID,
		Flags:          0,
		WakeWordPhrase: phrase,
	})
}

func (s *Server) sendVoiceAudio(client *Client, pcm []byte, end bool) error {

	if len(pcm) > 0 {
		client.log("the microphone is going to home assistant", "bytes", len(pcm))
	}
	return client.write(TypeVoiceAssistantAudio, &VoiceAssistantAudio{
		Data: pcm,
		End:  end,
	})
}

func (s *Server) sendAnnounceFinished(client *Client) error {
	return client.write(TypeVoiceAssistantAnnounceFinished, &VoiceAssistantAnnounceFinished{})
}
