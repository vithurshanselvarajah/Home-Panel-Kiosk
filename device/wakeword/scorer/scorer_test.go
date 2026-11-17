package scorer

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/vithurshanselvarajah/Home-Panel-Kiosk/device/wakeword"
)

type fakeInferer struct {
	mu    sync.Mutex
	score float32

	delay time.Duration

	failEmbed bool
	calls     int
}

type fakeEngine struct {
	inf *fakeInferer
	det *wakeword.Detector
}

func newFakeEngine(inf *fakeInferer) *fakeEngine {
	return &fakeEngine{inf: inf, det: wakeword.New(inf)}
}

func (e *fakeEngine) Push(samples []int16) (float32, bool, error) {
	if _, err := e.det.Push(samples); err != nil {
		return 0, false, err
	}
	if !e.det.Ready() {
		return 0, false, nil
	}
	score, err := e.det.Score()
	if err != nil {
		return 0, false, err
	}
	return score, true, nil
}

func (e *fakeEngine) Reset() { e.det.Reset() }

func (f *fakeInferer) Melspec(samples []float32) ([]float32, int, error) {

	frames := len(samples)/160 - 3
	if frames < 0 {
		frames = 0
	}
	return make([]float32, frames*wakeword.MelBins), frames, nil
}

func (f *fakeInferer) Embed(window []float32) ([]float32, error) {
	f.mu.Lock()
	f.calls++
	d, fail := f.delay, f.failEmbed
	f.mu.Unlock()
	if fail {
		return nil, errors.New("synthetic embed failure")
	}
	if d > 0 {
		time.Sleep(d)
	}
	return make([]float32, wakeword.FeatDim), nil
}

func (f *fakeInferer) Classify(feats []float32) (float32, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.score, nil
}

func (f *fakeInferer) set(score float32, delay time.Duration) {
	f.mu.Lock()
	f.score, f.delay = score, delay
	f.mu.Unlock()
}

func frame() []int16 { return make([]int16, wakeword.ChunkSamples) }

func pushAll(t *testing.T, s *Scorer, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		before := s.processed()
		s.Push(frame())
		waitFor(t, "a frame to be scored", func() bool { return s.processed() > before })
	}
	if st := s.peek(); st.Drops != 0 {
		t.Fatalf("warm-up dropped %d frames — the test is not measuring what it thinks", st.Drops)
	}
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestPushNeverBlocks(t *testing.T) {
	inf := &fakeInferer{}
	inf.set(0, 200*time.Millisecond)
	s := NewScorer(newFakeEngine(inf), 0.5, nil)
	defer s.Close()

	start := time.Now()
	for i := 0; i < queueFrames*10; i++ {
		s.Push(frame())
	}
	elapsed := time.Since(start)

	if elapsed > time.Second {
		t.Fatalf("pushing %d frames took %v — Push is blocking on inference",
			queueFrames*10, elapsed)
	}
	st := s.Drain()
	if st.Drops == 0 {
		t.Error("expected drops when the scorer cannot keep up")
	}
	if st.Drops+st.Frames == 0 {
		t.Error("no frames accounted for at all")
	}
}

func TestDropsAreCountedNotSilent(t *testing.T) {
	inf := &fakeInferer{}
	inf.set(0, 50*time.Millisecond)
	s := NewScorer(newFakeEngine(inf), 0.5, nil)
	defer s.Close()

	const pushed = 40
	for i := 0; i < pushed; i++ {
		s.Push(frame())
	}

	waitFor(t, "scorer to finish the queued frames", func() bool {
		return s.processed()+s.peek().Drops >= pushed
	})
	st := s.Drain()
	if got := st.Frames + st.Drops; got != pushed {
		t.Errorf("accounted for %d frames (%d scored + %d dropped), pushed %d",
			got, st.Frames, st.Drops, pushed)
	}
}

func TestCrossingFiresOncePerUtterance(t *testing.T) {
	var (
		mu      sync.Mutex
		crosses []float32
	)
	inf := &fakeInferer{}
	s := NewScorer(newFakeEngine(inf), 0.5, func(score, _ float32, at time.Time) {
		mu.Lock()
		crosses = append(crosses, score)
		mu.Unlock()
	})
	defer s.Close()

	inf.set(0.0, 0)
	pushAll(t, s, wakeword.FeatWindow)
	waitFor(t, "detector to become ready", func() bool { return s.Ready() })

	inf.set(0.9, 0)
	pushAll(t, s, 12)
	waitFor(t, "a crossing to fire", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(crosses) >= 1
	})

	time.Sleep(50 * time.Millisecond)

	mu.Lock()
	n := len(crosses)
	mu.Unlock()
	if n != 1 {
		t.Errorf("got %d crossings for one sustained utterance, want 1", n)
	}
	if st := s.Drain(); st.Crossings != 1 {
		t.Errorf("stats recorded %d crossings, want 1", st.Crossings)
	}
}

