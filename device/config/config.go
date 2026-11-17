package config

type Config struct {
	MicGainDb *int `json:"micGainDb,omitempty"`

	VadThreshold *float64 `json:"vadThreshold,omitempty"`

	VadSpeechMs *int `json:"vadSpeechMs,omitempty"`

	VadSilenceMs *int `json:"vadSilenceMs,omitempty"`

	WakeWordEnabled *bool `json:"wakeWordEnabled,omitempty"`

	WakeWordModel string `json:"wakeWordModel,omitempty"`

	WakeThreshold *float64 `json:"wakeThreshold,omitempty"`

	WakeSound *bool `json:"wakeSound,omitempty"`

	BargeIn *bool `json:"bargeIn,omitempty"`

	Volume *int `json:"volume,omitempty"`

	DuckDb *float64 `json:"duckDb,omitempty"`

	SpeakerGainDb *int `json:"speakerGainDb,omitempty"`

	SpeakerTuning *bool `json:"speakerTuning,omitempty"`

	SpeakerStereo *bool `json:"speakerStereo,omitempty"`

	SpeakerBass   *float64 `json:"speakerBassDb,omitempty"`
	SpeakerTreble *float64 `json:"speakerTrebleDb,omitempty"`

	ClearerSpeech *bool `json:"clearerSpeech,omitempty"`

	LouderTop *bool `json:"louderTop,omitempty"`

	LouderSpeech *bool `json:"louderSpeech,omitempty"`

	SpeakerResampling *string `json:"speakerResampling,omitempty"`

	MicSensitivity *int `json:"micSensitivityDb,omitempty"`

	MicLeveling *bool `json:"micLeveling,omitempty"`

	MicDenoise *bool `json:"micDenoise,omitempty"`

	MicCancel *bool `json:"micCancel,omitempty"`

	AutoBrightness *bool `json:"autoBrightness,omitempty"`

	SendspinEnabled *bool `json:"sendspinEnabled,omitempty"`

	SendspinName *string `json:"sendspinName,omitempty"`

	SendspinUnpaired *bool `json:"sendspinUnpaired,omitempty"`

	CameraEnabled *bool `json:"cameraEnabled,omitempty"`

	AudioInput *string `json:"audioInput,omitempty"`

	AudioOutput *string `json:"audioOutput,omitempty"`

	MonitorMic *bool `json:"monitorMic,omitempty"`

	TestSound *int `json:"testSound,omitempty"`
}
