package esphome

import (
	"google.golang.org/protobuf/proto"
)

// The voice messages the server handles.
//
// The numbers are the ones the upstream protocol assigns. They are part
// of the wire format and do not change.
const (
	// TypeSubscribeVoiceAssistantRequest is home assistant saying it
	// wants the voice events. It has no reply.
	TypeSubscribeVoiceAssistantRequest uint32 = 89
	// TypeVoiceAssistantRequest is the device asking for a turn.
	TypeVoiceAssistantRequest uint32 = 90
	// TypeVoiceAssistantResponse is home assistant's acknowledgement.
	TypeVoiceAssistantResponse uint32 = 91
	// TypeVoiceAssistantEventResponse is home assistant's progress.
	TypeVoiceAssistantEventResponse uint32 = 92
	// TypeVoiceAssistantAnnounceRequest is home assistant asking the
	// device to speak.
	TypeVoiceAssistantAnnounceRequest uint32 = 103
	// TypeVoiceAssistantAnnounceFinished is the device saying it has
	// finished speaking.
	TypeVoiceAssistantAnnounceFinished uint32 = 104
	// TypeVoiceAssistantConfigurationRequest is home assistant asking
	// what wake words the device has.
	TypeVoiceAssistantConfigurationRequest uint32 = 105
	// TypeVoiceAssistantAudio is the audio, in both directions.
	TypeVoiceAssistantAudio uint32 = 106
	// TypeVoiceAssistantConfigurationResponse is the answer.
	TypeVoiceAssistantConfigurationResponse uint32 = 107
	// TypeVoiceAssistantSetConfiguration is home assistant switching the
	// wake word on or off.
	TypeVoiceAssistantSetConfiguration uint32 = 108
)

// handleVoiceMessage handles the messages of the voice assistant.
//
// The messages that start a turn come from the device, so they are sent
// rather than received. The ones here are the ones home assistant sends.
//
// It reports whether the message was handled, so the caller knows whether
// to pass it on to anything else.
func (s *Server) handleVoiceMessage(client *client, messageType uint32, payload []byte) (bool, error) {
	switch messageType {
	case TypeSubscribeVoiceAssistantRequest:
		// Home assistant says it wants the voice events. There is no
		// reply: the request is one way, and answering it would be a
		// message home assistant does not expect.
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
		// Home assistant asks what wake words the panel has. The answer
		// is what its picker shows.
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
		// The reply audio. It arrives as a stream of chunks, and the
		// last one has the end flag set.
		audio := &VoiceAssistantAudio{}
		if err := proto.Unmarshal(payload, audio); err != nil {
			return true, err
		}
		s.pushReplyAudio(audio)
		return true, nil

	case TypeVoiceAssistantResponse:
		// Home assistant's acknowledgement of the device's request.
		//
		// The port in it is only meaningful for the UDP audio path that
		// real ESP32 firmware uses. This device uses the same connection
		// for the audio, so the port is not used and the message is
		// only a note that home assistant accepted the turn.
		response := &VoiceAssistantResponse{}
		if err := proto.Unmarshal(payload, response); err != nil {
			return true, err
		}
		s.log.Debug("home assistant accepted the turn", "error", response.GetError())
		return true, nil
	}

	return false, nil
}

// pushReplyAudio hands the reply audio to whoever is playing it.
//
// The audio arrives in chunks, and the last chunk of a reply has the end
// flag set. The chunks are handed on as they arrive so the reply starts
// playing before it has all arrived.
func (s *Server) pushReplyAudio(audio *VoiceAssistantAudio) {
	data := audio.GetData()
	if len(data) == 0 && !audio.GetEnd() {
		return
	}
	if s.onReplyAudio != nil {
		s.onReplyAudio(data, audio.GetEnd())
	}
}

// sendVoiceRequest starts a turn.
//
// The request carries the phrase that fired the wake word. It must not
// be empty for a wake word turn: home assistant's setup dialog reads it
// and refuses the run when it is missing.
//
// The flags are zero because the wake word has already fired. Home
// assistant would otherwise run its own wake word detection and wait for
// a second one.
func (s *Server) sendVoiceRequest(client *client, conversationID, phrase string) error {
	return client.write(TypeVoiceAssistantRequest, &VoiceAssistantRequest{
		Start:          true,
		ConversationId: conversationID,
		Flags:          0,
		WakeWordPhrase: phrase,
	})
}

// sendVoiceAudio sends a chunk of microphone audio.
//
// The audio is signed 16-bit little-endian mono at 16 kHz, which is what
// home assistant expects.
func (s *Server) sendVoiceAudio(client *client, pcm []byte, end bool) error {
	return client.write(TypeVoiceAssistantAudio, &VoiceAssistantAudio{
		Data: pcm,
		End:  end,
	})
}

// sendAnnounceFinished says the announcement has finished playing.
//
// It tells home assistant that the device is free again, which is what
// lets the next thing happen without waiting for a timeout.
func (s *Server) sendAnnounceFinished(client *client) error {
	return client.write(TypeVoiceAssistantAnnounceFinished, &VoiceAssistantAnnounceFinished{})
}