func TestBelowThresholdDoesNotCross(t *testing.T) {
	var fired int
	var mu sync.Mutex
	inf := &fakeInferer{}
	s := NewScorer(newFakeEngine(inf), 0.5, func(float32, float32, time.Time) {
		mu.Lock()
		fired++
		mu.Unlock()
	})
	defer s.Close()

	inf.set(0.42, 0)
	pushAll(t, s, wakeword.FeatWindow+5)

	mu.Lock()
	got := fired
	mu.Unlock()
	if got != 0 {
		t.Errorf("%d crossings fired below threshold", got)
	}
	st := s.Drain()
	if st.Crossings != 0 {
		t.Errorf("recorded %d crossings below threshold", st.Crossings)
	}
	if st.MaxScore != 0.42 {
		t.Errorf("MaxScore %v, want 0.42 — a missed wake needs the near-miss score", st.MaxScore)
	}
}

func TestInferenceErrorsDoNotStopScoring(t *testing.T) {
	inf := &fakeInferer{failEmbed: true}
	s := NewScorer(newFakeEngine(inf), 0.5, nil)
	defer s.Close()

	for i := 0; i < 5; i++ {
		before := s.processed()
		s.Push(frame())
		waitFor(t, "a frame to be accounted for", func() bool { return s.processed() > before })
	}
	waitFor(t, "errors to be recorded", func() bool { return s.peek().Errors >= 5 })

	inf.mu.Lock()
	inf.failEmbed = false
	inf.mu.Unlock()
	st := s.Drain()
	if st.Errors < 5 || st.LastErr == "" {
		t.Errorf("errors not recorded: %+v", st)
	}

	pushAll(t, s, wakeword.FeatWindow+2)
	if st := s.peek(); st.Frames == 0 || st.Errors != 0 {
		t.Errorf("scoring did not resume cleanly after errors: %+v", st)
	}
}

func TestDrainZeroesCounters(t *testing.T) {
	inf := &fakeInferer{}
	inf.set(0.3, 0)
	s := NewScorer(newFakeEngine(inf), 0.5, nil)
	defer s.Close()

	pushAll(t, s, wakeword.FeatWindow+1)
	if first := s.Drain(); first.Frames == 0 {
		t.Fatal("first Drain returned no frames")
	}
	if second := s.Drain(); second.Frames != 0 || second.MaxScore != 0 {
		t.Errorf("second Drain returned %+v, want zeroed", second)
	}
}

func TestResetClearsDetectorState(t *testing.T) {
	inf := &fakeInferer{}
	inf.set(0.1, 0)
	s := NewScorer(newFakeEngine(inf), 0.5, nil)
	defer s.Close()

	pushAll(t, s, wakeword.FeatWindow)
	waitFor(t, "detector to become ready", func() bool { return s.Ready() })

	s.Reset()

	s.Push(frame())
	waitFor(t, "reset to take effect", func() bool { return !s.Ready() })
}

func TestCloseIsIdempotent(t *testing.T) {
	s := NewScorer(newFakeEngine(&fakeInferer{}), 0.5, nil)
	s.Close()
	s.Close()
}

func (s *Scorer) processed() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stats.Frames
}

func (s *Scorer) peek() Stats {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stats
}

func TestBargeThresholdAppliesOnlyWhileSpeaking(t *testing.T) {
	var speaking bool
	var mu sync.Mutex
	var crosses int

	inf := &fakeInferer{}
	s := NewScorer(newFakeEngine(inf), 0.5, func(float32, float32, time.Time) {
		mu.Lock()
		crosses++
		mu.Unlock()
	})
	defer s.Close()
	s.SetBargeThreshold(0.10, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return speaking
	})

	inf.set(0.2, 0)
	pushAll(t, s, wakeword.FeatWindow+2)
	mu.Lock()
	got := crosses
	mu.Unlock()
	if got != 0 {
		t.Errorf("crossed at 0.2 with the speaker idle (bar should be 0.5)")
	}
	if thr := s.peek().Threshold; thr != 0.5 {
		t.Errorf("reported threshold %v while idle, want 0.5", thr)
	}

	mu.Lock()
	speaking = true
	mu.Unlock()
	s.Drain()
	pushAll(t, s, 3)
	waitFor(t, "a barge crossing", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return crosses >= 1
	})
	if thr := s.peek().Threshold; thr != 0.10 {
		t.Errorf("reported threshold %v while speaking, want 0.10", thr)
	}
}

