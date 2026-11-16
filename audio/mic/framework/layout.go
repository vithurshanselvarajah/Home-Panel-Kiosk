// Package framework holds the small pieces of the TECHO5 daemon framework that the ported audio
// packages used.
//
// The ported code is TECHO5's, byte for byte, apart from the import paths. These shims stand in for
// the daemon's own configuration, board description and service plumbing, so that the audio code
// itself stays as it was written and as it was tested.
//
// Ported from TECHO5 (github.com/HuskerMinion/techo5), MIT licensed. Copyright (c) 2026 Yuri
// Gelfand.
package framework

import (
	"os"
	"strings"
	"sync"
)

// Board is the board this build is for.
//
// It is read from the device tree, the way TECHO5 reads it, so that a
// machine that is not one of these boards reads as none of them: the
// board's values are chosen from it, and a board that is not there has
// none of them.
var Board = readBoard()

// readBoard is the board the device tree says this is.
//
// The compatible string names the board, the way the kernel's own device
// tree names it: "amzn,athena" and its private variant "amzn,athena-pvt".
// The model string is not used: on this board it says only the chip.
//
// The compatible property is a list of strings, the first of which is the
// most specific.
func readBoard() string {
	b, err := os.ReadFile("/proc/device-tree/compatible")
	if err != nil {
		return ""
	}
	for _, name := range strings.Split(strings.TrimRight(string(b), "\x00"), "\x00") {
		name = strings.TrimSpace(name)
		if name == "amzn,athena" || strings.HasPrefix(name, "amzn,athena-") {
			return "athena"
		}
	}
	return ""
}

// SoundCard is the sound card's id, empty when the kernel names it by number.
func SoundCard() string { return "" }

// StateDir is where the device program's runtime state lives.
const StateDir = "/run/home-panel"

// SpeakerGainMax is how far the speaker's gain may be raised, in dB.
//
// It is deliberately small: a unit was reported far too loud at the top of the range.
const SpeakerGainMax = 6

// VolumeSteps is how many steps the volume is divided into.
//
// The speaker's volume curve has one entry per step, and it is the curve
// that makes the top of the dial room-loud rather than painful.
const VolumeSteps = 30

// DefaultVolume is the level the speaker starts at, in the speaker's steps.
const DefaultVolume = VolumeSteps / 2

// DefaultResampling is the resampling used when nothing says otherwise.
const DefaultResampling = ResampleSinc

// DefaultDuckDB is how far the music is ducked under a reply, in dB.
const DefaultDuckDB = -15

// DefaultOnTurn is what a turn does to the music that is playing.
const DefaultOnTurn = OnTurnDuck

// DefaultASP says whether the driver's tuning applies, which is what the vendor's firmware does.
//
// It is the speaker's own tuning, read off the vendor partition at run time and applied in software.
const DefaultASP = true

// Resampling is how the audio is converted between rates.
type Resampling string

// The resampling modes.
const (
	// ResampleSinc interpolates through a low pass at the input's Nyquist, which is the correct
	// answer and what the device uses unless told otherwise.
	ResampleSinc Resampling = "sinc"

	// ResampleLinear draws a straight line between input samples. It attenuates the images rather
	// than removing them, for a fraction of the work.
	ResampleLinear Resampling = "linear"

	// ResampleHold repeats each input sample, which is what the device did before any of this and
	// leaves the images in full.
	ResampleHold Resampling = "hold"
)

// Label is the name of the resampling, for the settings.
func (r Resampling) Label() string {
	switch r {
	case ResampleSinc:
		return "Band limited"
	case ResampleLinear:
		return "Linear"
	case ResampleHold:
		return "Repeat samples"
	}
	return string(r)
}

// Tone is the shape of the sounds the speaker makes.
type Tone int

// The tones.
const (
	ToneNone Tone = iota
	ToneHA
	ToneDing
	ToneChirp
	ToneRise
)

// DefaultTone is the tone used when nothing says otherwise.
const DefaultTone = ToneHA

// Athena says whether this is an Echo Show 8 2nd generation.
//
// The board is read from the device tree, so the board's values are settled
// when the package is loaded: the Show 5 and the Show 8 read the card
// differently.
func Athena() bool { return Board == "athena" }

// AthenaMainline says whether the board is running a mainline kernel rather than Amazon's.
//
// Amazon's own kernel for this board is 4.4; the mainline kernel this
// project builds is 6.x. Anything that is not 4.x is taken as mainline.
//
// The Amazon kernel's microphone format is sign-extended 24-bit in the low
// three bytes, and the mainline kernel's is left-aligned in a 32-bit word,
// so the two are told apart by more than the version.
func AthenaMainline() bool {
	return Board == "athena" && mainlineRelease(kernelRelease())
}

