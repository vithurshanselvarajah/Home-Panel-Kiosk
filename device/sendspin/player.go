package sendspin

import (
	"math"
	"sync"
	"time"
)

// epoch anchors the client clock. time.Time carries a monotonic reading, so
// Sub between two of them is immune to the wall clock being stepped — which
// the controller does to every Echo on connect (time_ms on the ack).
var epoch = time.Now()

func nowUs() int64 { return time.Since(epoch).Microseconds() }

func toUs(t time.Time) int64 { return t.Sub(epoch).Microseconds() }

// Sync correction, after the spec's suggested strategy: whole-frame drops and
// repeats, bit-exact everywhere else.
const (
	// deadbandFrames: errors under ~100µs are left alone.
	deadbandFrames = 5
	// maxStepFrames per period: 8 of 2048 is 0.39% speed, inside the spec's
	// 0.5% cap. The DAC's own crystal runs -0.056% (1.15 frames a period,
	// measured 2026-08-10), so this keeps up with room to spare.
	maxStepFrames = 8
	// snapUs: past this the error is a disturbance rather than drift, and
	// one-shot resync beats a second of audible speed change.
	snapUs = 5000
)

type chunk struct {
	ts  int64 // server µs of the first sample
	pcm []int16
}

// player holds decoded audio and hands it to the speaker at the right time.
//
// The speaker PULLS (Fill): it knows when the period it is building will
// reach the DAC, and only a pull at that moment can place a sample to the
// millisecond. The existing music plane is push-and-play-in-order, which is
// why Sendspin cannot simply feed it.
type player struct {
	mu      sync.Mutex
	q       []chunk
	off     int // samples consumed from q[0]
	playing bool

	filter  *timeFilter
	delayUs int64 // static_delay_ms, µs

	gainTarget, gain int32 // Q15

	st playerStats
}

// playerStats are counts since the stream started, for the status report.
type playerStats struct {
	Snaps          int `json:"snaps"`
	Corrections    int `json:"corrections"`
	Underruns      int `json:"underruns"`
	LateDrops      int `json:"lateDrops"`
	BufferOverruns int `json:"bufferOverruns"`
	LastErrorUs    int `json:"lastErrorUs"`
	// Chunks is how many chunks have arrived and Samples how many samples
	// they carried.
	Chunks  int   `json:"chunks"`
	Samples int64 `json:"samples"`
	// MaxChunk is the largest chunk that has arrived, in samples.
	MaxChunk int `json:"maxChunk"`
	// Backwards is how many times the server's timestamps went backwards.
	Backwards int `json:"backwards"`
	// NotDue is how many periods the player found the queue was not due
	// to play yet, and TooLate how many found it already overdue. A rising
	// NotDue with no audio played is a server sending far ahead.
	NotDue  int `json:"notDue"`
	TooLate int `json:"tooLate"`
	// Empty is how many periods found nothing queued at all.
	Empty int `json:"empty"`
	// Played is how many periods were filled and Frames how many frames
	// of audio they carried.
	Played int   `json:"played"`
	Frames int64 `json:"frames"`
	// LeadUs is how far ahead of the play position the head of the queue
	// is. A large lead means the server is sending far ahead of where the
	// audio is being played.
	LeadUs int64 `json:"leadUs"`
	// SpanUs is the time the queued audio covers, from head to tail.
	SpanUs int64 `json:"spanUs"`
	// HeadAgeUs is the server's now minus the head chunk's timestamp:
	// positive means the head is in the past, negative means the server
	// is stamping it in the future.
	HeadAgeUs int64 `json:"headAgeUs"`
	// TailAgeUs is the same for the newest chunk.
	TailAgeUs int64 `json:"tailAgeUs"`
	// OffsetUs is the time filter's offset and DriftPpm its drift.
	OffsetUs int64   `json:"offsetUs"`
	DriftPpm float64 `json:"driftPpm"`
}

func newPlayer(f *timeFilter) *player {
	return &player{filter: f, gainTarget: unity, gain: unity}
}

const unity int32 = 1 << 15

