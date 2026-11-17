package main

import (
	"math"
	"sync/atomic"
)

const (
	louderThreshold = 0.15

	louderRatio = 3.0

	louderMakeup = 2.5

	louderAttack  = 0.005
	louderRelease = 0.12
)

const speechKnee = 0.8

var louderSpeechOn atomic.Bool

var (
	louderAttackK  = math.Exp(-1 / (louderAttack * voiceRate))
	louderReleaseK = math.Exp(-1 / (louderRelease * voiceRate))
)

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

type louder struct {
	env float64
}

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

func forSpeaker(in []int16, l *louder) []int16 {
	if !louderSpeechOn.Load() {
		return in
	}
	if l == nil {
		l = &louder{}
	}
	return l.process(in)
}