// mainlineRelease is whether the kernel is the mainline one rather than
// Amazon's.
func mainlineRelease(release string) bool {
	release = strings.TrimSpace(release)
	return release != "" && !strings.HasPrefix(release, "4.")
}

// kernelRelease is the running kernel's release string.
func kernelRelease() string {
	b, _ := os.ReadFile("/proc/sys/kernel/osrelease")
	return string(b)
}

// Show is the Echo Show 5 2nd generation.
func Show() bool { return false }

// Show8 is the Echo Show 8, of either generation.
//
// The Show 8 1st generation is the Crown and the 2nd is athena, and both
// are the Show 8: they have the same array of microphones and the same
// reference beside them, so they are read the same way.
//
// This matters more than it looks. The array's shape decides how the
// capture is laid out: how many channels the frame has, how many of them
// are microphones, and where the playback reference starts. On the Show 8
// that is six channels, four microphones and the reference last. Taken as
// the Show 5's four channels and two microphones, the reference is written
// over the third and fourth microphones, and the two microphones that
// survive are the only ones heard.
func Show8() bool { return Crown() || Athena() }

// Checkers is the Echo Show 5 1st generation.
func Checkers() bool { return false }

// Crown is the Echo Show 8 1st generation.
func Crown() bool { return false }

// Skylight is the Skylight Calendar.
func Skylight() bool { return false }

// Radar is the Echo 2nd generation.
func Radar() bool { return false }

// Cupcake is the Echo Input.
func Cupcake() bool { return false }

var (
	mu     sync.RWMutex
	micCfg = MicSettings{
		Gain:        DefaultMicGain,
		Sensitivity: DefaultSensitivity,
		Mixing:      DefaultMixing,
		Cancel:      DefaultCancel,
		Leveling:    DefaultLeveling,
		Denoise:     DefaultDenoise,
	}
	speakerCfg = SpeakerSettings{
		Volume:     DefaultVolume,
		Resampling: DefaultResampling,
		ASP:        DefaultASP,
		Tone:       DefaultTone,
	}
	mediaCfg = MediaSettings{DuckDB: DefaultDuckDB, OnTurn: DefaultOnTurn}
)

// DefaultMicGain is the analog gain the microphones are set at, in dB.
//
// It is the vendor's own value for this board: twenty decibels, which is what the leveler's floors
// are worked out against.
const DefaultMicGain = 20

// DefaultSensitivity is how far over the room's own floor counts as something happening, in dB.
const DefaultSensitivity = 8

// DefaultLeveling says whether the leveler is on.
const DefaultLeveling = true

// DefaultCancel says whether the echo canceller is on.
const DefaultCancel = true

// DefaultDenoise says whether the noise estimator is on.
const DefaultDenoise = false

// DefaultMuted says whether the software mute is on.
const DefaultMuted = false

// Mixing is how the microphones are combined into the one the echo canceller reads.
type Mixing int

// The ways the microphones can be combined.
const (
	MixDelaySum Mixing = iota
	MixBeamformer
	MixCenter
	MixAll
)

// DefaultMixing is the combination used when nothing says otherwise.
const DefaultMixing = MixAll

// DefaultThreshold is the wake word's threshold.
const DefaultThreshold = 0.85

// ASPMode says which signal processing runs.
type ASPMode string

// The signal processing modes.
const (
	ASPBuiltin  ASPMode = "builtin"
	ASPExternal ASPMode = "external"
)

// MicSettings is the microphone's settings.
type MicSettings struct {
	Gain        int
	Sensitivity int
	Mixing      Mixing
	Cancel      bool
	Leveling    bool
	Denoise     bool
	Muted       bool
	Threshold   float64
}

// SpeakerSettings is the speaker's settings.
type SpeakerSettings struct {
	Volume        int
	GainDB        int
	Resampling    Resampling
	ASP           bool
	Stereo        bool
	ClassicSounds bool
	Tone          Tone
	// Bass and Treble are the listener's own shelves, in decibels.
	Bass   float64
	Treble float64
}

// MediaSettings is what happens to a track that is playing when the device is spoken to.
type MediaSettings struct {
	// OnTurn is whether music carries on quietly under a turn or stops for it.
	OnTurn OnTurn
	// DuckDB is how far down, in dB, when it carries on. Negative.
	DuckDB int
	// DuckOnNearMiss lowers a playing track for a few seconds when the wake word was nearly heard and
	// did not fire.
	DuckOnNearMiss bool
}

// OnTurn is what a turn does to music.
type OnTurn string

