package esphome

import (
	"errors"
	"fmt"
	"net"
	"os"
	"regexp"
	"strings"
	"sync"
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

	// The end of the answer is not the end of the reply when the answer
	// is streamed: the audio is still on its way.
	voice.handleVoiceEvent(&VoiceAssistantEventResponse{
		EventType: VoiceAssistantEvent_VOICE_ASSISTANT_TTS_STREAM_START,
	})
	voice.handleVoiceEvent(&VoiceAssistantEventResponse{
		EventType: VoiceAssistantEvent_VOICE_ASSISTANT_TTS_END,
	})
	if !speaking {
		t.Error("the panel stopped speaking when the answer was written")
	}

	// The end of the audio stream is what ends the reply.
	voice.handleVoiceEvent(&VoiceAssistantEventResponse{
		EventType: VoiceAssistantEvent_VOICE_ASSISTANT_TTS_STREAM_END,
	})
	if speaking {
		t.Error("the panel is still speaking when the reply ended")
	}
}

// TestAnAnswerThatIsNotStreamedEnds checks that a reply that arrives
// whole ends when the speaker has drained it.
//
// There is no stream to end when the answer is not streamed, so the
// speaker is what ends the reply.
func TestAnAnswerThatIsNotStreamedEnds(t *testing.T) {
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
	if !speaking {
		t.Error("the panel stopped speaking when the answer was written")
	}

	// The reply is over when the speaker has drained it.
	voice.ReplyPlayedOut()
	if speaking {
		t.Error("the panel is still speaking after the reply was played out")
	}
}

