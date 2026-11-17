package main

import (
	"math"
	"testing"
)

func rms(samples []int16) float64 {
	if len(samples) == 0 {
		return 0
	}
	var sum float64
	for _, s := range samples {
		v := float64(s) / 32768
		sum += v * v
	}
	return math.Sqrt(sum / float64(len(samples)))
}

func peak(samples []int16) float64 {
	var high float64
	for _, s := range samples {
		a := math.Abs(float64(s) / 32768)
		if a > high {
			high = a
		}
	}
	return high
}

func TestTheLouderSpeechLiftsTheQuietParts(t *testing.T) {

	const samples = voiceRate
	word := make([]int16, samples)
	for i := range word {

		gain := 0.05
		if i > samples/10 {
			gain = 0.5
		}
		word[i] = int16(gain * math.Sin(float64(i)*2*math.Pi*440/voiceRate) * 32767)
	}

	comp := &louder{}
	out := comp.process(word)

	quietIn := rms(word[:samples/10])
	quietOut := rms(out[:samples/10])
	if quietOut <= quietIn {
		t.Errorf("the quiet part did not come up: %v went in and %v came out", quietIn, quietOut)
	}

	loudIn := rms(word[samples/10:])
	loudOut := rms(out[samples/10:])
	if loudOut <= 0 {
		t.Fatalf("the loud part went quiet: %v went in and %v came out", loudIn, loudOut)
	}
	quietLift := quietOut / quietIn
	loudLift := loudOut / loudIn
	if loudLift >= quietLift {
		t.Errorf("the loud part was lifted as much as the quiet one: %v against %v", loudLift, quietLift)
	}
}

func TestTheLouderSpeechDoesNotCrack(t *testing.T) {
	word := make([]int16, voiceRate)
	for i := range word {

		word[i] = int16(math.Sin(float64(i)*2*math.Pi*440/voiceRate) * 32767)
	}

	comp := &louder{}
	out := comp.process(word)

	if high := peak(out); high > 1.0 {
		t.Errorf("the audio went over full scale: %v", high)
	}
}

func TestTheLouderSpeechRunsOnAcrossTheChunks(t *testing.T) {

	const chunk = voiceRate / 10
	word := make([]int16, chunk*2)
	for i := range word {
		word[i] = int16(0.2 * math.Sin(float64(i)*2*math.Pi*440/voiceRate) * 32767)
	}

	whole := (&louder{}).process(word)

	comp := &louder{}
	first := comp.process(word[:chunk])
	second := comp.process(word[chunk:])

	secondWhole := rms(whole[chunk:])
	secondSplit := rms(second)
	if secondSplit > secondWhole*1.5 {
		t.Errorf("the second chunk was lifted too much: %v whole against %v split", secondWhole, secondSplit)
	}
	if len(first) != chunk || len(second) != chunk {
		t.Fatalf("the chunks came out the wrong size: %d and %d", len(first), len(second))
	}
}

func TestTheLouderSpeechIsOffByDefault(t *testing.T) {
	louderSpeechOn.Store(false)

	word := make([]int16, 1000)
	for i := range word {
		word[i] = int16(0.1 * math.Sin(float64(i)*2*math.Pi*440/voiceRate) * 32767)
	}

	out := forSpeaker(word, nil)
	for i := range word {
		if out[i] != word[i] {
			t.Fatalf("the audio was changed with the switch off, at %d", i)
		}
	}
}
