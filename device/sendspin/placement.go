package sendspin

import (
	"github.com/vithurshanselvarajah/Home-Panel-Kiosk/audio/speaker"
	"log"
	"math"
	"sync"
	"time"
)

const holdMax = 60 * outRate

type heldChunk struct {
	at int64

	samples []int16
}

const maxDelayMs = 1000

const reanchorFrames = 60 * outRate

const (
	driftGain  = 0.02
	driftBand  = outRate / 500
	snapBand   = outRate / 100
	maxSnap    = 2 * outRate
	duckFrames = outRate * 60 / 1000
)

type placement struct {
	mu    sync.Mutex
	ready bool

	held bool

	suspended bool

	joined bool

	attached bool

	base   int64
	played uint64
	pcm    []int16

	anchored bool
	frame    int64
	at       int64

	delay int64

	drift     float64
	corrected int64

	nextReport uint64

	filter *timeFilter

	attach func()
	detach func()

	backgrounds *speaker.Arbiter

	position func() (uint64, time.Duration)

	started func() bool

	latency func() time.Duration

	gain, target float32

	chunks  int
	samples int64

	pending []heldChunk

	rms float64

	playedPeriods int
	playedFrames  int64
	late          int64
	dropped       int64
}

func (o *placement) SetAttach(attach, detach func()) {
	o.mu.Lock()
	o.attach, o.detach = attach, detach
	o.mu.Unlock()
}

func newPlacement(filter *timeFilter) *placement {
	return &placement{filter: filter, gain: 1, target: 1}
}

func (o *placement) use(filter *timeFilter) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.filter = filter
}

func (o *placement) open() {
	o.mu.Lock()
	o.ready = true
	o.mu.Unlock()
}

func (o *placement) close() {
	o.mu.Lock()
	o.ready = false

	o.held = false
	o.reset()
	detach := o.detach
	backgrounds := o.backgrounds
	joined := o.joined

	attached := o.attached
	o.joined = false
	o.attached = false
	o.mu.Unlock()

	if joined && backgrounds != nil {
		backgrounds.Gave(o)
	}
	if attached && detach != nil {
		detach()
	}
}

func (o *placement) openAgain() {
	o.mu.Lock()
	first := !o.joined
	o.joined = true
	o.ready = true
	o.reset()
	attach := o.attach
	backgrounds := o.backgrounds
	o.attached = true
	o.mu.Unlock()

	if first && backgrounds != nil {

		backgrounds.Took(o)
	}
	if attach != nil {
		attach()
	}
}

func (o *placement) write(at int64, samples []int16) {
	o.mu.Lock()
	if !o.ready {
		o.dropped++
		o.mu.Unlock()
		return
	}

	if o.anchored {
		if drift := (at - o.at) * outRate / 1e6; drift > reanchorFrames || drift < -reanchorFrames {
			o.anchored = false
			o.drift = 0
			o.pcm = o.pcm[:0]
			o.base = 0
		}
	}

	o.frameFor(at)
	if !o.anchored {

		o.pending = append(o.pending, heldChunk{at: at, samples: samples})
		o.mu.Unlock()
		return
	}

	held := o.pending
	o.pending = nil
	if len(held) > 0 {
		o.anchorAt(held[0].at)
	}
	o.mu.Unlock()
	for _, c := range held {
		o.write(c.at, c.samples)
	}
	o.mu.Lock()
	frame := o.frameFor(at) + o.delay
	if frame > int64(o.played) && frame-int64(o.played) > holdMax {
		o.dropped++
		o.mu.Unlock()
		return
	}

	span := int64(len(samples) / outChannels)
	if frame+span <= int64(o.played) {
		o.late++
		o.mu.Unlock()
		return
	}
	if frame < int64(o.played) {
		samples = samples[(int64(o.played)-frame)*outChannels:]
		frame = int64(o.played)
	}

	o.chunks++
	o.samples += int64(len(samples) / outChannels)

	if len(o.pcm) == 0 {
		o.base = frame
	}
	if frame < o.base {
		o.pcm = append(make([]int16, (o.base-frame)*outChannels), o.pcm...)
		o.base = frame
	}

	off := int(frame-o.base) * outChannels
	if need := off + len(samples); need > len(o.pcm) {
		o.pcm = append(o.pcm, make([]int16, need-len(o.pcm))...)
	}
	copy(o.pcm[off:], samples)
	o.mu.Unlock()
}

