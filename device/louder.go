package main

import (
	"math"
	"sync/atomic"
)

// Louder speech.
//
// Speech that has been brought up to a level still sounds quieter than
// Amazon's, which compresses it: the quiet parts of words are lifted and
// the loud ones held down, so the whole reply sits nearer the top.
//
// Louder is that compressor, for what goes to the speaker only: replies
// and announcements. Not the music, not what is sent for transcription,
// not what goes into a call.
//
// It runs ahead of the speaker, so the echo canceller's reference — which
// is made from what the speaker was sent — includes it, and it ends in the
// same soft limiter as the rest of the speech, so a loud syllable
// compresses rather than cracks.
const (
	// louderThreshold is where compression starts, as a fraction of full
	// scale (-16.5 dBFS, on the level followed, which is the average of
	// the waveform's size): the loud parts of normalized speech are over
	// it and held down, the quiet ones under it and lifted by the whole
	// makeup.
	louderThreshold = 0.15
	// louderRatio is how much a level over the threshold is held down:
	// 3 dB over comes out 1 dB over.
	louderRatio = 3.0
	// louderMakeup is the gain added after, +8 dB: the quiet parts of
	// words come up by that much and the loud ones by less, about +3 to
	// +5 dB on average for normalized speech.
	louderMakeup = 2.5
	// louderAttack and louderRelease are how fast the level follows a
	// word starting and ending.
	louderAttack  = 0.005
	louderRelease = 0.12
)

// speechKnee is where the peak limiter starts to compress, as a fraction
// of full scale.
const speechKnee = 0.8

// louderSpeechOn says whether the replies are compressed.
//
// It is read by the reply path, which is on another goroutine, and it is
// written when the switch is turned, so it is an atomic.
var louderSpeechOn atomic.Bool

var (
	louderAttackK  = math.Exp(-1 / (louderAttack * voiceRate))
	louderReleaseK = math.Exp(-1 / (louderRelease * voiceRate))
)

// softLimit brings a sample of any level within full scale: linear to the
// knee, then the remaining headroom is approached asymptotically, so a
// loud passage compresses rather than cracks.
func softLimit(v float64) int16 {
	const knee = speechKnee * 32767
	const headroom = 32767 - knee
	a := math.Abs(v)
	if a > knee {
		over := a - knee
		a = knee + headroom*over/(over+headroom)
		if v < 0 {
			v = -a
		} else {
			v = a
		}
	}
	return int16(v)
}

// louder compresses speech chunk by chunk.
//
// One is kept for a whole reply, so the level it follows runs on across
// the chunks. The zero value is ready.
type louder struct {
	env float64
}

// process returns the samples in compressed, made up and soft-limited, at
// the voice rate.
func (l *louder) process(in []int16) []int16 {
	out := make([]int16, len(in))
	for i, s := range in {
		x := float64(s) / 32768
		a := math.Abs(x)
		k := louderReleaseK
		if a > l.env {
			k = louderAttackK
		}
		l.env = k*l.env + (1-k)*a
		g := louderMakeup
		if l.env > louderThreshold {
			g *= math.Pow(l.env/louderThreshold, 1/louderRatio-1)
		}
		out[i] = softLimit(x * g * 32768)
	}
	return out
}

// forSpeaker is the speech as the speaker should play it: compressed when
// the Louder speech switch is on, as it is otherwise.
//
// The compressor may be nil for a whole clip.
func forSpeaker(in []int16, l *louder) []int16 {
	if !louderSpeechOn.Load() {
		return in
	}
	if l == nil {
		l = &louder{}
	}
	return l.process(in)
}
