package esphome

import (
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"
)

func TestVoiceFlagsSayWhatTheDeviceCanDo(t *testing.T) {
	server := NewServer(ServerOptions{Name: "panel", MAC: "AA:BB:CC:DD:EE:FF"})

	// Without a wake word the panel cannot start a conversation by
	// itself, so it must not carry the flag: a panel that carries it
	// appears in the picker and answers every question with silence.
	flags := server.voiceAssistantFlags()
	if flags&startConversationFlag != 0 {
		t.Error("the panel claims it can start conversations without a wake word")
	}
	if flags&voiceAssistantFlag == 0 {
		t.Error("the panel does not claim to be a voice assistant")
	}
	if flags&announceFlag == 0 {
		t.Error("the panel does not claim it can be asked to speak")
	}
}

func TestVoiceFlagsIncludeStartConversationWithAWakeWord(t *testing.T) {
	server := NewServer(ServerOptions{Name: "panel", MAC: "AA:BB:CC:DD:EE:FF"})
	voice := NewVoice(VoiceOptions{})
	voice.SetWakeWord("hey_jarvis", true, true)
	server.SetVoice(voice)

	if server.voiceAssistantFlags()&startConversationFlag == 0 {
		t.Error("the panel does not claim it can start conversations with a wake word")
	}
}

func TestVoiceFlagsAreOffWhenTheWakeWordIsSwitchedOff(t *testing.T) {
	server := NewServer(ServerOptions{Name: "panel", MAC: "AA:BB:CC:DD:EE:FF"})
	voice := NewVoice(VoiceOptions{})
	voice.SetWakeWord("hey_jarvis", true, false)
	server.SetVoice(voice)

	if server.voiceAssistantFlags()&startConversationFlag != 0 {
		t.Error("the panel claims it can start conversations with the wake word switched off")
	}
}

func TestTurnStartsWithThePhrase(t *testing.T) {
	voice := NewVoice(VoiceOptions{})

	var started string
	voice.SetVoiceHooks(VoiceHooks{OnTurn: func(phrase string) { started = phrase }})

	voice.startTurn("hey jarvis")

	if started != "hey jarvis" {
		t.Errorf("the turn started with %q, want %q", started, "hey jarvis")
	}
	if !voice.turnRunning() {
		t.Error("the turn is not running after it started")
	}
}

func TestOnlyOneTurnRunsAtATime(t *testing.T) {
	voice := NewVoice(VoiceOptions{})

	starts := 0
	voice.SetVoiceHooks(VoiceHooks{OnTurn: func(string) { starts++ }})

	voice.startTurn("hey jarvis")
	voice.startTurn("hey jarvis")

	if starts != 1 {
		t.Errorf("the turn started %d times, want 1", starts)
	}
}

func TestTurnEndsWithTheTranscript(t *testing.T) {
	voice := NewVoice(VoiceOptions{})

	var transcript string
	var failed bool
	voice.SetVoiceHooks(VoiceHooks{OnEnd: func(text string, isFailed bool) {
		transcript = text
		failed = isFailed
	}})

	voice.startTurn("hey jarvis")
	voice.handleVoiceEvent(&VoiceAssistantEventResponse{
		EventType: VoiceAssistantEvent_VOICE_ASSISTANT_STT_END,
		Data:      []*VoiceAssistantEventData{{Name: "text", Value: "what is the weather"}},
	})
	voice.handleVoiceEvent(&VoiceAssistantEventResponse{
		EventType: VoiceAssistantEvent_VOICE_ASSISTANT_RUN_END,
	})

	if transcript != "what is the weather" {
		t.Errorf("the transcript was %q, want %q", transcript, "what is the weather")
	}
	if failed {
		t.Error("the turn ended as a failure")
	}
	if voice.turnRunning() {
		t.Error("the turn is still running after it ended")
	}
}