func TestBargeThresholdIgnoredWhenUnset(t *testing.T) {
	inf := &fakeInferer{}
	var fired int
	var mu sync.Mutex
	s := NewScorer(newFakeEngine(inf), 0.5, func(float32, float32, time.Time) {
		mu.Lock()
		fired++
		mu.Unlock()
	})
	defer s.Close()

	s.SetBargeThreshold(0, func() bool { return true })
	inf.set(0.2, 0)
	pushAll(t, s, wakeword.FeatWindow+2)
	mu.Lock()
	defer mu.Unlock()
	if fired != 0 {
		t.Errorf("crossed at 0.2 with no barge threshold set")
	}
}

func TestBargeThresholdNeverRaisesTheBar(t *testing.T) {
	inf := &fakeInferer{}
	var fired int
	var mu sync.Mutex
	s := NewScorer(newFakeEngine(inf), 0.3, func(float32, float32, time.Time) {
		mu.Lock()
		fired++
		mu.Unlock()
	})
	defer s.Close()
	s.SetBargeThreshold(0.9, func() bool { return true })
	inf.set(0.4, 0)
	pushAll(t, s, wakeword.FeatWindow+2)
	waitFor(t, "a crossing at the normal threshold", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return fired >= 1
	})
}

func TestDrainReportsAndResetsTheTimingMaxima(t *testing.T) {

	s := NewScorer(newFakeEngine(&fakeInferer{}), 0.5, nil)
	defer s.Close()

	s.maxInferMs.Store(37)
	s.maxGapMs.Store(812)

	got := s.Drain()
	if got.MaxInferMs != 37 {
		t.Fatalf("MaxInferMs = %d, want 37", got.MaxInferMs)
	}
	if got.MaxGapMs != 812 {
		t.Fatalf("MaxGapMs = %d, want 812", got.MaxGapMs)
	}

	again := s.Drain()
	if again.MaxInferMs != 0 || again.MaxGapMs != 0 {
		t.Fatalf("maxima must reset per window, got infer=%d gap=%d",
			again.MaxInferMs, again.MaxGapMs)
	}
}

func TestTheFirstFrameRecordsNoProducerGap(t *testing.T) {

	s := NewScorer(newFakeEngine(&fakeInferer{}), 0.5, nil)
	defer s.Close()

	s.Push(make([]int16, 1280))
	if got := s.maxGapMs.Load(); got != 0 {
		t.Fatalf("first frame must not record a gap, got %dms", got)
	}
}

func TestTheProducerGapIsMeasuredForBothEntryPoints(t *testing.T) {

	s := NewScorer(newFakeEngine(&fakeInferer{}), 0.5, nil)
	defer s.Close()

	s.Push(make([]int16, 1280))
	time.Sleep(25 * time.Millisecond)
	s.PushBytes(make([]byte, 2560))

	if got := s.maxGapMs.Load(); got < 20 {
		t.Fatalf("gap across Push -> PushBytes should be ~25ms, got %dms", got)
	}
}

func TestCrossingReportsTheThresholdItCleared(t *testing.T) {
	var speaking bool
	var mu sync.Mutex
	var got []float32

	inf := &fakeInferer{}
	s := NewScorer(newFakeEngine(inf), 0.5, func(_, threshold float32, _ time.Time) {
		mu.Lock()
		got = append(got, threshold)
		mu.Unlock()
	})
	defer s.Close()
	s.SetBargeThreshold(0.10, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return speaking
	})

	inf.set(0.9, 0)
	pushAll(t, s, wakeword.FeatWindow+2)
	waitFor(t, "an idle crossing", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(got) >= 1
	})
	mu.Lock()
	if got[0] != 0.5 {
		t.Errorf("idle crossing reported threshold %v, want 0.5", got[0])
	}

	speaking = true
	mu.Unlock()
	inf.set(0.2, 0)

	time.Sleep(DefaultRefractory + 50*time.Millisecond)
	pushAll(t, s, 3)
	waitFor(t, "a barge crossing", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(got) >= 2
	})
	mu.Lock()
	defer mu.Unlock()
	if got[1] != 0.10 {
		t.Errorf("barge crossing reported threshold %v, want 0.10 — the bar it "+
			"actually cleared, not the nominal one", got[1])
	}
}

