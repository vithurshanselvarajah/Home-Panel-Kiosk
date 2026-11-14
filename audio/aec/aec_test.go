package aec

import (
	"math"
	"math/rand/v2"
	"testing"
)

// The echo canceller.
//
// What it has to be right about is that it removes the echo and leaves
// the room. A canceller that adapts on a silent reference fits itself to
// the room's noise, which is the one thing it must never learn, and one
// that keeps adapting through somebody talking is fitting the wrong
// signal.
//
// The tests below are the ones that matter, and they measure the
// cancellation from the signals rather than trusting the filter's own
// report.

const frame = 320

// echoed passes ref through h and returns it as a microphone signal
// would arrive.
func echoed(ref []int16, h []float64) []int16 {
	out := make([]int16, len(ref))
	for i := range ref {
		var v float64
		for k, c := range h {
			if i-k >= 0 {
				v += c * float64(ref[i-k])
			}
		}
		out[i] = int16(v)
	}
	return out
}

// noiseAt is n samples of noise at an amplitude.
func noiseAt(n int, amp float64, seed uint64) []int16 {
	out := make([]int16, n)
	r := rand.New(rand.NewPCG(seed, 7))
	for i := range out {
		out[i] = int16((r.Float64()*2 - 1) * amp)
	}
	return out
}

// erle is measured directly from the signals rather than trusting the
// Canceller's own report.
func erle(mic, out []int16) float64 {
	var d, e float64
	for i := range mic {
		d += float64(mic[i]) * float64(mic[i])
		e += float64(out[i]) * float64(out[i])
	}
	if e == 0 {
		return math.Inf(1)
	}
	return 10 * math.Log10(d/e)
}

// run feeds everything through in frames, and returns the output of the
// last part only, with the filter frozen for it. Scoring on data the
// filter is still adapting to would be in-sample.
func run(t *testing.T, c *Canceller, mic, ref []int16, holdOut int) (gotMic, gotOut []int16) {
	t.Helper()

	split := len(mic) - holdOut
	for i := 0; i < len(mic); i += frame {
		end := min(i+frame, len(mic))
		if i >= split {
			c.SetAdapting(false)
		}

		out, err := c.Process(mic[i:end], ref[i:end])
		if err != nil {
			t.Fatal(err)
		}
		if i >= split {
			gotOut = append(gotOut, out...)
			gotMic = append(gotMic, mic[i:end]...)
		}
	}
	return gotMic, gotOut
}

// A pure delay is the easiest path there is, and the filter has to reach
// past it: the delay here is longer than the hardware's ~30 samples.
func TestCancelsAPureDelay(t *testing.T) {
	c, err := New(Config{Taps: 128, Mu: 0.5})
	if err != nil {
		t.Fatal(err)
	}

	h := make([]float64, 40)
	h[30] = 0.5

	ref := noiseAt(48000, 8000, 1)
	mic := echoed(ref, h)

	gotMic, gotOut := run(t, c, mic, ref, 8000)
	if got := erle(gotMic, gotOut); got < 30 {
		t.Fatalf("erle %.1f dB on a pure delay, want at least 30", got)
	}
}

// A decaying random tail is the real shape: the measurement on the device
// showed the echo spread over tens of milliseconds rather than arriving as
// one reflection.
func TestCancelsADispersiveTail(t *testing.T) {
	c, err := New(Config{Taps: 512, Mu: 0.5})
	if err != nil {
		t.Fatal(err)
	}

	r := rand.New(rand.NewPCG(3, 9))
	h := make([]float64, 300)
	for k := 30; k < len(h); k++ {
		h[k] = (r.Float64()*2 - 1) * 0.4 * math.Exp(-float64(k-30)/80)
	}

	ref := noiseAt(160000, 8000, 2)
	mic := echoed(ref, h)

	gotMic, gotOut := run(t, c, mic, ref, 16000)
	if got := erle(gotMic, gotOut); got < 20 {
		t.Fatalf("erle %.1f dB on a dispersive tail, want at least 20", got)
	}
}

