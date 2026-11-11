// Package config is what the panel is told to do.
//
// It is the panel's own settings: the microphone and the speaker, the
// volume, the music, and the wake word.
//
// Every field is a pointer, so an absent key means "the application did
// not set this" and the panel keeps what it had. A plain value would make
// "unset" and "set to zero" the same thing, and a panel that has never
// been told what its wake-word threshold is would then have a threshold of
// zero, which scores every sound as a wake word.
package config

// Config is the panel's configuration.
type Config struct {
	// ── Microphone ───────────────────────────────────────────────────────
	// MicGainDb is the microphone's gain in decibels.
	MicGainDb *int `json:"micGainDb,omitempty"`
	// VadThreshold is the level, as a fraction of full scale, above which
	// the room counts as speaking.
	VadThreshold *float64 `json:"vadThreshold,omitempty"`
	// VadSpeechMs is how long the room has to keep talking before the
	// turn is opened.
	VadSpeechMs *int `json:"vadSpeechMs,omitempty"`
	// VadSilenceMs is how long the room has to be quiet before the turn
	// is closed.
	VadSilenceMs *int `json:"vadSilenceMs,omitempty"`

	// ── Wake word ───────────────────────────────────────────────────────
	// WakeWordEnabled is a pointer: false is "no wake word", and that is
	// the value that matters.
	WakeWordEnabled *bool `json:"wakeWordEnabled,omitempty"`
	// WakeWordModel is the model to listen for. It is either a bare name
	// ("hey_jarvis_v0.1") or the path of an ONNX file.
	WakeWordModel string `json:"wakeWordModel,omitempty"`
	// WakeThreshold is the score the wake word has to reach to fire.
	WakeThreshold *float64 `json:"wakeThreshold,omitempty"`
	// WakeSound plays a sound when the wake word fires.
	WakeSound *bool `json:"wakeSound,omitempty"`
	// BargeIn lets a person interrupt a reply that is playing.
	BargeIn *bool `json:"bargeIn,omitempty"`

	// ── Playback ────────────────────────────────────────────────────────
	// StartupVolume is the level the speaker starts at.
	StartupVolume *int `json:"startupVolume,omitempty"`
	// Volume is the current level.
	Volume *int `json:"volume,omitempty"`
	// DuckDb is how far the music is turned down while a reply plays.
	DuckDb *int `json:"duckDb,omitempty"`
	// MuteButton is the panel's own mute switch.
	MuteButton *bool `json:"muteButton,omitempty"`

	// ── Music ───────────────────────────────────────────────────────────
	// SendspinEnabled turns the music player on and off.
	SendspinEnabled *bool `json:"sendspinEnabled,omitempty"`
	// SendspinName is the name the player is known by in Music Assistant.
	SendspinName *string `json:"sendspinName,omitempty"`
	// SendspinUnpaired lets a server play without pairing. Off by
	// default: anyone on the network can claim to be a server, and
	// pairing is one paste of the device's token.
	SendspinUnpaired *bool `json:"sendspinUnpaired,omitempty"`

	// ── Hardware ────────────────────────────────────────────────────────
	// AudioInput is the microphone to use. An empty name is the system
	// default.
	AudioInput *string `json:"audioInput,omitempty"`
	// AudioOutput is the speaker to use. An empty name is the system
	// default.
	AudioOutput *string `json:"audioOutput,omitempty"`
	// MonitorMic opens the microphone so its level can be shown, without
	// sending anything anywhere.
	MonitorMic *bool `json:"monitorMic,omitempty"`
	// TestSound is a counter. Every time it changes, the panel plays a
	// short tone through its speaker.
	//
	// A counter rather than a flag, because a flag that stays set is
	// played once and then never again, and the settings page cannot
	// tell that the button did nothing.
	TestSound *int `json:"testSound,omitempty"`
}