func TestCloseDuringPushDoesNotPanic(t *testing.T) {
	for i := 0; i < 50; i++ {
		s := NewScorer(newFakeEngine(&fakeInferer{score: 0.1}), 0.5, func(float32, float32, time.Time) {})

		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				s.Push(make([]int16, 1280))
			}
		}()

		time.Sleep(time.Duration(i%5) * time.Millisecond)
		s.Close()
		wg.Wait()

		s.Close()
	}
}

func TestBargeBarNeedsTwoConsecutiveFrames(t *testing.T) {
	var mu sync.Mutex
	var crosses int
	inf := &fakeInferer{}
	s := NewScorer(newFakeEngine(inf), 0.5, func(float32, float32, time.Time) {
		mu.Lock()
		crosses++
		mu.Unlock()
	})
	defer s.Close()
	s.SetBargeThreshold(0.10, func() bool { return true })

	inf.set(0.0, 0)
	pushAll(t, s, wakeword.FeatWindow+1)

	for i := 0; i < 6; i++ {
		inf.set(0.2, 0)
		pushAll(t, s, 1)
		inf.set(0.0, 0)
		pushAll(t, s, 1)
	}
	mu.Lock()
	got := crosses
	mu.Unlock()
	if got != 0 {
		t.Fatalf("crossed %d times on isolated frames at the barge bar", got)
	}
	inf.set(0.2, 0)
	pushAll(t, s, 2)
	waitFor(t, "a two-frame barge crossing", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return crosses == 1
	})
}

func TestNormalBarStillFiresOnOneFrame(t *testing.T) {
	var mu sync.Mutex
	var crosses int
	inf := &fakeInferer{}
	s := NewScorer(newFakeEngine(inf), 0.5, func(float32, float32, time.Time) {
		mu.Lock()
		crosses++
		mu.Unlock()
	})
	defer s.Close()
	inf.set(0.0, 0)
	pushAll(t, s, wakeword.FeatWindow+1)
	inf.set(0.9, 0)
	pushAll(t, s, 1)
	waitFor(t, "a one-frame crossing", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return crosses == 1
	})
}

func TestNormalBarMinFramesNeedsConsecutiveFrames(t *testing.T) {
	var mu sync.Mutex
	var crosses int
	inf := &fakeInferer{}
	s := NewScorer(newFakeEngine(inf), 0.5, func(float32, float32, time.Time) {
		mu.Lock()
		crosses++
		mu.Unlock()
	})
	defer s.Close()
	s.SetMinFrames(2)

	inf.set(0.0, 0)
	pushAll(t, s, wakeword.FeatWindow+1)

	for i := 0; i < 6; i++ {
		inf.set(0.9, 0)
		pushAll(t, s, 1)
		inf.set(0.0, 0)
		pushAll(t, s, 1)
	}
	if got := s.peek().Crossings; got != 0 {
		t.Fatalf("crossed %d times on isolated frames with minFrames=2", got)
	}
	inf.set(0.9, 0)
	pushAll(t, s, 2)
	waitFor(t, "a two-frame crossing", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return crosses == 1
	})
}

func TestNormalBarMinFramesCountsToN(t *testing.T) {
	var mu sync.Mutex
	var crosses int
	inf := &fakeInferer{}
	s := NewScorer(newFakeEngine(inf), 0.5, func(float32, float32, time.Time) {
		mu.Lock()
		crosses++
		mu.Unlock()
	})
	defer s.Close()
	s.SetMinFrames(3)

	inf.set(0.0, 0)
	pushAll(t, s, wakeword.FeatWindow+1)
	inf.set(0.9, 0)
	pushAll(t, s, 2)
	if got := s.peek().Crossings; got != 0 {
		t.Fatalf("crossed after 2 frames with minFrames=3")
	}
	pushAll(t, s, 1)
	waitFor(t, "a three-frame crossing", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return crosses == 1
	})
}