func TestAnErrorIsAFailedTurn(t *testing.T) {
	voice := NewVoice(VoiceOptions{})

	var failed bool
	voice.SetVoiceHooks(VoiceHooks{OnEnd: func(_ string, isFailed bool) { failed = isFailed }})

	voice.startTurn("hey jarvis")
	voice.handleVoiceEvent(&VoiceAssistantEventResponse{
		EventType: VoiceAssistantEvent_VOICE_ASSISTANT_ERROR,
		Data:      []*VoiceAssistantEventData{{Name: "message", Value: "boom"}},
	})
	voice.handleVoiceEvent(&VoiceAssistantEventResponse{
		EventType: VoiceAssistantEvent_VOICE_ASSISTANT_RUN_END,
	})

	if !failed {
		t.Error("the turn with an error did not end as a failure")
	}
}

func TestThinkingFollowsTheEvents(t *testing.T) {
	voice := NewVoice(VoiceOptions{})

	thinking := false
	voice.SetVoiceHooks(VoiceHooks{OnThinking: func(value bool) { thinking = value }})

	voice.startTurn("hey jarvis")
	voice.handleVoiceEvent(&VoiceAssistantEventResponse{
		EventType: VoiceAssistantEvent_VOICE_ASSISTANT_RUN_START,
	})
	if !thinking {
		t.Error("the panel is not thinking when the run started")
	}

	voice.handleVoiceEvent(&VoiceAssistantEventResponse{
		EventType: VoiceAssistantEvent_VOICE_ASSISTANT_RUN_END,
	})
	if thinking {
		t.Error("the panel is still thinking after the run ended")
	}
}

func TestSpeakingFollowsTheReply(t *testing.T) {
	voice := NewVoice(VoiceOptions{})

	speaking := false
	voice.SetVoiceHooks(VoiceHooks{OnSpeaking: func(value bool) { speaking = value }})

	voice.startTurn("hey jarvis")
	voice.handleVoiceEvent(&VoiceAssistantEventResponse{
		EventType: VoiceAssistantEvent_VOICE_ASSISTANT_TTS_START,
	})
	if !speaking {
		t.Error("the panel is not speaking when the reply started")
	}

	voice.handleVoiceEvent(&VoiceAssistantEventResponse{
		EventType: VoiceAssistantEvent_VOICE_ASSISTANT_TTS_END,
	})
	if speaking {
		t.Error("the panel is still speaking when the reply ended")
	}
}

func TestConfigurationRequestOffersTheWakeWord(t *testing.T) {
	voice := NewVoice(VoiceOptions{})
	voice.SetWakeWord("hey_jarvis", true, true)

	response := voice.handleConfigurationRequest()

	if len(response.GetAvailableWakeWords()) != 1 {
		t.Fatalf("the panel offered %d wake words, want 1", len(response.GetAvailableWakeWords()))
	}
	if got := response.GetAvailableWakeWords()[0].GetId(); got != "hey_jarvis" {
		t.Errorf("the wake word was %q, want %q", got, "hey_jarvis")
	}
	if len(response.GetActiveWakeWords()) != 1 {
		t.Error("the wake word is not active, so the picker would show it as off")
	}
}

func TestConfigurationRequestOffersNothingWithoutAModel(t *testing.T) {
	voice := NewVoice(VoiceOptions{})

	response := voice.handleConfigurationRequest()

	if len(response.GetAvailableWakeWords()) != 0 {
		t.Error("the panel offered a wake word it does not have")
	}
	if len(response.GetActiveWakeWords()) != 0 {
		t.Error("the panel offered an active wake word it does not have")
	}
}

func TestConfigurationRequestIsEmptyWhenTheWakeWordIsOff(t *testing.T) {
	voice := NewVoice(VoiceOptions{})
	voice.SetWakeWord("hey_jarvis", true, false)

	response := voice.handleConfigurationRequest()

	// The model is still offered — home assistant has to be able to
	// switch it back on — but it is not active.
	if len(response.GetAvailableWakeWords()) != 1 {
		t.Error("the panel does not offer the wake word it has")
	}
	if len(response.GetActiveWakeWords()) != 0 {
		t.Error("the wake word is active although it is switched off")
	}
}

