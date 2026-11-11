package esphome

import (
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// The voice assistant feature flags.
//
// The flags ride DeviceInfoResponse, which is a one-shot at connect, so
// they are decided once and stay decided. A device that says it can do
// something and then cannot is worse than a device that never said so.
const (
	// voiceAssistantFlag says the device is a voice assistant at all.
	voiceAssistantFlag uint32 = 1 << 0
	// speakerFlag says the device has a speaker and the reply comes back
	// as audio on the connection rather than as a URL to fetch.
	speakerFlag uint32 = 1 << 1
	// apiAudioFlag says the reply audio travels over the same encrypted
	// connection, rather than over a separate UDP socket.
	apiAudioFlag uint32 = 1 << 2
	// timersFlag says the device wants the timer events, so "set a timer"
	// reaches it rather than staying in home assistant.
	timersFlag uint32 = 1 << 3
	// announceFlag says the device can be asked to speak.
	//
	// It is needed for the wake word picker to appear at all: home
	// assistant asks for the configuration over
	// VoiceAssistantConfigurationRequest, and it does not ask unless this
	// flag is set.
	announceFlag uint32 = 1 << 4
	// startConversationFlag says the device can start a conversation by
	// itself, which is what a local wake word does.
	//
	// It is the one flag that is per device rather than constant, because
	// it is the one that needs hardware: a panel with no microphone must
	// not carry it, or it appears in the picker and answers every
	// question with silence.
	startConversationFlag uint32 = 1 << 5
)

// The request flags for a voice turn.
//
// The values are a bitmask on the wire and are not a protobuf enum.
const (
	// requestUseVAD says home assistant should use its own voice activity
	// detection to find the end of the utterance.
	requestUseVAD uint32 = 1 << 0
	// requestUseWakeWord says home assistant should run its own wake word
	// detection on the audio.
	//
	// The panel does not set it when the wake word fired locally: the
	// wake word has already happened, and asking home assistant to look
	// for it again would make it wait for a second one.
	requestUseWakeWord uint32 = 1 << 1
)

// voiceAssistant is the panel's voice assistant.
//
// It is one turn at a time: the wake word fires, the turn starts, the
// microphone streams to home assistant, home assistant works it out and
// sends the reply back as audio, and the reply plays.
//
// The device does not detect the end of the utterance. Home assistant
// does, with its own voice activity detection, and says so by ending the
// turn. That is why there is no speech detector in this file: the
// microphone is open for exactly as long as home assistant wants it.
// Voice is the panel's voice assistant.
//
// It is the part of the ESPHome protocol that makes the panel a voice
// satellite: it starts turns, carries the microphone to Home Assistant
// and plays the reply.
type Voice struct {
	log *slog.Logger

	mu sync.Mutex
	// turn is true while a turn is running.
	turn bool
	// conversationID identifies the turn to home assistant.
	conversationID string
	// wakeWordPhrase is the phrase that fired, or is empty when the turn
	// did not start with a wake word.
	wakeWordPhrase string
	// startedAt is when the turn began.
	startedAt time.Time
	// transcript is what home assistant heard.
	transcript string
	// lastError is the most recent failure in the turn.
	lastError string

	// wakeWord is the model the panel is listening for.
	wakeWord string
	// wakeWordReady says the local wake word can run.
	wakeWordReady bool
	// wakeWordEnabled says the local wake word is switched on.
	wakeWordEnabled bool
	// activeWakeWords are the models home assistant has switched on.
	activeWakeWords []string

	// onTurn is called when a turn starts, with the phrase that fired.
	onTurn func(phrase string)
	// onEnd is called when a turn ends, with the transcript and whether
	// it ended in an error.
	onEnd func(transcript string, failed bool)
	// onEvent is called for the events home assistant sends, so the panel
	// can show what is happening.
	onEvent func(event VoiceAssistantEvent)
	// onAnnounce is called when home assistant asks the panel to speak.
	//
	// The panel has no way to fetch the audio itself — the reply comes
	// back on the connection as audio — so the handler is given the media
	// id and the text, and the announcement is played when the audio
	// arrives.
	onAnnounce func(mediaID, text string)
	// onThinking is called when home assistant starts or stops working.
	onThinking func(thinking bool)
	// onSpeaking is called when the reply starts or stops.
	onSpeaking func(speaking bool)
	// hooks are the callbacks the device program wires.
	hooks VoiceHooks
}

// voiceOptions is what a voice assistant is made with.
// VoiceOptions is what a voice assistant is made with.
type VoiceOptions struct {
	// Log is where the messages go.
	Log *slog.Logger
}

// newVoiceAssistant makes a voice assistant.
// NewVoice makes a voice assistant.
func NewVoice(options VoiceOptions) *Voice {
	log := options.Log
	if log == nil {
		log = slog.Default()
	}
	return &Voice{log: log}
}

// voiceAssistantFlags are the flags this device advertises.
//
// The start-conversation flag is added when the panel has a microphone
// and a local wake word, because that is what lets it begin a turn by
// itself.
func (s *Server) voiceAssistantFlags() uint32 {
	flags := voiceAssistantFlag | speakerFlag | apiAudioFlag | timersFlag | announceFlag
	if s.voice != nil && s.voice.canStartConversations() {
		flags |= startConversationFlag
	}
	return flags
}

// canStartConversations reports whether the panel can start a turn by
// itself.
//
// It needs a microphone and a local wake word. Without both, home
// assistant has to do the detecting, and the panel is a speaker rather
// than a satellite.
func (v *Voice) canStartConversations() bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.wakeWordReady && v.wakeWordEnabled
}

