package denoise

import (
	"math"
	"testing"
)

// A bin with no energy at all, once a noise estimate exists, drives the gain to +Inf and the estimate
// to Inf*0. That is NaN, it is kept in clean, and clean feeds the next frame's prior — so one frame of
// digital silence deafens the filter for good. The hardware mute and a suspended codec both produce
// exactly that frame.
func TestDigitalSilenceDoesNotDeafenTheFilter(t *testing.T) {
	f := New(16000)
	in := make([]float64, f.Frame())
	out := make([]float64, f.Hop())

	tone := func(k int) {
		for i := range in {
			in[i] = 8000 * math.Sin(2*math.Pi*440*float64(k*f.Hop()+i)/16000)
		}
	}

	for k := range 40 {
		tone(k)
		f.Push(in, out)
	}

	clear(in)
	f.Push(in, out)

	var poisoned int
	for _, v := range f.clean {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			poisoned++
		}
	}
	t.Logf("after one silent frame, %d of %d bins hold a non-finite estimate", poisoned, len(f.clean))

	var loudest int16
	for k := range 40 {
		tone(k)
		f.Push(in, out)
		for _, v := range out {
			if s := clampSample(v); s > loudest {
				loudest = s
			}
		}
	}
	if loudest == 0 {
		t.Fatalf("a full-scale tone now comes out as pure zeros: the filter is permanently deaf")
	}
	t.Logf("tone recovered at peak %d", loudest)
}

// A long mute, not just one silent frame: followed through the silence, the noise estimate decayed for
// as long as it lasted, so after minutes the room came back crushed and after an hour every bin was
// non-finite (a Show left muted, then deaf to its wake word until restarted). Whatever the silence's
// length, what follows must come out as it would after none.
func TestLongSilenceLeavesTheFilterAsItWas(t *testing.T) {
	run := func(silent int) (float64, int) {
		s := NewStream(16000)
		frame := make([]int16, 320)
		k := 0
		tone := func() {
			for i := range frame {
				frame[i] = int16(3000 * math.Sin(2*math.Pi*440*float64(k)/16000))
				k++
			}
		}
		for range 100 {
			tone()
			s.Apply(frame)
		}
		for range silent {
			clear(frame)
			s.Apply(frame)
		}
		var sum float64
		n := 0
		for j := range 200 {
			tone()
			s.Apply(frame)
			if j > 100 {
				for _, v := range frame {
					sum += float64(v) * float64(v)
					n++
				}
			}
		}
		bad := 0
		for i := range s.f.clean {
			if math.IsNaN(s.f.clean[i]) || math.IsInf(s.f.clean[i], 0) || math.IsNaN(s.f.noise[i]) || math.IsInf(s.f.noise[i], 0) {
				bad++
			}
		}
		return math.Sqrt(sum / float64(n)), bad
	}
	want, _ := run(1)
	got, bad := run(60000) // twenty minutes of 20 ms frames
	if bad > 0 {
		t.Fatalf("%d bins non-finite after the silence", bad)
	}
	if math.Abs(got-want) > 0.1*want+1 {
		t.Fatalf("after the silence the tone comes out at %.0f, against %.0f after none", got, want)
	}
}