func TestNormalBarMinFramesDatesFromTheFirstFrame(t *testing.T) {
	got := make(chan time.Time, 1)
	inf := &fakeInferer{}
	s := NewScorer(newFakeEngine(inf), 0.5, func(_, _ float32, at time.Time) { got <- at })
	defer s.Close()
	s.SetMinFrames(2)
	inf.set(0.0, 0)
	pushAll(t, s, wakeword.FeatWindow+1)
	inf.set(0.9, 0)
	first := time.Now().Add(-500 * time.Millisecond)
	b := make([]byte, wakeword.ChunkSamples*2)
	s.PushBytesAt(b, first)
	s.PushBytesAt(b, first.Add(80*time.Millisecond))
	select {
	case at := <-got:
		if !at.Equal(first) {
			t.Fatalf("crossing dated %v after the first frame, want 0", at.Sub(first))
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no crossing")
	}
}

func TestMinFramesIsCapped(t *testing.T) {
	var mu sync.Mutex
	var crosses int
	inf := &fakeInferer{}
	s := NewScorer(newFakeEngine(inf), 0.5, func(float32, float32, time.Time) {
		mu.Lock()
		crosses++
		mu.Unlock()
	})
	defer s.Close()
	s.SetMinFrames(50)

	inf.set(0.0, 0)
	pushAll(t, s, wakeword.FeatWindow+1)
	inf.set(0.9, 0)
	pushAll(t, s, MaxMinFrames)
	waitFor(t, "a crossing at the cap", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return crosses == 1
	})
}

func TestMinFramesDoesNotChangeTheBargeBar(t *testing.T) {
	var mu sync.Mutex
	var crosses int
	inf := &fakeInferer{}
	s := NewScorer(newFakeEngine(inf), 0.5, func(float32, float32, time.Time) {
		mu.Lock()
		crosses++
		mu.Unlock()
	})
	defer s.Close()
	s.SetMinFrames(3)
	s.SetBargeThreshold(0.10, func() bool { return true })

	inf.set(0.0, 0)
	pushAll(t, s, wakeword.FeatWindow+1)
	inf.set(0.2, 0)
	pushAll(t, s, 2)
	waitFor(t, "a two-frame barge crossing", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return crosses == 1
	})
}

func TestCrossingReportsCaptureTime(t *testing.T) {
	got := make(chan time.Time, 1)
	inf := &fakeInferer{}
	s := NewScorer(newFakeEngine(inf), 0.5, func(_, _ float32, at time.Time) { got <- at })
	defer s.Close()
	inf.set(0.0, 0)
	pushAll(t, s, wakeword.FeatWindow+1)
	inf.set(0.9, 0)
	captured := time.Now().Add(-500 * time.Millisecond)
	b := make([]byte, wakeword.ChunkSamples*2)
	s.PushBytesAt(b, captured)
	select {
	case at := <-got:
		if !at.Equal(captured) {
			t.Fatalf("callback got %v, want the capture time %v", at, captured)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no crossing")
	}
}

func TestBargeCrossingIsDatedFromItsFirstFrame(t *testing.T) {
	got := make(chan time.Time, 1)
	inf := &fakeInferer{}
	s := NewScorer(newFakeEngine(inf), 0.5, func(_, _ float32, at time.Time) { got <- at })
	defer s.Close()
	s.SetBargeThreshold(0.25, func() bool { return true })
	inf.set(0.0, 0)
	pushAll(t, s, wakeword.FeatWindow+1)
	inf.set(0.3, 0)
	first := time.Now().Add(-500 * time.Millisecond)
	b := make([]byte, wakeword.ChunkSamples*2)
	s.PushBytesAt(b, first)
	s.PushBytesAt(b, first.Add(80*time.Millisecond))
	select {
	case at := <-got:
		if !at.Equal(first) {
			t.Fatalf("barge crossing dated %v, want the first frame's %v", at.Sub(first), time.Duration(0))
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no crossing")
	}
}

func TestBargeWindowReportsThePeakAtTheBargeBar(t *testing.T) {
	inf := &fakeInferer{}
	s := NewScorer(newFakeEngine(inf), 0.5, nil)
	defer s.Close()
	playing := false
	var mu sync.Mutex
	s.SetBargeThreshold(0.25, func() bool { mu.Lock(); defer mu.Unlock(); return playing })
	inf.set(0.0, 0)
	pushAll(t, s, wakeword.FeatWindow+1)
	inf.set(0.9, 0)
	pushAll(t, s, 1)
	if _, _, n := s.TakeBargeWindow(); n != 0 {
		t.Fatalf("counted %d barge frames while idle", n)
	}
	mu.Lock()
	playing = true
	mu.Unlock()
	inf.set(0.18, 0)
	pushAll(t, s, 3)
	peak, bar, n := s.TakeBargeWindow()
	if n != 3 || bar != 0.25 || peak < 0.17 || peak > 0.19 {
		t.Fatalf("window = peak %.3f bar %.2f frames %d", peak, bar, n)
	}
	if _, _, n := s.TakeBargeWindow(); n != 0 {
		t.Fatal("window not reset")
	}
}
