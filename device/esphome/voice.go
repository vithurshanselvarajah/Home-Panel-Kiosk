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
	// ttsURL is the reply's audio address, taken from the run start.
	//
	// Home Assistant tells the satellite about the reply twice: the run
	// start carries the address, and the intent progress says when the
	// reply may begin. Waiting for the end of the reply instead means
	// every second of generating the answer sits inside the wait.
	ttsURL string
	// streaming is whether the reply's audio is streaming.
	//
	// It is what tells the end of the answer from the end of the reply:
	// a streamed answer is ended by the end of its stream, and an answer
	// that arrives whole is ended by the end of the answer.
	streaming bool
	// speaking is whether the reply is being spoken.
	//
	// It is kept here because the end of the run and the end of the
	// reply are different things: home assistant ends the run when the
	// agent has written the answer, and the answer is still being
	// spoken after that.
	speaking bool
	// barrier says whether the events are being thrown away.
	//
	// Home Assistant acknowledges an abort with no wire message at all:
	// it just cancels the task. So when a barge-in happens, the aborted
	// run's tail lands on the new turn, and Home Assistant reads it as
	// "Home Assistant ended a run it never started" and ends the turn.
	//
	// The fix is ordering rather than timing: the barrier is armed on
	// the abort and every event is discarded until the next run start,
	// which is necessarily the new turn's, because Home Assistant emits
	// one only when it creates a pipeline and the panel created exactly
	// one more.
	//
	// Without it every barge-in ends with no audio captured.
	barrier bool
	// spoken is what home assistant is saying.
	//
	// It is kept apart from the transcript because the two mean
	// different things: the transcript is what was heard and this is
	// what is being said.
	spoken string
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

	// hooks are the callbacks the device program wires.
	hooks VoiceHooks
	// onWakeWord is called when home assistant's picker switches the wake
	// word on or off.
	//
	// It is the same switch the panel's own settings make, so both have
	// to reach the same place: a panel that is listening when home
	// assistant says it is not, and not listening when it says it is, is
	// a panel that lies about itself twice.
	onWakeWord func(enabled bool)
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

	// The barrier discards the tail of the aborted turn.
	//
	// The run start is the one event that is not discarded: it is the
	// new turn's, and it is what ends the barrier.
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
		// A run that has started is home assistant working on the turn.
		v.setThinking(true)

		// The reply's address comes with it. The reply may begin before
		// home assistant has finished writing it, and the address is what
		// says where to get it.
		if url := data["url"]; url != "" {
			v.mu.Lock()
			v.ttsURL = url
			v.mu.Unlock()
		}

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

		// The end of the utterance is told to Home Assistant as well, on
		// the wire, with the end flag on the audio.
		//
		// Its own detection has already closed the pipeline by the time
		// this arrives, so the flag is belt and braces: without it the
		// pipeline is left open on the device's side, and a turn that
		// ends badly can sit there until something times out.
		if hooks := v.voiceHooks(); hooks.OnUtteranceEnd != nil {
			hooks.OnUtteranceEnd()
		}

	case VoiceAssistantEvent_VOICE_ASSISTANT_INTENT_PROGRESS:
		// Home Assistant writes the answer as it goes, and says here when
		// the first of it is ready.
		//
		// The device has a speaker, so the reply's audio comes back over
		// the connection rather than being fetched from the address. The
		// address is only used by a device without a speaker, and this
		// one is not that.
		if data["tts_start_streaming"] == "1" {
			v.log.Debug("the reply is being written")
		}

	case VoiceAssistantEvent_VOICE_ASSISTANT_INTENT_END:
		// The intent matched. The reply is coming.
		v.setThinking(true)

		// The answer's words may be here.
		//
		// Home assistant sends the answer's text with the intent, but
		// only the plain kind of it: an answer written in another kind
		// of markup is empty here. It is kept as a fallback, for when
		// the text-to-speech does not carry the words either.
		if speech := data["speech"]; speech != "" {
			v.mu.Lock()
			v.spoken = speech
			v.mu.Unlock()
			v.log.Debug("the answer is", "text", speech)
		}

	case VoiceAssistantEvent_VOICE_ASSISTANT_TTS_START:
		// The reply is starting to arrive.
		v.setSpeaking(true)

		// The words of the reply are here too, so the panel can show
		// what is being said.
		//
		// This is the reliable one: the text going into the text-to-speech
		// is whatever the answer is, whatever kind of markup it is in.
		//
		// They are kept apart from the transcript: the transcript is
		// what was heard and this is what is being said, and the panel
		// shows them differently.
		if text := data["tts_input"]; text != "" {
			v.mu.Lock()
			v.spoken = text
			v.mu.Unlock()
			v.log.Debug("the reply is", "text", text)
		}

	case VoiceAssistantEvent_VOICE_ASSISTANT_TTS_STREAM_START:
		// The reply's audio is about to start arriving as a stream.
		//
		// The reply is streamed rather than sent in one piece when
		// home assistant can stream it, and the stream has its own start
		// and end events. The end is what says the reply is over.
		//
		// This is what says the reply is being spoken. The end of the
		// run comes first: home assistant ends the pipeline when the
		// agent has written the answer, and the answer is still being
		// spoken after that.
		v.log.Debug("the reply is streaming")
		v.mu.Lock()
		v.streaming = true
		v.mu.Unlock()
		v.setSpeaking(true)

	case VoiceAssistantEvent_VOICE_ASSISTANT_TTS_STREAM_END:
		// The reply has all arrived.
		//
		// This is the end of the reply's audio. Home Assistant sends the
		// audio with the end flag clear and says here that it is done,
		// so this is the only thing that ends a reply.
		v.log.Debug("the reply has streamed")
		v.mu.Lock()
		v.streaming = false
		v.mu.Unlock()
		v.setSpeaking(false)
		v.endReply()

	case VoiceAssistantEvent_VOICE_ASSISTANT_TTS_END:
		// Home Assistant has written the answer and is now sending it.
		//
		// The reply is not over. The end of the answer comes before the
		// audio arrives — even when the answer is streamed, the
		// streaming task starts afterwards — so this is not where the
		// reply ends.
		//
		// The reply ends when its audio has all arrived, which is the
		// end of the stream, or when the speaker has drained it.
		v.log.Debug("the answer has been written")

	case VoiceAssistantEvent_VOICE_ASSISTANT_RUN_END:
		// The run is over: home assistant has finished writing the
		// answer and the pipeline is done.
		//
		// The reply is not over. When the answer is streamed, home
		// assistant ends the run and then keeps sending the audio, so
		// ending the reply here would cut it off in the middle.
		//
		// The reply's own end is the end of the audio stream, which is
		// what ends it.
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
			Id:       model,
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

	if v.onWakeWord != nil {
		v.onWakeWord(enabled)
	}
	return nil
}

