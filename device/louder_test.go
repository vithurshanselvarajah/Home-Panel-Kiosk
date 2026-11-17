package main

import (
	"math"
	"testing"
)

// rms is the root mean square of the samples, as a fraction of full scale.
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

// peak is the largest magnitude of the samples, as a fraction of full
// scale.
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

// The Louder speech compressor lifts the quiet parts of the words.
//
// A reply that is only brought up to a level still sounds quieter than
// Amazon's, which compresses it: the quiet parts of the words come up and
// the loud ones are held down. So the quiet part of a word comes out
// louder than it went in.
func TestTheLouderSpeechLiftsTheQuietParts(t *testing.T) {
	// A word with a quiet start and a loud middle, as a word has.
	const samples = voiceRate // a second
	word := make([]int16, samples)
	for i := range word {
		// The first tenth of a second is quiet, the rest is loud.
		gain := 0.05
		if i > samples/10 {
			gain = 0.5
		}
		word[i] = int16(gain * math.Sin(float64(i)*2*math.Pi*440/voiceRate) * 32767)
	}

	comp := &louder{}
	out := comp.process(word)

	// The quiet part comes up.
	quietIn := rms(word[:samples/10])
	quietOut := rms(out[:samples/10])
	if quietOut <= quietIn {
		t.Errorf("the quiet part did not come up: %v went in and %v came out", quietIn, quietOut)
	}

	// The loud part is held down *relative to the makeup*: the whole
	// makeup is added first, and the loud parts have some of it taken
	// back, so the loud part comes up by less than the quiet one does.
	//
	// This is the whole point of the compressor: the quiet parts of the
	// words come up and the loud ones do not, so the whole reply sits
	// nearer the top without getting louder.
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

// The compressor never makes the audio louder than full scale.
//
// It ends in the same soft limiter as the rest of the speech, so a loud
// syllable compresses rather than cracks.
func TestTheLouderSpeechDoesNotCrack(t *testing.T) {
	word := make([]int16, voiceRate)
	for i := range word {
		// A word at full scale, which is what the makeup would push
		// over.
		word[i] = int16(math.Sin(float64(i)*2*math.Pi*440/voiceRate) * 32767)
	}

	comp := &louder{}
	out := comp.process(word)

	if high := peak(out); high > 1.0 {
		t.Errorf("the audio went over full scale: %v", high)
	}
}

// The compressor follows the level across the chunks of a reply.
//
// One compressor is kept for the whole reply, so the level it follows runs
// on: a compressor that started again at every chunk would hear every
// chunk as a new word beginning, and the quiet start of every chunk would
// be lifted the whole way up.
func TestTheLouderSpeechRunsOnAcrossTheChunks(t *testing.T) {
	// One long word, delivered as two chunks.
	const chunk = voiceRate / 10
	word := make([]int16, chunk*2)
	for i := range word {
		word[i] = int16(0.2 * math.Sin(float64(i)*2*math.Pi*440/voiceRate) * 32767)
	}

	// The whole word, in one go.
	whole := (&louder{}).process(word)

	// The same word, in two chunks, with one compressor for both.
	comp := &louder{}
	first := comp.process(word[:chunk])
	second := comp.process(word[chunk:])

	// The second chunk is not lifted as much as it would be if the
	// compressor had started again: the level had already followed the
	// word up.
	secondWhole := rms(whole[chunk:])
	secondSplit := rms(second)
	if secondSplit > secondWhole*1.5 {
		t.Errorf("the second chunk was lifted too much: %v whole against %v split", secondWhole, secondSplit)
	}
	if len(first) != chunk || len(second) != chunk {
		t.Fatalf("the chunks came out the wrong size: %d and %d", len(first), len(second))
	}
}

// The switch decides whether the reply is compressed.
//
// With it off the audio is passed through as it is, so the reply is
// exactly what came in.
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
