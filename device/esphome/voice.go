package esphome

import (
	"fmt"
	"log/slog"
	"sync"
	"time"
)

const (
	voiceAssistantFlag uint32 = 1 << 0

	speakerFlag uint32 = 1 << 1

	apiAudioFlag uint32 = 1 << 2

	timersFlag uint32 = 1 << 3

	announceFlag uint32 = 1 << 4

	startConversationFlag uint32 = 1 << 5
)

type Voice struct {
	log *slog.Logger

	mu sync.Mutex

	turn bool

	conversationID string

	wakeWordPhrase string

	startedAt time.Time

	transcript string

	ttsURL string

	streaming bool

	speaking bool

	barrier bool

	spoken string

	lastError string

	wakeWord string

	wakeWordReady bool

	wakeWordEnabled bool

	activeWakeWords []string

	hooks VoiceHooks

	onWakeWord func(enabled bool)
}

type VoiceOptions struct {
	Log *slog.Logger
}

func NewVoice(options VoiceOptions) *Voice {
	log := options.Log
	if log == nil {
		log = slog.Default()
	}
	return &Voice{log: log}
}

func (s *Server) voiceAssistantFlags() uint32 {
	flags := voiceAssistantFlag | speakerFlag | apiAudioFlag | timersFlag | announceFlag
	if s.voice != nil && s.voice.canStartConversations() {
		flags |= startConversationFlag
	}
	return flags
}

func (v *Voice) canStartConversations() bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.wakeWordReady && v.wakeWordEnabled
}

func (v *Voice) handleVoiceEvent(message *VoiceAssistantEventResponse) {
	data := map[string]string{}
	for _, item := range message.GetData() {
		data[item.GetName()] = item.GetValue()
	}

	v.log.Debug("a voice event", "event", message.GetEventType(), "data", data)

	v.mu.Lock()
	barrier := v.barrier
	v.mu.Unlock()
	if barrier {
		if message.GetEventType() != VoiceAssistantEvent_VOICE_ASSISTANT_RUN_START {
			v.log.Debug("an event was discarded by the barrier", "event", message.GetEventType())
			return
		}
		v.mu.Lock()
		v.barrier = false
		v.mu.Unlock()
	}

	switch message.GetEventType() {
	case VoiceAssistantEvent_VOICE_ASSISTANT_RUN_START:

		v.setThinking(true)

		if url := data["url"]; url != "" {
			v.mu.Lock()
			v.ttsURL = url
			v.mu.Unlock()
		}

	case VoiceAssistantEvent_VOICE_ASSISTANT_STT_START:

		v.setThinking(true)

	case VoiceAssistantEvent_VOICE_ASSISTANT_STT_END:

		v.mu.Lock()
		v.transcript = data["text"]
		v.mu.Unlock()
		v.log.Info("home assistant heard", "text", data["text"])

	case VoiceAssistantEvent_VOICE_ASSISTANT_STT_VAD_END:

		v.log.Info("the utterance is over")

		if hooks := v.voiceHooks(); hooks.OnUtteranceEnd != nil {
			hooks.OnUtteranceEnd()
		}

	case VoiceAssistantEvent_VOICE_ASSISTANT_INTENT_PROGRESS:

		if data["tts_start_streaming"] == "1" {
			v.log.Debug("the reply is being written")
		}

	case VoiceAssistantEvent_VOICE_ASSISTANT_INTENT_END:

		v.setThinking(true)

		if speech := data["speech"]; speech != "" {
			v.mu.Lock()
			v.spoken = speech
			v.mu.Unlock()
			v.log.Debug("the answer is", "text", speech)
		}

	case VoiceAssistantEvent_VOICE_ASSISTANT_TTS_START:

		v.setSpeaking(true)

		if text := data["tts_input"]; text != "" {
			v.mu.Lock()
			v.spoken = text
			v.mu.Unlock()
			v.log.Debug("the reply is", "text", text)
		}

	case VoiceAssistantEvent_VOICE_ASSISTANT_TTS_STREAM_START:

		v.log.Debug("the reply is streaming")
		v.mu.Lock()
		v.streaming = true
		v.mu.Unlock()
		v.setSpeaking(true)

	case VoiceAssistantEvent_VOICE_ASSISTANT_TTS_STREAM_END:

		v.log.Debug("the reply has streamed")
		v.mu.Lock()
		v.streaming = false
		v.mu.Unlock()
		v.setSpeaking(false)
		v.endReply()

	case VoiceAssistantEvent_VOICE_ASSISTANT_TTS_END:

		v.log.Debug("the answer has been written")

	case VoiceAssistantEvent_VOICE_ASSISTANT_RUN_END:

		v.log.Debug("the run is over")
		v.endRun()

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

func (v *Voice) handleAnnounce(message *VoiceAssistantAnnounceRequest) {
	v.log.Info("home assistant asked the panel to speak", "text", message.GetText())
	if hooks := v.voiceHooks(); hooks.OnAnnounce != nil {
		hooks.OnAnnounce(message.GetMediaId(), message.GetText())
	}
}

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
			Id:       model,
			WakeWord: model,

			TrainedLanguages: []string{},
		}},

		ActiveWakeWords:    activeWakeWords(model, enabled),
		MaxActiveWakeWords: 1,
	}
}