func TestSetConfigurationSwitchesTheWakeWordOn(t *testing.T) {
	voice := NewVoice(VoiceOptions{})
	voice.SetWakeWord("hey_jarvis", true, false)

	if err := voice.handleSetConfiguration(&VoiceAssistantSetConfiguration{
		ActiveWakeWords: []string{"hey_jarvis"},
	}); err != nil {
		t.Fatalf("the wake word could not be switched on: %v", err)
	}

	if !voice.canStartConversations() {
		t.Error("the panel cannot start conversations after the wake word was switched on")
	}
}

func TestSetConfigurationSwitchesTheWakeWordOff(t *testing.T) {
	voice := NewVoice(VoiceOptions{})
	voice.SetWakeWord("hey_jarvis", true, true)

	if err := voice.handleSetConfiguration(&VoiceAssistantSetConfiguration{
		ActiveWakeWords: []string{},
	}); err != nil {
		t.Fatalf("the wake word could not be switched off: %v", err)
	}

	if voice.canStartConversations() {
		t.Error("the panel can still start conversations after the wake word was switched off")
	}
}

func TestSetConfigurationRefusesAModelThePanelDoesNotHave(t *testing.T) {
	voice := NewVoice(VoiceOptions{})
	voice.SetWakeWord("hey_jarvis", true, true)

	err := voice.handleSetConfiguration(&VoiceAssistantSetConfiguration{
		ActiveWakeWords: []string{"alexa"},
	})
	if err == nil {
		t.Fatal("the panel accepted a wake word it does not have")
	}

	// It keeps what it had, rather than pretending it switched.
	if !voice.canStartConversations() {
		t.Error("the panel lost its wake word because of a request it could not satisfy")
	}
}

func TestAnnounceIsHandedOn(t *testing.T) {
	voice := NewVoice(VoiceOptions{})

	var mediaID, text string
	voice.SetVoiceHooks(VoiceHooks{OnAnnounce: func(id, message string) {
		mediaID = id
		text = message
	}})

	voice.handleAnnounce(&VoiceAssistantAnnounceRequest{
		MediaId: "http://homeassistant/media.mp3",
		Text:    "the washing is finished",
	})

	if mediaID != "http://homeassistant/media.mp3" {
		t.Errorf("the media id was %q", mediaID)
	}
	if text != "the washing is finished" {
		t.Errorf("the text was %q", text)
	}
}

func TestConversationIDsAreUnique(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 100; i++ {
		id := newConversationID()
		if seen[id] {
			t.Fatalf("the conversation id %q was used twice", id)
		}
		seen[id] = true
	}
}

func TestEndTurnOnATurnThatNeverStartedIsHarmless(t *testing.T) {
	voice := NewVoice(VoiceOptions{})

	ended := 0
	voice.SetVoiceHooks(VoiceHooks{OnEnd: func(string, bool) { ended++ }})

	voice.handleVoiceEvent(&VoiceAssistantEventResponse{
		EventType: VoiceAssistantEvent_VOICE_ASSISTANT_RUN_END,
	})

	if ended != 0 {
		t.Errorf("a turn that never started ended %d times", ended)
	}
}

func TestVoiceAssistantIsSafeToUseFromSeveralGoroutines(t *testing.T) {
	voice := NewVoice(VoiceOptions{})

	done := make(chan struct{})
	for i := 0; i < 4; i++ {
		go func() {
			for j := 0; j < 100; j++ {
				voice.startTurn("hey jarvis")
				voice.handleVoiceEvent(&VoiceAssistantEventResponse{
					EventType: VoiceAssistantEvent_VOICE_ASSISTANT_STT_END,
					Data:      []*VoiceAssistantEventData{{Name: "text", Value: "hello"}},
				})
				voice.handleConfigurationRequest()
				voice.turnRunning()
			}
			done <- struct{}{}
		}()
	}
	for i := 0; i < 4; i++ {
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("the voice assistant did not finish")
		}
	}
}