// The ways a turn can behave towards music.
const (
	OnTurnDuck  OnTurn = "duck"
	OnTurnPause OnTurn = "pause"
	OnTurnNone  OnTurn = "none"
)

// Use points the settings at another place, and starts them afresh.
//
// The settings are held in memory here rather than in a file, so the file is
// not read; but the settings are reset to their defaults, which is what
// reading a file that has never been written would give.
func Use(path string) {
	mu.Lock()
	statePath = path
	micCfg = MicSettings{
		Gain:        DefaultMicGain,
		Sensitivity: DefaultSensitivity,
		Mixing:      DefaultMixing,
		Cancel:      true,
		Leveling:    true,
		Denoise:     true,
	}
	speakerCfg = SpeakerSettings{Volume: DefaultVolume, Resampling: DefaultResampling, ASP: DefaultASP, Tone: DefaultTone}
	mediaCfg = MediaSettings{DuckDB: DefaultDuckDB, OnTurn: DefaultOnTurn}
	mu.Unlock()
}

// statePath is where the settings would be written.
var statePath string

// Get returns the current settings.
func Get() Settings {
	mu.RLock()
	defer mu.RUnlock()
	return Settings{
		Microphone: micCfg,
		Speaker:    speakerCfg,
		Media:      mediaCfg,
	}
}

// Settings is the whole of the configuration the audio code reads.
type Settings struct {
	Microphone MicSettings
	Speaker    SpeakerSettings
	Media      MediaSettings
}

// Set writes a setting.
func Set() Writer { return Writer{} }

// Writer writes the configuration.
type Writer struct{}

// Microphone returns the microphone's settings to write.
func (w Writer) Microphone() MicrophoneWriter { return MicrophoneWriter{} }

// Speaker returns the speaker's settings to write.
func (w Writer) Speaker() SpeakerWriter { return SpeakerWriter{} }

// Media returns the media player's settings to write.
func (w Writer) Media() MediaWriter { return MediaWriter{} }

// MicrophoneWriter writes the microphone's settings.
type MicrophoneWriter struct{}

// Gain sets the microphones' analog gain, in dB.
func (MicrophoneWriter) Gain(db int) error {
	mu.Lock()
	micCfg.Gain = db
	mu.Unlock()
	return nil
}

// Sensitivity sets how far over the floor counts as something happening, in dB.
func (MicrophoneWriter) Sensitivity(db int) error {
	mu.Lock()
	micCfg.Sensitivity = db
	mu.Unlock()
	return nil
}

// Mixing sets how the microphones are combined.
func (MicrophoneWriter) Mixing(m Mixing) error {
	mu.Lock()
	micCfg.Mixing = m
	mu.Unlock()
	return nil
}

// Cancel turns echo cancellation on or off.
func (MicrophoneWriter) Cancel(on bool) error {
	mu.Lock()
	micCfg.Cancel = on
	mu.Unlock()
	return nil
}

// Leveling turns the leveler on or off.
func (MicrophoneWriter) Leveling(on bool) error {
	mu.Lock()
	micCfg.Leveling = on
	mu.Unlock()
	return nil
}

// Denoise turns the noise estimator on or off.
func (MicrophoneWriter) Denoise(on bool) error {
	mu.Lock()
	micCfg.Denoise = on
	mu.Unlock()
	return nil
}

// Muted turns the software mute on or off.
func (MicrophoneWriter) Muted(on bool) error {
	mu.Lock()
	micCfg.Muted = on
	mu.Unlock()
	return nil
}

// Threshold sets the wake word's threshold.
func (MicrophoneWriter) Threshold(v float64) error {
	mu.Lock()
	micCfg.Threshold = v
	mu.Unlock()
	return nil
}

// DefaultWakeThreshold is the wake word's threshold when nothing says otherwise.
const DefaultWakeThreshold = 0.85

// SpeakerWriter writes the speaker's settings.
type SpeakerWriter struct{}

// Volume sets the level, in the speaker's own steps.
func (SpeakerWriter) Volume(step int) error {
	mu.Lock()
	speakerCfg.Volume = step
	mu.Unlock()
	return nil
}

// Gain sets the speaker's gain over the vendor's, in dB.
func (SpeakerWriter) Gain(db int) error {
	mu.Lock()
	speakerCfg.GainDB = db
	mu.Unlock()
	return nil
}

// Resampling sets how the audio is converted between rates.
func (SpeakerWriter) Resampling(r Resampling) error {
	mu.Lock()
	speakerCfg.Resampling = r
	mu.Unlock()
	return nil
}

// ASP turns the vendor's tuning on or off.
func (SpeakerWriter) ASP(on bool) error {
	mu.Lock()
	speakerCfg.ASP = on
	mu.Unlock()
	return nil
}