func activeWakeWords(model string, enabled bool) []string {
	if !enabled {
		return []string{}
	}
	return []string{model}
}

func (v *Voice) handleSetConfiguration(message *VoiceAssistantSetConfiguration) error {
	requested := message.GetActiveWakeWords()

	v.mu.Lock()
	v.activeWakeWords = append([]string(nil), requested...)

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

	if v.onWakeWord != nil {
		v.onWakeWord(enabled)
	}
	return nil
}

func (v *Voice) Transcript() string {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.transcript
}

func (v *Voice) Spoken() string {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.spoken
}

func (v *Voice) SetWakeWordCallback(fn func(enabled bool)) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.onWakeWord = fn
}

func (v *Voice) startTurn(phrase string) {
	v.mu.Lock()
	if v.turn {

		v.barrier = true
		v.mu.Unlock()
		v.log.Debug("a turn is already running")
		return
	}
	v.turn = true
	v.conversationID = newConversationID()
	v.wakeWordPhrase = phrase
	v.startedAt = time.Now()
	v.transcript = ""
	v.spoken = ""
	v.ttsURL = ""
	v.lastError = ""
	v.mu.Unlock()

	v.log.Info("a turn is starting", "phrase", phrase)

	if hooks := v.voiceHooks(); hooks.OnTurn != nil {
		hooks.OnTurn(phrase)
	}
}

func (v *Voice) conversation() string {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.conversationID
}

func (v *Voice) turnRunning() bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.turn
}

func (v *Voice) cutOff() {
	v.mu.Lock()
	if !v.turn {
		v.mu.Unlock()
		return
	}
	v.turn = false

	v.barrier = true
	v.mu.Unlock()

	v.setSpeaking(false)
	v.setThinking(false)

	v.log.Info("the turn was interrupted")

	if hooks := v.voiceHooks(); hooks.OnFlush != nil {
		hooks.OnFlush()
	}
}

func (v *Voice) endRun() {
	v.mu.Lock()
	if !v.turn {
		v.mu.Unlock()
		return
	}
	v.turn = false
	transcript := v.transcript
	failed := v.lastError != ""

	replying := v.speaking
	v.mu.Unlock()

	v.setThinking(false)
	if !replying {
		v.setSpeaking(false)
	}

	v.log.Info("the run is over", "transcript", transcript, "failed", failed)

	if hooks := v.voiceHooks(); hooks.OnEnd != nil {
		hooks.OnEnd(transcript, failed)
	}
}

func (v *Voice) endTurn() {
	v.mu.Lock()
	if !v.turn {
		v.mu.Unlock()
		return
	}
	v.turn = false
	transcript := v.transcript
	failed := v.lastError != ""
	v.spoken = ""
	v.mu.Unlock()

	v.setSpeaking(false)
	v.setThinking(false)

	v.log.Info("the turn is over", "transcript", transcript, "failed", failed)

	if hooks := v.voiceHooks(); hooks.OnEnd != nil {
		hooks.OnEnd(transcript, failed)
	}
}

func (v *Voice) setThinking(thinking bool) {
	if hooks := v.voiceHooks(); hooks.OnThinking != nil {
		hooks.OnThinking(thinking)
	}
}

func (v *Voice) setSpeaking(speaking bool) {
	v.mu.Lock()
	v.speaking = speaking
	v.mu.Unlock()
	if hooks := v.voiceHooks(); hooks.OnSpeaking != nil {
		hooks.OnSpeaking(speaking)
	}
}

func (v *Voice) ReplyPlayedOut() {
	v.setSpeaking(false)
	v.endReply()
}

func (v *Voice) endReply() {
	if hooks := v.voiceHooks(); hooks.OnReplyEnd != nil {
		hooks.OnReplyEnd()
	}
}

func (v *Voice) handleReplyAudio(pcm []byte, final bool) {
	if len(pcm) == 0 && !final {
		return
	}

	if len(pcm) > 0 {
		v.setSpeaking(true)
	}
	if hooks := v.voiceHooks(); hooks.OnAudio != nil {
		hooks.OnAudio(pcm, final)
	}
}

func (v *Voice) SetWakeWord(model string, ready, enabled bool) {
	v.mu.Lock()
	v.wakeWord = model
	v.wakeWordReady = ready
	v.wakeWordEnabled = enabled
	v.mu.Unlock()
}

func newConversationID() string {
	return fmt.Sprintf("%d-%d", time.Now().UnixNano(), conversationCounter.Add(1))
}

var conversationCounter atomicCounter

type atomicCounter struct {
	value int64
	mu    sync.Mutex
}

func (c *atomicCounter) Add(by int64) int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.value += by
	return c.value
}