// The filter must not treat a talking room as an echo of silence. This is
// the failure that matters: adapting against a silent reference drives
// the coefficients anywhere at all.
func TestSilentReferenceDoesNotDiverge(t *testing.T) {
	c, err := New(Config{Taps: 256, Mu: 0.5})
	if err != nil {
		t.Fatal(err)
	}

	ref := make([]int16, 48000)
	mic := noiseAt(48000, 6000, 4)

	for i := 0; i < len(mic); i += frame {
		end := min(i+frame, len(mic))
		out, err := c.Process(mic[i:end], ref[i:end])
		if err != nil {
			t.Fatal(err)
		}
		for j := range out {
			if out[j] != mic[i+j] {
				t.Fatalf("output changed with no reference: mic %d became %d", mic[i+j], out[j])
			}
		}
	}

	for k, v := range c.w {
		if v != 0 {
			t.Fatalf("filter learned something from silence: w[%d] = %v", k, v)
		}
	}
}

// Near-end speech over the echo is what freezing exists for. A filter that
// keeps adapting through it is fitting the wrong signal, so it should end
// up worse than one that was told to stop.
func TestFreezingSurvivesDoubleTalk(t *testing.T) {
	h := make([]float64, 80)
	for k := 30; k < len(h); k++ {
		h[k] = 0.3 * math.Exp(-float64(k-30)/20)
	}

	ref := noiseAt(96000, 8000, 5)
	echo := echoed(ref, h)

	// Train both on echo alone.
	train := 64000
	var cs [2]*Canceller
	for i := range cs {
		c, err := New(Config{Taps: 256, Mu: 0.5})
		if err != nil {
			t.Fatal(err)
		}
		for j := 0; j < train; j += frame {
			if _, err := c.Process(echo[j:j+frame], ref[j:j+frame]); err != nil {
				t.Fatal(err)
			}
		}
		cs[i] = c
	}

	// Then put a loud voice on top, one still adapting and one frozen.
	voice := noiseAt(len(ref), 9000, 6)
	both := make([]int16, len(ref))
	for i := range both {
		both[i] = echo[i] + voice[i]
	}
	cs[1].SetAdapting(false)

	// Measure how well each still cancels the echo afterwards, on echo
	// alone.
	var got [2]float64
	for i, c := range cs {
		for j := train; j < len(both); j += frame {
			if _, err := c.Process(both[j:j+frame], ref[j:j+frame]); err != nil {
				t.Fatal(err)
			}
		}
		c.SetAdapting(false)

		var mics, outs []int16
		for j := 0; j < 16000; j += frame {
			out, err := c.Process(echo[j:j+frame], ref[j:j+frame])
			if err != nil {
				t.Fatal(err)
			}
			outs = append(outs, out...)
			mics = append(mics, echo[j:j+frame]...)
		}
		got[i] = erle(mics, outs)
	}

	if got[1] <= got[0] {
		t.Fatalf("freezing did not help through double talk: adapting %.1f dB, frozen %.1f dB", got[0], got[1])
	}
}

// A canceller that rejects a bad setup is better than one that does not:
// the taps and the step are the two numbers that decide whether it works.
func TestRejectsBadConfigAndLengths(t *testing.T) {
	if _, err := New(Config{Taps: 0, Mu: 0.5}); err == nil {
		t.Error("a canceller with no taps was accepted")
	}
	if _, err := New(Config{Taps: 128, Mu: 0}); err == nil {
		t.Error("a canceller that never adapts was accepted")
	}
	if _, err := New(Config{Taps: 128, Mu: 2}); err == nil {
		t.Error("a canceller with a step over 2 was accepted")
	}

	c, err := New(Config{Taps: 128, Mu: 0.5})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Process(make([]int16, frame), make([]int16, frame-1)); err == nil {
		t.Error("mismatched lengths were accepted")
	}
}
