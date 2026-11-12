package esphome

import (
	"testing"
)

// TestTheStreamedReplySequence walks the events in the order home
// assistant sends them when the answer is streamed.
//
// The order is the whole point. Home Assistant ends the run when the
// agent has written the answer, and the answer is still being spoken
// after that, so the run's end must not end the reply.
func TestTheStreamedReplySequence(t *testing.T) {
	voice := NewVoice(VoiceOptions{})

	speaking := false
	voice.SetVoiceHooks(VoiceHooks{OnSpeaking: func(v bool) { speaking = v }})

	voice.startTurn("hey jarvis")

	// The answer is written, and its words come with it.
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

	// The answer is sent. The audio has not arrived yet.
	voice.handleVoiceEvent(&VoiceAssistantEventResponse{
		EventType: VoiceAssistantEvent_VOICE_ASSISTANT_TTS_END,
	})
	if !speaking {
		t.Error("the panel stopped speaking when the answer was written")
	}

	// The run ends. The answer is still on its way.
	voice.handleVoiceEvent(&VoiceAssistantEventResponse{
		EventType: VoiceAssistantEvent_VOICE_ASSISTANT_RUN_END,
	})
	if !speaking {
		t.Error("the run's end cut the reply off")
	}

	// The audio starts streaming.
	voice.handleVoiceEvent(&VoiceAssistantEventResponse{
		EventType: VoiceAssistantEvent_VOICE_ASSISTANT_TTS_STREAM_START,
	})
	voice.handleReplyAudio([]byte{0, 0, 0, 0}, false)
	if !speaking {
		t.Error("the panel is not speaking while the reply is arriving")
	}

	// And all of it.
	voice.handleVoiceEvent(&VoiceAssistantEventResponse{
		EventType: VoiceAssistantEvent_VOICE_ASSISTANT_TTS_STREAM_END,
	})
	voice.ReplyPlayedOut()
	if speaking {
		t.Error("the panel is still speaking after the reply was played out")
	}
}

// TestTheAnswerIsCaptured checks that the answer's words are taken.
//
// Home Assistant says what the answer is when the intent has matched,
// which is before the audio starts. The start of the text-to-speech
// carries the words too, but only when the answer is being streamed, in
// which case they arrive in pieces — so the intent's end is the one
// that always comes.
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

// TestTheAnswerIsKeptWhenTheTextToSpeechStarts checks that the answer
// survives the start of the text-to-speech.
//
// The text-to-speech carries the words too when the answer is being
// streamed, but in pieces, so it must not throw away what the intent
// already said.
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