// maxBufferedSamples bounds memory and latency if the speaker stalls while
// the network keeps delivering audio.
//
// It is in client.go, beside the buffer capacity the server is told about:
// the two have to agree, because the server's cap must always bind before
// the player's, or the queue is thrown away and the music stops.

// volumeGain is the spec's perceptual curve: amplitude = (v/100)^1.5.
func volumeGain(volume int, muted bool) int32 {
	if muted || volume <= 0 {
		return 0
	}
	if volume >= 100 {
		return unity
	}
	return int32(math.Pow(float64(volume)/100, 1.5)*float64(unity) + 0.5)
}

func (p *player) setGain(volume int, muted bool) {
	p.mu.Lock()
	p.gainTarget = volumeGain(volume, muted)
	p.mu.Unlock()
}

func (p *player) setFilter(f *timeFilter) {
	p.mu.Lock()
	p.filter = f
	p.mu.Unlock()
}

func (p *player) setDelay(ms int) {
	p.mu.Lock()
	p.delayUs = int64(ms) * 1000
	p.mu.Unlock()
}

// push queues one decoded chunk. A chunk that starts before the one already
// at the tail is a server that went backwards; it is taken as a restart.
func (p *player) push(ts int64, pcm []int16) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(pcm) > maxBufferedSamples {
		drop := len(pcm) - maxBufferedSamples
		pcm = pcm[drop:]
		ts += int64(drop) * 1e6 / outRate
	}
	if n := len(p.q); n > 0 && ts < p.q[n-1].ts {
		p.st.Backwards++
		p.clearLocked()
	}
	p.st.Chunks++
	p.st.Samples += int64(len(pcm))
	if len(pcm) > p.st.MaxChunk {
		p.st.MaxChunk = len(pcm)
	}
	buffered := -p.off
	for _, queued := range p.q {
		buffered += len(queued.pcm)
	}
	if buffered+len(pcm) > maxBufferedSamples {
		// The queue is over its limit.
		//
		// The OLDEST audio is dropped, not everything: throwing the
		// whole queue away leaves the head of it far in the future, and
		// the player then sits in silence until the queue refills. What
		// is dropped here is already late, so dropping it costs nothing
		// that has not been lost already.
		p.trimLocked(buffered + len(pcm) - maxBufferedSamples)
		p.st.BufferOverruns++
	}
	p.q = append(p.q, chunk{ts: ts, pcm: pcm})
}

// trimLocked drops n samples from the head of the queue.
func (p *player) trimLocked(n int) {
	for n > 0 && len(p.q) > 0 {
		k := min(len(p.q[0].pcm)-p.off, n)
		p.advanceLocked(k)
		n -= k
	}
}

// clear drops everything buffered: stream/clear, stream/end, or HA taking
// the music plane.
func (p *player) clear() {
	p.mu.Lock()
	p.clearLocked()
	p.mu.Unlock()
}

func (p *player) clearLocked() {
	p.q, p.off, p.playing = nil, 0, false
}

func (p *player) resetStats() {
	p.mu.Lock()
	p.st = playerStats{}
	p.mu.Unlock()
}

func (p *player) stats() playerStats {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.st
}

// buffered is how much audio is queued, as time.
func (p *player) buffered() time.Duration {
	p.mu.Lock()
	defer p.mu.Unlock()
	n := -p.off
	for _, c := range p.q {
		n += len(c.pcm)
	}
	return time.Duration(n) * time.Second / outRate
}

func (p *player) active() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.q) > 0
}