func TestSetConfigurationTellsTheDevice(t *testing.T) {
	voice := NewVoice(VoiceOptions{})
	voice.SetWakeWord("hey_jarvis", true, true)

	enabled := true
	called := false
	voice.SetWakeWordCallback(func(value bool) {
		enabled = value
		called = true
	})

	if err := voice.handleSetConfiguration(&VoiceAssistantSetConfiguration{
		ActiveWakeWords: []string{},
	}); err != nil {
		t.Fatalf("the wake word could not be switched off: %v", err)
	}

	if !called {
		t.Fatal("the device was not told the wake word was switched")
	}
	if enabled {
		t.Error("the device was told the wake word was switched on when it was off")
	}
}

func TestTheWakeWordCallbackIsNotCalledForAModelThePanelDoesNotHave(t *testing.T) {
	voice := NewVoice(VoiceOptions{})
	voice.SetWakeWord("hey_jarvis", true, true)

	called := false
	voice.SetWakeWordCallback(func(bool) { called = true })

	if err := voice.handleSetConfiguration(&VoiceAssistantSetConfiguration{
		ActiveWakeWords: []string{"alexa"},
	}); err == nil {
		t.Fatal("the panel accepted a wake word it does not have")
	}

	if called {
		t.Error("the device was told about a request the panel could not satisfy")
	}
}

// TestTheVoiceMessageNumbersMatchTheProtocol checks the message numbers.
//
// The numbers are the wire format, and getting one wrong is a silent bug:
// home assistant sends the message, the device does not recognise it, and
// the device sits there waiting for a reply that was never asked for.
//
// The numbers are read out of the upstream protocol file, so they cannot
// drift from it.
func TestTheVoiceMessageNumbersMatchTheProtocol(t *testing.T) {
	upstream, err := os.ReadFile("api.proto.upstream")
	if err != nil {
		t.Skipf("the upstream protocol file could not be read: %v", err)
	}
	text := string(upstream)

	for _, want := range []struct {
		name string
		id   uint32
	}{
		{"SubscribeVoiceAssistantRequest", TypeSubscribeVoiceAssistantRequest},
		{"VoiceAssistantRequest", TypeVoiceAssistantRequest},
		{"VoiceAssistantResponse", TypeVoiceAssistantResponse},
		{"VoiceAssistantEventResponse", TypeVoiceAssistantEventResponse},
		{"VoiceAssistantAudio", TypeVoiceAssistantAudio},
		{"VoiceAssistantAnnounceRequest", TypeVoiceAssistantAnnounceRequest},
		{"VoiceAssistantAnnounceFinished", TypeVoiceAssistantAnnounceFinished},
		{"VoiceAssistantConfigurationRequest", TypeVoiceAssistantConfigurationRequest},
		{"VoiceAssistantConfigurationResponse", TypeVoiceAssistantConfigurationResponse},
		{"VoiceAssistantSetConfiguration", TypeVoiceAssistantSetConfiguration},
		{"VoiceAssistantTimerEventResponse", TypeVoiceAssistantTimerEventResponse},
		{"ListEntitiesMediaPlayerResponse", TypeListEntitiesMediaPlayerResponse},
		{"MediaPlayerCommandRequest", TypeMediaPlayerCommandRequest},
		{"MediaPlayerStateResponse", TypeMediaPlayerStateResponse},
	} {
		block := messageBlock(t, text, want.name)
		id, ok := block["id"]
		if !ok {
			t.Errorf("%s has no id in the upstream protocol", want.name)
			continue
		}
		if id != fmt.Sprint(want.id) {
			t.Errorf("%s is message %s upstream, but the device handles %d",
				want.name, id, want.id)
		}
	}
}

// messageBlock returns the id of a message in the upstream protocol.
//
// The upstream file is the source of the wire format, so the numbers in it
// are the numbers the device has to use.
func messageBlock(t *testing.T, text, name string) map[string]string {
	t.Helper()
	pattern := regexp.MustCompile(`(?s)message ` + name + ` \{(.*?)\n\}`)
	match := pattern.FindStringSubmatch(text)
	if match == nil {
		t.Fatalf("%s is not in the upstream protocol", name)
	}
	fields := map[string]string{}
	for _, line := range strings.Split(match[1], "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "option (id) = ") {
			fields["id"] = strings.TrimSuffix(strings.TrimPrefix(line, "option (id) = "), ";")
		}
	}
	return fields
}
