package esphome

import (
	"testing"
)

func TestTheStreamedReplySequence(t *testing.T) {
	voice := NewVoice(VoiceOptions{})

	speaking := false
	voice.SetVoiceHooks(VoiceHooks{OnSpeaking: func(v bool) { speaking = v }})

	voice.startTurn("hey jarvis")

	voice.handleVoiceEvent(&VoiceAssistantEventResponse{
		EventType: VoiceAssistantEvent_VOICE_ASSISTANT_TTS_START,
		Data:      []*VoiceAssistantEventData{{Name: "tts_input", Value: "It is half past three"}},
	})
	if voice.Spoken() != "It is half past three" {
		t.Errorf("the answer's words were lost: %q", voice.Spoken())
	}
	if !speaking {
		t.Error("the panel is not speaking when the reply started")
	}

	voice.handleVoiceEvent(&VoiceAssistantEventResponse{
		EventType: VoiceAssistantEvent_VOICE_ASSISTANT_TTS_END,
	})
	if !speaking {
		t.Error("the panel stopped speaking when the answer was written")
	}

	voice.handleVoiceEvent(&VoiceAssistantEventResponse{
		EventType: VoiceAssistantEvent_VOICE_ASSISTANT_RUN_END,
	})
	if !speaking {
		t.Error("the run's end cut the reply off")
	}

	voice.handleVoiceEvent(&VoiceAssistantEventResponse{
		EventType: VoiceAssistantEvent_VOICE_ASSISTANT_TTS_STREAM_START,
	})
	voice.handleReplyAudio([]byte{0, 0, 0, 0}, false)
	if !speaking {
		t.Error("the panel is not speaking while the reply is arriving")
	}

	voice.handleVoiceEvent(&VoiceAssistantEventResponse{
		EventType: VoiceAssistantEvent_VOICE_ASSISTANT_TTS_STREAM_END,
	})
	voice.ReplyPlayedOut()
	if speaking {
		t.Error("the panel is still speaking after the reply was played out")
	}
}

func TestTheAnswerIsCaptured(t *testing.T) {
	voice := NewVoice(VoiceOptions{})

	voice.startTurn("hey jarvis")
	voice.handleVoiceEvent(&VoiceAssistantEventResponse{
		EventType: VoiceAssistantEvent_VOICE_ASSISTANT_INTENT_END,
		Data: []*VoiceAssistantEventData{
			{Name: "conversation_id", Value: "1"},
			{Name: "speech", Value: "It is half past three in the afternoon"},
		},
	})

	if voice.Spoken() != "It is half past three in the afternoon" {
		t.Errorf("the answer's words were not taken: %q", voice.Spoken())
	}
}

func TestTheAnswerIsKeptWhenTheTextToSpeechStarts(t *testing.T) {
	voice := NewVoice(VoiceOptions{})

	voice.startTurn("hey jarvis")
	voice.handleVoiceEvent(&VoiceAssistantEventResponse{
		EventType: VoiceAssistantEvent_VOICE_ASSISTANT_INTENT_END,
		Data:      []*VoiceAssistantEventData{{Name: "speech", Value: "It is half past three"}},
	})
	voice.handleVoiceEvent(&VoiceAssistantEventResponse{
		EventType: VoiceAssistantEvent_VOICE_ASSISTANT_TTS_START,
		Data:      []*VoiceAssistantEventData{{Name: "tts_input", Value: ""}},
	})

	if voice.Spoken() != "It is half past three" {
		t.Errorf("the answer's words were lost: %q", voice.Spoken())
	}
}