// TestTheRunEndDoesNotCutTheReply checks that the end of the run does not
// end the reply.
//
// Home Assistant ends the run when the agent has written the answer, and
// the answer is still being spoken after that. Ending the reply on the
// run's end cuts it off in the middle.
func TestTheRunEndDoesNotCutTheReply(t *testing.T) {
	voice := NewVoice(VoiceOptions{})

	speaking := false
	voice.SetVoiceHooks(VoiceHooks{OnSpeaking: func(value bool) { speaking = value }})

	voice.startTurn("hey jarvis")
	voice.handleVoiceEvent(&VoiceAssistantEventResponse{
		EventType: VoiceAssistantEvent_VOICE_ASSISTANT_TTS_START,
	})
	voice.handleVoiceEvent(&VoiceAssistantEventResponse{
		EventType: VoiceAssistantEvent_VOICE_ASSISTANT_TTS_STREAM_START,
	})

	// The run ends and the reply is still going.
	voice.handleVoiceEvent(&VoiceAssistantEventResponse{
		EventType: VoiceAssistantEvent_VOICE_ASSISTANT_RUN_END,
	})
	if !speaking {
		t.Error("the run's end cut the reply off")
	}

	// The audio arrives after the run has ended, which is what happens
	// when the answer is streamed.
	voice.handleReplyAudio([]byte{0, 0, 0, 0}, false)
	if !speaking {
		t.Error("the panel is not speaking while the reply is arriving")
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

// TestTheReplyAudioReachesTheSpeaker checks that the reply's audio is
// handed to whoever is playing it.
//
// The reply comes back on the connection as audio. It used to arrive and
// be dropped, and the panel looked like it was answering while it was
// silent.
func TestTheReplyAudioReachesTheSpeaker(t *testing.T) {
	server := NewServer(ServerOptions{Name: "panel", MAC: "AA:BB:CC:DD:EE:FF"})
	voice := NewVoice(VoiceOptions{})
	server.SetVoice(voice)

	var played []byte
	voice.SetVoiceHooks(VoiceHooks{
		OnAudio: func(pcm []byte, final bool) {
			played = append(played, pcm...)
		},
	})

	server.pushReplyAudio(&VoiceAssistantAudio{Data: []byte{1, 2, 3, 4}})
	server.pushReplyAudio(&VoiceAssistantAudio{Data: []byte{5, 6, 7, 8}})

	if len(played) != 8 {
		t.Fatalf("the speaker was given %d bytes of reply, want 8", len(played))
	}
	if played[0] != 1 || played[7] != 8 {
		t.Errorf("the reply was %v, want the bytes in order", played)
	}
}

// TestTheReplyIsEndedByTheEvent checks that the reply's end is said when
// Home Assistant says the reply has streamed.
//
// Home Assistant sends the reply's audio with the end flag clear and
// says the reply is over with an event. A device that waits for the flag
// waits for ever, and the reply is never finished.
func TestTheReplyIsEndedByTheEvent(t *testing.T) {
	server := NewServer(ServerOptions{Name: "panel", MAC: "AA:BB:CC:DD:EE:FF"})
	voice := NewVoice(VoiceOptions{})
	server.SetVoice(voice)

	ended := 0
	voice.SetVoiceHooks(VoiceHooks{
		OnReplyEnd: func() { ended++ },
	})

	voice.handleVoiceEvent(&VoiceAssistantEventResponse{
		EventType: VoiceAssistantEvent_VOICE_ASSISTANT_TTS_STREAM_END,
	})

	if ended != 1 {
		t.Errorf("the reply was ended %d times, want 1", ended)
	}
}

// TestTheAnnouncementIsAskedForByHomeAssistant checks that the request to
// speak reaches the panel.
func TestTheAnnouncementIsAskedForByHomeAssistant(t *testing.T) {
	server := NewServer(ServerOptions{Name: "panel", MAC: "AA:BB:CC:DD:EE:FF"})
	voice := NewVoice(VoiceOptions{})
	server.SetVoice(voice)

	var media, text string
	voice.SetVoiceHooks(VoiceHooks{
		OnAnnounce: func(m, t string) {
			media, text = m, t
		},
	})

	voice.handleAnnounce(&VoiceAssistantAnnounceRequest{
		MediaId: "http://homeassistant/media.wav",
		Text:    "the time is ten past",
	})

	if media != "http://homeassistant/media.wav" {
		t.Errorf("the announcement's media was %q", media)
	}
	if text != "the time is ten past" {
		t.Errorf("the announcement's text was %q", text)
	}
}

// TestTheCapabilitiesCarryTheVoiceFlags checks that the device's
// capabilities say what the device can do.
//
// From API 1.15 onwards Home Assistant reads the voice assistant's flags
// from the capabilities rather than from the device information. An empty
// answer says the device has no voice assistant at all, and Home
// Assistant then never sends the reply audio over the connection — which
// is a panel that looks like it is answering while it is silent.
func TestTheCapabilitiesCarryTheVoiceFlags(t *testing.T) {
	server := NewServer(ServerOptions{Name: "panel", MAC: "AA:BB:CC:DD:EE:FF"})
	voice := NewVoice(VoiceOptions{})
	voice.SetWakeWord("hey_jarvis", true, true)
	server.SetVoice(voice)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go server.Listen(listener)

	lines, err := runPeer("capabilities", listener.Addr().String(), "")
	if err != nil {
		if errors.Is(err, errSkipPeer) {
			t.Skip("the python peer is not available")
		}
		t.Fatalf("the client could not read the capabilities: %v", err)
	}

	flags := uint32(0)
	found := false
	for _, line := range lines {
		if value, ok := strings.CutPrefix(line, "voice_flags:"); ok {
			found = true
			if _, err := fmt.Sscanf(value, "%d", &flags); err != nil {
				t.Fatalf("the voice flags were %q: %v", value, err)
			}
		}
	}
	if !found {
		t.Fatalf("the capabilities said nothing about the voice assistant: %v", lines)
	}

	// The flags are the ones Home Assistant reads to decide how to talk
	// to the device: the API audio flag is what makes it send the reply
	// over the connection rather than over UDP.
	for _, want := range []struct {
		name string
		flag uint32
	}{
		{"the voice assistant", voiceAssistantFlag},
		{"the speaker", speakerFlag},
		{"the API audio", apiAudioFlag},
		{"announcements", announceFlag},
		{"starting conversations", startConversationFlag},
	} {
		if flags&want.flag == 0 {
			t.Errorf("the capabilities did not say %s: the flags were %d", want.name, flags)
		}
	}
}

// TestTheReplyComesOverTheConnection checks that the reply's audio
// arrives on the connection and reaches the speaker.
//
// This is the whole round trip, driven by the library Home Assistant
// uses: the audio goes over the wire the way Home Assistant sends it,
// with the end flag clear, and the reply is ended with the event that
// Home Assistant sends.
func TestTheReplyComesOverTheConnection(t *testing.T) {
	server := NewServer(ServerOptions{Name: "panel", MAC: "AA:BB:CC:DD:EE:FF"})
	voice := NewVoice(VoiceOptions{})
	server.SetVoice(voice)

	var mu sync.Mutex
	var played []byte
	ended := 0
	voice.SetVoiceHooks(VoiceHooks{
		OnAudio: func(pcm []byte, final bool) {
			mu.Lock()
			played = append(played, pcm...)
			mu.Unlock()
		},
		OnReplyEnd: func() {
			mu.Lock()
			ended++
			mu.Unlock()
		},
	})

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go server.Listen(listener)

	if _, err := runPeer("reply_audio", listener.Addr().String(), ""); err != nil {
		if errors.Is(err, errSkipPeer) {
			t.Skip("the python peer is not available")
		}
		t.Fatalf("the client could not send the reply: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(played) != 128 {
		t.Errorf("the speaker was given %d bytes of reply, want 128", len(played))
	}
	if ended != 1 {
		t.Errorf("the reply was ended %d times, want 1", ended)
	}
}

// TestTheUtteranceEndIsSent checks that the device tells Home Assistant
// that the utterance is over.
//
// It is an empty block of audio with the end flag on it, and it is what
// closes the pipeline on the device's side. Without it the pipeline is
// left open, and a turn that ends badly sits there until something
// times out.
//
// The peer is the real client, so the flag is checked where it is
// actually read.
func TestTheUtteranceEndIsSent(t *testing.T) {
	server := NewServer(ServerOptions{Name: "panel", MAC: "AA:BB:CC:DD:EE:FF"})
	voice := NewVoice(VoiceOptions{})
	server.SetVoice(voice)

	// The end is sent when the utterance is over.
	sent := make(chan bool, 4)
	voice.SetVoiceHooks(VoiceHooks{
		OnUtteranceEnd: func() {
			if err := server.SendVoiceEnd(); err == nil {
				sent <- true
			}
		},
	})

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go server.Listen(listener)

	lines, err := runPeer("utterance_end", listener.Addr().String(), "")
	if err != nil {
		if errors.Is(err, errSkipPeer) {
			t.Skip("the python peer is not available")
		}
		t.Fatalf("the peer failed: %v", err)
	}
	_ = lines

	select {
	case <-sent:
	case <-time.After(5 * time.Second):
		t.Fatal("the end of the utterance was never sent")
	}
}

// TestTheBarrierDiscardsTheAbortedTurn checks that the tail of an
// aborted turn is thrown away.
//
// Home Assistant acknowledges an abort with no wire message at all, so
// the aborted run's tail lands on the new turn and Home Assistant reads
// it as "Home Assistant ended a run it never started" and ends the
// turn. Every barge-in would end with no audio captured.
//
// The barrier is armed when the new turn starts and every event is
// discarded until the run start that belongs to it.
func TestTheBarrierDiscardsTheAbortedTurn(t *testing.T) {
	voice := NewVoice(VoiceOptions{})

	ended := 0
	voice.SetVoiceHooks(VoiceHooks{
		OnEnd: func(string, bool) { ended++ },
	})

	// A turn starts.
	voice.startTurn("hey jarvis")

	// A barge-in: the turn is interrupted, which arms the barrier, and
	// the new turn starts.
	voice.cutOff()
	voice.startTurn("hey jarvis")

	// The aborted run's tail arrives. It must be discarded.
	voice.handleVoiceEvent(&VoiceAssistantEventResponse{
		EventType: VoiceAssistantEvent_VOICE_ASSISTANT_RUN_END,
	})
	if ended != 0 {
		t.Errorf("the aborted turn's tail ended the new turn, %d times", ended)
	}

	// The error that comes with it must be discarded too.
	voice.handleVoiceEvent(&VoiceAssistantEventResponse{
		EventType: VoiceAssistantEvent_VOICE_ASSISTANT_ERROR,
		Data: []*VoiceAssistantEventData{
			{Name: "message", Value: "cancelled"},
		},
	})
	if voice.lastError != "" {
		t.Errorf("the aborted turn's error was taken: %q", voice.lastError)
	}

	// The new turn's run start ends the barrier.
	voice.handleVoiceEvent(&VoiceAssistantEventResponse{
		EventType: VoiceAssistantEvent_VOICE_ASSISTANT_RUN_START,
	})

	// And the events after it are taken as they are.
	voice.handleVoiceEvent(&VoiceAssistantEventResponse{
		EventType: VoiceAssistantEvent_VOICE_ASSISTANT_RUN_END,
	})
	if ended != 1 {
		t.Errorf("the new turn's end was not taken, the turn ended %d times", ended)
	}
}

// TestTheBarrierIsNotArmedWithoutABarge checks that an ordinary turn is
// not behind a barrier.
//
// The barrier is for a barge-in: a turn that starts while another is
// running. An ordinary turn must see every event.
func TestTheBarrierIsNotArmedWithoutABarge(t *testing.T) {
	voice := NewVoice(VoiceOptions{})

	thinking := false
	voice.SetVoiceHooks(VoiceHooks{
		OnThinking: func(value bool) { thinking = value },
	})

	voice.startTurn("hey jarvis")
	voice.handleVoiceEvent(&VoiceAssistantEventResponse{
		EventType: VoiceAssistantEvent_VOICE_ASSISTANT_RUN_START,
	})
	voice.handleVoiceEvent(&VoiceAssistantEventResponse{
		EventType: VoiceAssistantEvent_VOICE_ASSISTANT_INTENT_END,
	})
	if !thinking {
		t.Error("the event after the run start was discarded")
	}
}