// SetWakeWordCallback sets the callback for the wake word being switched.
//
// Transcript says what Home Assistant heard.
//
// The panel shows it while the assistant is thinking about it, so the
// person can see that it heard what they said.
func (v *Voice) Transcript() string {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.transcript
}

// Spoken says what Home Assistant is saying.
//
// The panel shows it while the reply is being played.
func (v *Voice) Spoken() string {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.spoken
}

// It is separate from the hooks because it is the device program's own
// business: the wake word is a thing the panel runs, and the panel has to
// be told when home assistant switches it off.
func (v *Voice) SetWakeWordCallback(fn func(enabled bool)) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.onWakeWord = fn
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
		// A turn is already running, so this one is a barge-in: the
		// running turn is being interrupted.
		//
		// The barrier is armed for it. Home Assistant acknowledges the
		// abort with no wire message at all, so the aborted run's tail
		// lands on this turn and Home Assistant reads it as "Home
		// Assistant ended a run it never started" and ends the turn.
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
// cutOff ends the turn that is running, for a barge-in.
//
// The reply that is playing is dropped rather than waited for, and the
// barrier is armed so that the aborted run's tail does not land on the
// new turn.
//
// The turn is ended with the flush rather than the ordinary end: the
// reply was cut off in the middle, not finished.
func (v *Voice) cutOff() {
	v.mu.Lock()
	if !v.turn {
		v.mu.Unlock()
		return
	}
	v.turn = false
	// The barrier is armed: the run that was running is being
	// interrupted, and its tail is on its way.
	v.barrier = true
	v.mu.Unlock()

	v.setSpeaking(false)
	v.setThinking(false)

	v.log.Info("the turn was interrupted")

	if hooks := v.voiceHooks(); hooks.OnFlush != nil {
		hooks.OnFlush()
	}
}

// endRun says the run is over.
//
// The run is home assistant's pipeline: the agent has written the
// answer and the pipeline is done. The reply may still be streaming,
// because home assistant ends the run and then keeps sending the audio.
//
// The reply's own end is the end of the audio stream.
func (v *Voice) endRun() {
	v.mu.Lock()
	if !v.turn {
		v.mu.Unlock()
		return
	}
	v.turn = false
	transcript := v.transcript
	failed := v.lastError != ""
	// The reply is still speaking, so the speaking flag stays on: the
	// reply's end is what turns it off.
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

// endTurn says the whole turn is over: the run and the reply.
//
// It is used when the turn ends without a reply, and by the places that
// are not the protocol's own end.
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

// setThinking records whether home assistant is working on the turn.
func (v *Voice) setThinking(thinking bool) {
	if hooks := v.voiceHooks(); hooks.OnThinking != nil {
		hooks.OnThinking(thinking)
	}
}

// setSpeaking records whether the reply is playing.
func (v *Voice) setSpeaking(speaking bool) {
	v.mu.Lock()
	v.speaking = speaking
	v.mu.Unlock()
	if hooks := v.voiceHooks(); hooks.OnSpeaking != nil {
		hooks.OnSpeaking(speaking)
	}
}

// replyPlayedOut says the reply has been said.
//
// It is called when the speaker has drained the reply, which is the only
// moment the reply is really over: the last frame arriving is not the
// end, because the speaker still has the words in it.
//
// It is the speaker that knows, and it is the speaker that says.
func (v *Voice) ReplyPlayedOut() {
	v.setSpeaking(false)
	v.endReply()
}

// endReply says the reply's audio has all arrived.
//
// It is the reply's end, and it is not the same as the turn's end: the
// turn may carry more than a reply, and the reply is over before the
// turn is.
func (v *Voice) endReply() {
	if hooks := v.voiceHooks(); hooks.OnReplyEnd != nil {
		hooks.OnReplyEnd()
	}
}

// handleReplyAudio hands the reply's audio to the speaker.
//
// The audio comes back on the same connection as everything else, so it
// has to be handed over rather than fetched, and the panel plays it as
// it arrives.
//
// The final flag is only ever set for a reply that was cut short. Home
// Assistant says the reply is over with an event, because the audio
// itself carries no end marker.
func (v *Voice) handleReplyAudio(pcm []byte, final bool) {
	if len(pcm) == 0 && !final {
		return
	}
	// The audio arriving is what says the reply is being spoken.
	//
	// The stream's start event says so too, but it is sent by the
	// streaming task and the audio can arrive before it does, because
	// home assistant ends the run and then keeps sending the audio. The
	// audio is the thing that is certain.
	if len(pcm) > 0 {
		v.setSpeaking(true)
	}
	if hooks := v.voiceHooks(); hooks.OnAudio != nil {
		hooks.OnAudio(pcm, final)
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
