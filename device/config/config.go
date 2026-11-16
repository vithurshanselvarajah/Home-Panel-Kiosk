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
	// Volume is the current level.
	//
	// It is the level the speaker was last at, and it is what the
	// speaker starts at on the next launch. There is no startup volume
	// of its own: a level that was chosen is kept.
	Volume *int `json:"volume,omitempty"`
	// DuckDb is how far the music is turned down while a reply plays.
	//
	// It is in decibels and it is negative, so the music is quieter
	// under a reply than it would be rather than louder.
	DuckDb *float64 `json:"duckDb,omitempty"`

	// ── The speaker's own tuning ─────────────────────────────────────────
	//
	// These are the settings the audio stack itself has, and they are
	// what make the speaker sound the way the vendor's own firmware
	// makes it sound.
	//
	// SpeakerGainDb is how far the amplifier's gain is raised over the
	// vendor's own, in decibels. It is capped at six, because past that
	// a unit has been reported far too loud.
	SpeakerGainDb *int `json:"speakerGainDb,omitempty"`
	// SpeakerTuning turns the vendor's tuning on and off.
	//
	// With it on, the speaker plays the sound the vendor's firmware
	// gives it: the amplifier's own filter and limiter, read off the
	// device at run time. With it off, the speaker plays what it is
	// given.
	SpeakerTuning *bool `json:"speakerTuning,omitempty"`
	// SpeakerStereo plays the two drivers separately, which athena can
	// do and which keeps the amplifier's own processing of each.
	SpeakerStereo *bool `json:"speakerStereo,omitempty"`
	// SpeakerBass and SpeakerTreble are the listener's own shelves, in
	// decibels, at most six either way.
	SpeakerBass   *float64 `json:"speakerBassDb,omitempty"`
	SpeakerTreble *float64 `json:"speakerTrebleDb,omitempty"`
	// ClearerSpeech brings the tuning's low band down while a reply is
	// playing, so that words are heard over the music.
	ClearerSpeech *bool `json:"clearerSpeech,omitempty"`
	// LouderTop makes the top of the volume dial go hotter into the
	// tuning.
	LouderTop *bool `json:"louderTop,omitempty"`
	// SpeakerResampling is how the reply is stretched from the
	// controller's rate to the speaker's: "sinc", "linear" or "hold".
	SpeakerResampling *string `json:"speakerResampling,omitempty"`

	// ── The microphone's own settings ───────────────────────────────────
	// MicSensitivity is how far over the room's own noise counts as
	// something happening, in decibels.
	MicSensitivity *int `json:"micSensitivityDb,omitempty"`
	// MicLeveling turns the automatic gain control on and off.
	MicLeveling *bool `json:"micLeveling,omitempty"`
	// MicDenoise takes the steady noise out of the room.
	//
	// It is off by default: it removes the room's own hum as well as
	// the fan, and a room that sounds right without it is a room worth
	// hearing as it is.
	MicDenoise *bool `json:"micDenoise,omitempty"`
	// MicCancel turns the echo canceller on and off.
	//
	// It is on by default: with it off, the speaker's own voice comes
	// back through the microphones and the wake word hears the panel
	// talking to itself.
	MicCancel *bool `json:"micCancel,omitempty"`

	// AutoBrightness says whether the screen follows the room's light.
	//
	// With it on, the brightness setting is a ceiling and the room's
	// light decides how much of that ceiling the screen gets.
	AutoBrightness *bool `json:"autoBrightness,omitempty"`

	// ── Music ───────────────────────────────────────────────────────────
	// SendspinEnabled turns the music player on and off.
	SendspinEnabled *bool `json:"sendspinEnabled,omitempty"`
	// SendspinName is the name the player is known by in Music Assistant.
	SendspinName *string `json:"sendspinName,omitempty"`
	// SendspinUnpaired lets a server play without pairing. Off by
	// default: anyone on the network can claim to be a server, and
	// pairing is one paste of the device's token.
	SendspinUnpaired *bool `json:"sendspinUnpaired,omitempty"`
	// CameraEnabled retains the Home Assistant camera switch state.
	CameraEnabled *bool `json:"cameraEnabled,omitempty"`

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
