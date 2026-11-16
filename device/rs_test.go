package main

import (
	"testing"

	"github.com/vithurshanselvarajah/Home-Panel-Kiosk/audio"
)

func TestVoiceRateChain(t *testing.T) {
	// One second of 16 kHz mono audio.
	in := make([]byte, 16000*2)
	r := audio.NewResampler(16000, 48000)
	out := r.Convert(in)
	tail := r.Flush()
	t.Logf("input %d bytes (16000 samples at 16kHz)", len(in))
	t.Logf("after our resample: %d bytes = %d samples at 48kHz = %.2f seconds", len(out)+len(tail),
		(len(out)+len(tail))/2, float64(len(out)+len(tail))/2/48000)
}