// handleVoiceEvent handles an event from home assistant.
//
// The events are what home assistant says while it works: that the turn
// started, that it heard something, that the intent matched, that the
// reply is coming.
func (v *Voice) handleVoiceEvent(message *VoiceAssistantEventResponse) {
	data := map[string]string{}
	for _, item := range message.GetData() {
		data[item.GetName()] = item.GetValue()
	}

	v.log.Debug("a voice event", "event", message.GetEventType(), "data", data)

	switch message.GetEventType() {
	case VoiceAssistantEvent_VOICE_ASSISTANT_RUN_START:
		// A run that has started is home assistant working on the turn.
		v.setThinking(true)

	case VoiceAssistantEvent_VOICE_ASSISTANT_STT_START:
		// Home assistant has started transcribing.
		v.setThinking(true)

	case VoiceAssistantEvent_VOICE_ASSISTANT_STT_END:
		// The final text. Nothing more can change it, so the microphone
		// is no longer interesting.
		v.mu.Lock()
		v.transcript = data["text"]
		v.mu.Unlock()
		v.log.Info("home assistant heard", "text", data["text"])

	case VoiceAssistantEvent_VOICE_ASSISTANT_STT_VAD_END:
		// Home assistant's voice activity detection says the utterance is
		// over. The microphone can be closed.
		v.log.Info("the utterance is over")

	case VoiceAssistantEvent_VOICE_ASSISTANT_INTENT_END:
		// The intent matched. The reply is coming.
		v.setThinking(true)

	case VoiceAssistantEvent_VOICE_ASSISTANT_TTS_START:
		// The reply is starting to arrive.
		v.setSpeaking(true)

	case VoiceAssistantEvent_VOICE_ASSISTANT_TTS_END:
		// The reply has all arrived.
		v.setSpeaking(false)

	case VoiceAssistantEvent_VOICE_ASSISTANT_RUN_END:
		// The turn is over.
		v.endTurn()

	case VoiceAssistantEvent_VOICE_ASSISTANT_ERROR:
		v.mu.Lock()
		v.lastError = data["message"]
		v.mu.Unlock()
		v.log.Warn("home assistant reported an error", "error", data["message"])
		v.setSpeaking(false)
		v.setThinking(false)

	case VoiceAssistantEvent_VOICE_ASSISTANT_WAKE_WORD_REJECTED:
		v.log.Info("home assistant rejected the wake word")
	}

	if hooks := v.voiceHooks(); hooks.OnEvent != nil {
		hooks.OnEvent(message.GetEventType())
	}
}

// handleAnnounce handles a request from home assistant to speak.
//
// The panel does not fetch the audio: the reply comes back on the
// connection as audio, in the same way a spoken reply does. So the
// announcement is a request to play something, and the audio that
// follows is the something.
func (v *Voice) handleAnnounce(message *VoiceAssistantAnnounceRequest) {
	v.log.Info("home assistant asked the panel to speak", "text", message.GetText())
	if hooks := v.voiceHooks(); hooks.OnAnnounce != nil {
		hooks.OnAnnounce(message.GetMediaId(), message.GetText())
	}
}

// handleConfigurationRequest answers home assistant's question about the
// wake words.
//
// The list it is given is what its picker shows, so an empty list is how
// "no wake word" survives a reconnect.
func (v *Voice) handleConfigurationRequest() *VoiceAssistantConfigurationResponse {
	v.mu.Lock()
	model := v.wakeWord
	ready := v.wakeWordReady
	enabled := v.wakeWordEnabled
	v.mu.Unlock()

	if model == "" || !ready {
		return &VoiceAssistantConfigurationResponse{
			AvailableWakeWords: []*VoiceAssistantWakeWord{},
			ActiveWakeWords:    []string{},
			MaxActiveWakeWords: 1,
		}
	}

	return &VoiceAssistantConfigurationResponse{
		AvailableWakeWords: []*VoiceAssistantWakeWord{{
			Id:      model,
			WakeWord: model,
			// The languages the model was trained on are not known, and
			// saying so is better than guessing: home assistant shows the
			// phrase, and the phrase is what the panel will announce.
			TrainedLanguages: []string{},
		}},
		// The active list says whether the wake word is switched on.
		//
		// It matters that this reflects what the panel is really doing:
		// home assistant shows the picker as on when the model is in the
		// active list, and a picker that says on while the panel is not
		// listening is a picker that lies.
		ActiveWakeWords:    activeWakeWords(model, enabled),
		MaxActiveWakeWords: 1,
	}
}