// Fill writes one period of stereo S16LE to out, whose first frame will
// reach the DAC at playAt. It returns false when there is nothing to play at
// that moment, so the speaker treats the music plane as empty.
func (p *player) Fill(out []byte, playAt time.Time) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	frames := len(out) / 4
	if len(p.q) == 0 || !p.filter.synchronized() {
		if p.playing {
			p.st.Underruns++
			p.playing = false
		}
		p.st.Empty++
		return false
	}

	errUs := toUs(playAt) - p.localTimeLocked()
	errFrames := int(math.Round(float64(errUs) * outRate / 1e6))
	p.st.LastErrorUs = int(errUs)

	// How far the queue's head is from the play position, and how much
	// server time the queue covers.
	//
	// A queue that is far ahead of the play position is a server sending
	// faster than we play; one whose span is much longer than it holds
	// means the timestamps disagree with the samples.
	p.st.LeadUs = -errUs
	if n := len(p.q); n > 0 {
		tail := p.q[n-1]
		p.st.SpanUs = tail.ts + int64(len(tail.pcm))*1e6/outRate - p.q[0].ts
		// How the head and the tail of the queue sit against the
		// server's own clock, as our filter sees it.
		serverNow := p.filter.serverTime(nowUs())
		p.st.HeadAgeUs = serverNow - p.q[0].ts
		p.st.TailAgeUs = serverNow - tail.ts
	}

	lead := 0 // frames of silence before the first sample
	repeat := 0
	switch {
	case !p.playing || errUs > snapUs || errUs < -snapUs:
		if errFrames > 0 {
			// Late: the first samples should already have played.
			if !p.dropLocked(errFrames) {
				p.playing = false
				p.st.TooLate++
				return false
			}
		} else if errFrames < 0 {
			if -errFrames >= frames {
				p.playing = false
				p.st.NotDue++
				return false // not due yet
			}
			lead = -errFrames
		}
		p.playing = true
		p.st.Snaps++
	case errFrames > deadbandFrames:
		if !p.dropLocked(min(errFrames, maxStepFrames)) {
			p.playing = false
			return false
		}
		p.st.Corrections++
	case errFrames < -deadbandFrames:
		repeat = min(-errFrames, maxStepFrames)
		p.st.Corrections++
	}

	start, end := p.gain, p.gainTarget
	p.gain = end
	gainAt := func(i int) int32 {
		if start == end {
			return end
		}
		return start + (end-start)*int32(i)/int32(frames)
	}

	i := 0
	for ; i < lead; i++ {
		put(out, i, 0)
	}
	if repeat > 0 {
		s := p.q[0].pcm[p.off]
		for r := 0; r < repeat && i < frames; r++ {
			put(out, i, scale(s, gainAt(i)))
			i++
		}
	}
	for i < frames {
		if len(p.q) == 0 {
			// Ran dry mid-period: the rest is silence, and the next Fill
			// resyncs from scratch.
			for ; i < frames; i++ {
				put(out, i, 0)
			}
			p.playing = false
			p.st.Underruns++
			break
		}
		c := p.q[0].pcm
		n := min(len(c)-p.off, frames-i)
		for k := 0; k < n; k++ {
			put(out, i+k, scale(c[p.off+k], gainAt(i+k)))
		}
		i += n
		p.advanceLocked(n)
	}
	p.st.Played++
	p.st.Frames += int64(frames)
	return true
}

// localTimeLocked is when the next sample should reach the DAC on our clock.
func (p *player) localTimeLocked() int64 {
	c := p.q[0]
	ts := c.ts + int64(p.off)*1e6/outRate
	return p.filter.clientTime(ts) - p.delayUs
}

// dropLocked discards n samples, reporting false if the queue ran out first.
func (p *player) dropLocked(n int) bool {
	for n > 0 && len(p.q) > 0 {
		k := min(len(p.q[0].pcm)-p.off, n)
		if k == len(p.q[0].pcm)-p.off && p.off == 0 {
			p.st.LateDrops++
		}
		p.advanceLocked(k)
		n -= k
	}
	return len(p.q) > 0
}

func (p *player) advanceLocked(n int) {
	p.off += n
	for len(p.q) > 0 && p.off >= len(p.q[0].pcm) {
		p.off -= len(p.q[0].pcm)
		p.q[0].pcm = nil
		p.q = p.q[1:]
	}
}

func scale(s int16, g int32) int16 {
	if g == unity {
		return s
	}
	return int16((int32(s) * g) >> 15)
}

// put writes one mono sample to both channels of stereo frame i.
func put(out []byte, i int, s int16) {
	u := uint16(s)
	out[i*4], out[i*4+1] = byte(u), byte(u>>8)
	out[i*4+2], out[i*4+3] = byte(u), byte(u>>8)
}