func (o *placement) frameFor(at int64) int64 {
	if o.anchored {
		return o.frame + (at-o.at)*outRate/1e6
	}

	if o.started != nil && !o.started() {
		return o.frame
	}

	return o.anchorAt(at)
}

func (o *placement) anchorAt(at int64) int64 {
	written, latency := o.position()
	ahead := time.Until(o.serverToLocal(at)) - latency

	o.frame = int64(written) + int64(max(0, ahead.Seconds()*outRate))
	o.at = at
	o.anchored = true
	if o.frame < 0 {
		o.frame = 0
	}
	o.played = uint64(o.frame)

	log.Printf("[sendspin] anchored frame=%d ahead_ms=%d latency_ms=%d lead_ms=%d written=%d queued_ms=%d",
		o.frame, ahead.Milliseconds(), latency.Milliseconds(),
		(at-o.filter.serverTime(nowUs()))/1000, written, len(o.pcm)/outChannels*1000/outRate)
	return o.frame
}

func (o *placement) serverToLocal(at int64) time.Time {
	if o.filter == nil {
		return time.Now()
	}
	return time.UnixMicro(o.filter.clientTime(at))
}

func (o *placement) Render(from uint64, buf []int16) {
	o.mu.Lock()
	defer o.mu.Unlock()

	if from > o.played {
		o.played = from
	}

	at := int64(from)
	if at > o.base && len(o.pcm) > 0 {
		if gone := int((at - o.base) * outChannels); gone >= len(o.pcm) {
			o.pcm = o.pcm[:0]
		} else {
			o.pcm = append(o.pcm[:0], o.pcm[gone:]...)
		}
	}
	if at > o.base {
		o.base = at
	}

	if o.held || o.suspended || !o.ready || at < o.base {
		return
	}
	o.correct(from)
	n := min(len(buf), len(o.pcm))
	if n > 0 {
		o.playedPeriods++
		o.playedFrames += int64(n / outChannels)

		var sum float64
		for i := 0; i < n; i++ {
			v := float64(o.pcm[i])
			sum += v * v
		}
		o.rms = math.Sqrt(sum / float64(n))
	}
	if o.gain == o.target {
		gain := o.gain
		for i := range n {
			buf[i] = scaleSample(o.pcm[i], gain)
		}
		return
	}

	const step = 1.0 / duckFrames
	for f := 0; f+outChannels <= n; f += outChannels {
		if o.gain < o.target {
			o.gain = min(o.gain+step, o.target)
		} else {
			o.gain = max(o.gain-step, o.target)
		}
		gain := o.gain
		for c := range outChannels {
			buf[f+c] = scaleSample(o.pcm[f+c], gain)
		}
	}
}

func (o *placement) correct(from uint64) {
	if !o.anchored || len(o.pcm) < 2*outChannels {
		return
	}
	if o.filter == nil {
		return
	}

	if !o.filter.synchronized() {
		return
	}
	now := o.filter.serverTime(nowUs())

	want := o.frame + (now-o.at)*outRate/1e6
	tail := o.tail()

	off := float64(int64(from) - tail - want)
	o.drift += (off - o.drift) * driftGain

	if from >= o.nextReport {
		o.nextReport = from + outRate
		log.Printf("[sendspin] correction off_ms=%d drift_ms=%d from=%d want=%d anchor=%d corrected=%d tail_ms=%d queued_ms=%d",
			int64(off)*1000/outRate, int64(o.drift)*1000/outRate, from, want, o.frame, o.corrected,
			o.tail()*1000/outRate, len(o.pcm)/outChannels*1000/outRate)
	}

	if off > maxSnap || off < -maxSnap {
		o.anchored = false
		o.drift = 0
		return
	}

	switch {
	case o.drift > snapBand:
		n := int(o.drift)
		o.pcm = append(make([]int16, n*outChannels), o.pcm...)
		o.frame += int64(n)
		o.corrected += int64(n)
		o.drift = 0
	case o.drift < -snapBand:
		n := min(int(-o.drift), len(o.pcm)/outChannels-1)
		o.pcm = append(o.pcm[:0], o.pcm[n*outChannels:]...)

		o.frame -= int64(n)
		o.corrected -= int64(n)
		o.drift = 0
	case o.drift > driftBand:

		o.pcm = append(o.pcm, o.pcm[:outChannels]...)
		copy(o.pcm[outChannels:], o.pcm[:len(o.pcm)-outChannels])
		o.frame++
		o.drift--
		o.corrected++
	case o.drift < -driftBand:

		n := copy(o.pcm, o.pcm[outChannels:])
		o.pcm = o.pcm[:n]
		o.frame--
		o.drift++
		o.corrected--
	}
}