// activeWakeWords is the active list for a wake word.
//
// An empty list means "no wake word", which is how the picker shows that
// detection is off.
func activeWakeWords(model string, enabled bool) []string {
	if !enabled {
		return []string{}
	}
	return []string{model}
}

// handleSetConfiguration applies what home assistant's picker chose.
//
// An empty list means "no wake word", which switches the local detection
// off. It is applied straight away, because home assistant reads the
// configuration back immediately after writing it.
func (v *Voice) handleSetConfiguration(message *VoiceAssistantSetConfiguration) error {
	requested := message.GetActiveWakeWords()

	v.mu.Lock()
	v.activeWakeWords = append([]string(nil), requested...)
	// The panel has one model. Home assistant asking for it switches it
	// on; asking for nothing switches it off; asking for something else
	// is a request the panel cannot satisfy, and the panel keeps what it
	// had rather than pretending.
	model := v.wakeWord
	switch {
	case len(requested) == 0:
		v.wakeWordEnabled = false
	case model != "" && requested[0] == model:
		v.wakeWordEnabled = true
	default:
		v.mu.Unlock()
		return fmt.Errorf("esphome: home assistant asked for the wake words %v, which this panel does not have", requested)
	}
	enabled := v.wakeWordEnabled
	v.mu.Unlock()

	v.log.Info("the wake word was switched", "enabled", enabled)
	return nil
}

// startTurn begins a turn.
//
// The phrase is the wake word that fired, or empty when the turn did not
// start with one — a turn home assistant began itself, or one started by
// the panel's button.
//
// The phrase must not be empty for a wake word turn. Home assistant's
// setup dialog reads it and refuses the run when it is missing, so a
// panel that sends an empty phrase cannot complete the one flow that
// asks it to prove it heard a wake word.
func (v *Voice) startTurn(phrase string) {
	v.mu.Lock()
	if v.turn {
		v.mu.Unlock()
		v.log.Debug("a turn is already running")
		return
	}
	v.turn = true
	v.conversationID = newConversationID()
	v.wakeWordPhrase = phrase
	v.startedAt = time.Now()
	v.transcript = ""
	v.lastError = ""
	v.mu.Unlock()

	v.log.Info("a turn is starting", "phrase", phrase)

	if hooks := v.voiceHooks(); hooks.OnTurn != nil {
		hooks.OnTurn(phrase)
	}
}

// conversation returns the identifier of the current turn.
//
// It is empty when no turn is running.
func (v *Voice) conversation() string {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.conversationID
}

// turnRunning reports whether a turn is running.
func (v *Voice) turnRunning() bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.turn
}

// endTurn finishes the turn.
func (v *Voice) endTurn() {
	v.mu.Lock()
	if !v.turn {
		v.mu.Unlock()
		return
	}
	v.turn = false
	transcript := v.transcript
	failed := v.lastError != ""
	v.mu.Unlock()

	v.setSpeaking(false)
	v.setThinking(false)

	v.log.Info("the turn is over", "transcript", transcript, "failed", failed)

	if hooks := v.voiceHooks(); hooks.OnEnd != nil {
		hooks.OnEnd(transcript, failed)
	}
}

// setThinking records whether home assistant is working on the turn.
func (v *Voice) setThinking(thinking bool) {
	if hooks := v.voiceHooks(); hooks.OnThinking != nil {
		hooks.OnThinking(thinking)
	}
}

// setSpeaking records whether the reply is playing.
func (v *Voice) setSpeaking(speaking bool) {
	if hooks := v.voiceHooks(); hooks.OnSpeaking != nil {
		hooks.OnSpeaking(speaking)
	}
}

// SetWakeWord records the local wake word's state.
//
// It is what Home Assistant's picker shows, and it decides whether the
// panel can start a conversation by itself.
func (v *Voice) SetWakeWord(model string, ready, enabled bool) {
	v.mu.Lock()
	v.wakeWord = model
	v.wakeWordReady = ready
	v.wakeWordEnabled = enabled
	v.mu.Unlock()
}

// newConversationID makes the identifier for a turn.
//
// It only has to be unique among the turns of this device, and it is
// what home assistant uses to tell one run from another.
func newConversationID() string {
	return fmt.Sprintf("%d-%d", time.Now().UnixNano(), conversationCounter.Add(1))
}

// conversationCounter makes the identifiers unique when two turns start in
// the same nanosecond.
var conversationCounter atomicCounter

// atomicCounter is a counter that can be incremented from several
// goroutines.
type atomicCounter struct {
	value int64
	mu    sync.Mutex
}

// Add increments the counter and returns the new value.
func (c *atomicCounter) Add(by int64) int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.value += by
	return c.value
}
