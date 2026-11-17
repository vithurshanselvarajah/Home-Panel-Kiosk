package main

import (
	"fmt"
	"math"
	"math/cmplx"

	"github.com/vithurshanselvarajah/Home-Panel-Kiosk/audio"
	"github.com/vithurshanselvarajah/Home-Panel-Kiosk/audio/mic/framework"
	"github.com/vithurshanselvarajah/Home-Panel-Kiosk/audio/speaker"
)

func main() {
	const srcRate = 16000
	const n = srcRate // 1.000 s
	in := make([]int16, n)
	for i := range in {
		in[i] = int16(10000 * math.Sin(2*math.Pi*400*float64(i)/srcRate))
	}
	raw := make([]byte, 2*n)
	for i, v := range in {
		raw[2*i] = byte(uint16(v))
		raw[2*i+1] = byte(uint16(v) >> 8)
	}

	// --- stage 1: resampleVoice 16k -> 48k (HEAD) ---
	convA := audio.NewResampler(srcRate, 48000).Convert(raw)
	fmt.Printf("stage1 resampleVoice(16k->48k): %d bytes = %d mono samples, rms=%.0f\n",
		len(convA), len(convA)/2, rmsBytes(convA))
	fmt.Printf("stage1 spectrum peak: %.1f Hz\n", peakHzBytes(convA, 48000))

	// --- stage 2: monoSamples -> stereo int16 ---
	stereoA := monoSamples(convA)
	fmt.Printf("stage2 monoSamples: %d int16 = %d frames\n", len(stereoA), len(stereoA)/2)

	// --- stage 3: PlayVoice ---
	vr, _ := speaker.NewResampler(framework.ResampleSinc)
	outA := vr.Run(stereoA, make([]int16, 0, len(stereoA)*speaker.VoiceUpsample*speaker.Channels))
	fmt.Printf("stage3 PlayVoice: %d int16 = %d frames = %.0f ms @48k, rms=%.0f\n",
		len(outA), len(outA)/2, float64(len(outA)/2)/48.0, rmsInt16(outA))
	fmt.Printf("stage3 spectrum peak: %.1f Hz\n", peakHzInt16(outA, 48000))

	// --- the drain-rate check: how much card time per 10ms of wall clock? ---
	// device loop takes periodBytes=960 bytes (10ms @48k mono) per 10ms tick.
	const periodBytes = 48000 / 100 * 2
	tick := convA[:periodBytes]
	tickStereo := monoSamples(tick)
	tickOut := vr.Run(tickStereo, make([]int16, 0, len(tickStereo)*speaker.VoiceUpsample*speaker.Channels))
	fmt.Printf("\nDRAIN: one 10ms tick hands %d bytes -> PlayVoice -> %d frames = %.0f ms of card time\n",
		periodBytes, len(tickOut)/2, float64(len(tickOut)/2)/48.0)
}

func rmsBytes(pcm []byte) float64 {
	var s float64
	for i := 0; i+1 < len(pcm); i += 2 {
		v := float64(int16(uint16(pcm[i]) | uint16(pcm[i+1])<<8))
		s += v * v
	}
	return math.Sqrt(s / float64(len(pcm)/2))
}

func rmsInt16(pcm []int16) float64 {
	var s float64
	for _, v := range pcm {
		s += float64(v) * float64(v)
	}
	return math.Sqrt(s / float64(len(pcm)))
}

func monoSamples(pcm []byte) []int16 {
	out := make([]int16, 0, len(pcm))
	for i := 0; i+1 < len(pcm); i += 2 {
		v := int16(uint16(pcm[i]) | uint16(pcm[i+1])<<8)
		out = append(out, v, v)
	}
	return out
}

func peakHzBytes(pcm []byte, rate float64) float64 {
	var x []float64
	for i := 0; i+1 < len(pcm); i += 2 {
		x = append(x, float64(int16(uint16(pcm[i])|uint16(pcm[i+1])<<8)))
	}
	return peak(x, rate)
}

func peakHzInt16(pcm []int16, rate float64) float64 {
	var x []float64
	for i := 0; i+1 < len(pcm); i += 2 {
		x = append(x, float64(pcm[i]))
	}
	return peak(x, rate)
}

func peak(x []float64, rate float64) float64 {
	best, bestMag := 0.0, 0.0
	for f := 30.0; f < 4000; f += 0.5 {
		m := goertzel(x, f, rate)
		if m > bestMag {
			bestMag, best = m, f
		}
	}
	return best
}

func goertzel(x []float64, freq, rate float64) float64 {
	w := 2 * math.Pi * freq / rate
	c := 2 * math.Cos(w)
	var s1, s2 float64
	for _, v := range x {
		s0 := v + c*s1 - s2
		s2, s1 = s1, s0
	}
	return math.Sqrt(s1*s1+s2*s2-c*s1*s2) / float64(len(x))
}

var _ = cmplx.Abs