func (o *placement) tail() int64 {
	if o.latency == nil {
		return 0
	}
	return int64(o.latency() * outRate / time.Second)
}

func (o *placement) setDelay(ms int) {

	ms = min(max(ms, 0), maxDelayMs)
	n := int64(ms) * outRate / 1000

	o.mu.Lock()
	defer o.mu.Unlock()
	d := n - o.delay
	o.delay = n
	if d == 0 || len(o.pcm) == 0 {
		return
	}
	if d > 0 {
		o.pcm = append(make([]int16, int(d)*outChannels), o.pcm...)
		return
	}
	drop := min(int(-d), len(o.pcm)/outChannels)
	o.pcm = append(o.pcm[:0], o.pcm[drop*outChannels:]...)
}

func (o *placement) setGain(volume int, muted bool) {
	gain := float32(1)
	if muted {
		gain = 0
	} else if volume > 0 && volume < 100 {
		gain = float32(math.Pow(float64(volume)/100, 1.5))
	}
	o.mu.Lock()
	o.gain, o.target = gain, gain
	o.mu.Unlock()
}

func (o *placement) Duck(db int) {
	gain := float32(1)
	if db < 0 {
		gain = float32(math.Pow(10, float64(db)/20))
	}
	o.mu.Lock()
	o.target = gain
	o.mu.Unlock()
}

func (o *placement) Suspend() {
	o.mu.Lock()
	o.suspended = true
	o.mu.Unlock()
}

func (o *placement) Resume() {
	o.mu.Lock()
	o.suspended = false
	o.mu.Unlock()
}

func (o *placement) Requeue() {}

func (o *placement) Ready() bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.ready
}

func (o *placement) Held() bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.held || o.suspended
}

func (o *placement) hold(paused bool) {
	o.mu.Lock()
	o.held = paused
	if paused {
		o.pcm = o.pcm[:0]
		o.base = 0
		o.anchored = false
		o.drift = 0
	}
	o.mu.Unlock()
}

func (o *placement) flush() {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.reset()
}

func (o *placement) stats() playerStats {
	o.mu.Lock()
	defer o.mu.Unlock()
	st := playerStats{
		LateDrops: int(o.late),
		Backwards: int(o.dropped),
		Snaps:     int(o.corrected),
		Chunks:    o.chunks,
		Samples:   o.samples,
		Played:    o.playedPeriods,
		Frames:    o.playedFrames,
		RMS:       o.rms,
	}
	if o.filter != nil {
		st.OffsetUs = pyRound(o.filter.cur.offset)
		if o.filter.cur.useDrift {
			st.DriftPpm = o.filter.cur.drift * 1e6
		}
	}
	return st
}

func (o *placement) resetStats() {
	o.mu.Lock()
	o.late, o.dropped = 0, 0
	o.corrected = 0
	o.mu.Unlock()
}

func (o *placement) reset() {
	o.pcm = o.pcm[:0]
	o.base = 0
	o.anchored = false
	o.drift = 0
}

func (o *placement) queuedMs() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return len(o.pcm) / outChannels * 1000 / outRate
}

func scaleSample(s int16, gain float32) int16 {
	if gain == 1 {
		return s
	}
	return int16(max(math.MinInt16, min(float64(s)*float64(gain), math.MaxInt16)))
}