// Stereo turns stereo on or off.
func (SpeakerWriter) Stereo(on bool) error {
	mu.Lock()
	speakerCfg.Stereo = on
	mu.Unlock()
	return nil
}

// ClassicSounds turns the classic sounds on or off.
func (SpeakerWriter) ClassicSounds(on bool) error {
	mu.Lock()
	speakerCfg.ClassicSounds = on
	mu.Unlock()
	return nil
}

// Tone sets the shape of the sounds the speaker makes.
func (SpeakerWriter) Tone(t Tone) error {
	mu.Lock()
	speakerCfg.Tone = t
	mu.Unlock()
	return nil
}

// Bass sets the bass shelf, in decibels.
func (SpeakerWriter) Bass(db float64) error {
	mu.Lock()
	speakerCfg.Bass = db
	mu.Unlock()
	return nil
}

// Treble sets the treble shelf, in decibels.
func (SpeakerWriter) Treble(db float64) error {
	mu.Lock()
	speakerCfg.Treble = db
	mu.Unlock()
	return nil
}

// MediaWriter writes the media player's settings.
type MediaWriter struct{}

// OnTurn sets what happens to the music while a reply is being given.
func (MediaWriter) OnTurn(v OnTurn) error {
	mu.Lock()
	mediaCfg.OnTurn = v
	mu.Unlock()
	return nil
}

// DuckDB sets how far the music is ducked under a reply, in dB.
func (MediaWriter) DuckDB(db int) error {
	mu.Lock()
	mediaCfg.DuckDB = db
	mu.Unlock()
	return nil
}

// DuckOnNearMiss lowers the music when the wake word was nearly heard.
func (MediaWriter) DuckOnNearMiss(on bool) error {
	mu.Lock()
	mediaCfg.DuckOnNearMiss = on
	mu.Unlock()
	return nil
}

// Current returns the settings the audio code reads.
//
// The audio code reads them through Get, which the ported packages call as config.Get(); the shim
// keeps them in one place so the device program can set them.
func Current() (MicSettings, SpeakerSettings, MediaSettings) {
	mu.RLock()
	defer mu.RUnlock()
	return micCfg, speakerCfg, mediaCfg
}

// SetMicGain sets the microphones' analog gain, in dB.
func SetMicGain(db int) {
	mu.Lock()
	micCfg.Gain = db
	mu.Unlock()
}

// SetMicSensitivity sets how far over the floor counts as something happening, in dB.
func SetMicSensitivity(db int) {
	mu.Lock()
	micCfg.Sensitivity = db
	mu.Unlock()
}

// SetMicMixing sets how the microphones are combined.
func SetMicMixing(m Mixing) {
	mu.Lock()
	micCfg.Mixing = m
	mu.Unlock()
}

// SetMicCancel turns echo cancellation on or off.
func SetMicCancel(on bool) {
	mu.Lock()
	micCfg.Cancel = on
	mu.Unlock()
}

// SetMicLeveling turns the leveler on or off.
func SetMicLeveling(on bool) {
	mu.Lock()
	micCfg.Leveling = on
	mu.Unlock()
}

// SetMicDenoise turns the noise estimator on or off.
func SetMicDenoise(on bool) {
	mu.Lock()
	micCfg.Denoise = on
	mu.Unlock()
}

// SetSpeakerResampling sets how the audio is converted between rates.
func SetSpeakerResampling(r Resampling) {
	mu.Lock()
	speakerCfg.Resampling = r
	mu.Unlock()
}

// SetSpeakerTone sets the shape of the sounds the speaker makes.
func SetSpeakerTone(t Tone) {
	mu.Lock()
	speakerCfg.Tone = t
	mu.Unlock()
}

// SetSpeakerGain sets the speaker's gain over the vendor's, in dB.
func SetSpeakerGain(db int) {
	mu.Lock()
	speakerCfg.GainDB = db
	mu.Unlock()
}

// SetSpeakerASP turns the vendor's tuning on or off.
func SetSpeakerASP(on bool) {
	mu.Lock()
	speakerCfg.ASP = on
	mu.Unlock()
}

// SetSpeakerVolume sets the level, in the speaker's own steps.
func SetSpeakerVolume(step int) {
	mu.Lock()
	speakerCfg.Volume = step
	mu.Unlock()
}

// SetSpeakerStereo turns stereo on or off.
func SetSpeakerStereo(on bool) {
	mu.Lock()
	speakerCfg.Stereo = on
	mu.Unlock()
}

// SetMediaDuckDB sets how far the music is ducked under a reply, in dB.
func SetMediaDuckDB(db int) {
	mu.Lock()
	mediaCfg.DuckDB = db
	mu.Unlock()
}
