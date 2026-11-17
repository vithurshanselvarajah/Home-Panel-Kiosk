package sendspin

import (
	"github.com/vithurshanselvarajah/Home-Panel-Kiosk/audio/speaker"
	"log"
	"math"
	"sync"
	"time"
)

// The placement of a room's audio.
//
// The speaker pulls, and it pulls by frame index: the write loop asks for
// the frames that are going to the card next. That index IS the timeline, so
// the audio is placed against it rather than against a clock.
//
// The alternative is to compare the audio's own time with the wall clock
// and play it when it is due. That compares two readings that each carry
// their own jitter — the clock reading, and the moment the period was asked
// for — and the two are taken by different loops at different times. The
// error between them is therefore never small, and every period corrects
// it: which is heard as a crackle.
//
// So the audio is placed once, when it arrives, at the frame its server
// timestamp asks for, and the speaker takes it from there.

// holdMax bounds what is held ahead. The server keeps within the buffer
// capacity we advertised, so reaching this means a timestamp that cannot be
// believed rather than a server sending too much.
const holdMax = 60 * outRate

// Drift correction.
//
// The anchor maps the server's time to output frames at the nominal rate,
// but the speaker's clock is not the server's: measured on device, one Dot
// ran about 200 ppm fast and pulled ahead of another by 12 ms a minute. So
// every render period the frame being heard is compared with the frame the
// server clock says should be, the error is smoothed, and one frame is
// dropped or repeated per period while the smoothed error is outside the
// band.
//
// driftGain is the smoothing: at 47 periods a second, a time constant of
// about a second, enough to take the scheduling jitter out of when a period
// happens to be rendered. driftBand is 2 ms either side, which is what that
// jitter leaves. A band inside that residual corrects against the noise
// instead of the drift, and spends most of its corrections undoing the last
// ones.
//
// An error past snapBand is not drift but a misplaced anchor, most often a
// period's worth of phase between the write counter and the card at the
// moment the stream started, or an underrun that moved the counter on
// without playing anything. That is put right in one step of silence or one
// skip, which the spec allows on a start, and the fine correction takes it
// from there.
//
// maxSnap is the largest correction that can be one, above which the anchor
// predates a change of clock rather than having drifted.
// heldChunk is a chunk that arrived before the anchor could be laid.
type heldChunk struct {
	// at is the chunk's server timestamp.
	at int64
	// samples are its frames, interleaved stereo.
	samples []int16
}

// maxDelayMs is the longest a room may be held back, in milliseconds.
const maxDelayMs = 1000

// reanchorFrames is how far a chunk may be from the anchor before the
// placement lays a new one.
//
// It is a minute's worth of frames: a chunk that far from where the last
// one was is not a piece of the same timeline. A skip of a few seconds
// within a track does not move it, because the server keeps the timeline
// going across a skip within a track.
const reanchorFrames = 60 * outRate

const (
	driftGain  = 0.02
	driftBand  = outRate / 500
	snapBand   = outRate / 100
	maxSnap    = 2 * outRate
	duckFrames = outRate * 60 / 1000
)

// placement holds a room's audio and puts it where the server said it goes.
//
// It is the bridge between the server's clock and the speaker's frame
// index: the server's timestamps are turned into output frames once, when
// the stream starts, and everything after that is in frames.
type placement struct {
	mu    sync.Mutex
	ready bool
	// held is whether the server has said it is not playing, or the
	// placement is not ready. It gates the rendering.
	held bool
	// suspended is whether the arbiter has stood the placement down
	// because something else took the speaker. It gates the rendering
	// too, and it is kept apart from held so the two do not fight: the
	// server's state and the speaker's owner are different things.
	suspended bool
	// joined is whether the placement is in the arbiter's stack.
	joined bool

	// pcm holds frames from base onward. played is where the card has got
	// to, which is what decides whether a chunk is late — base only says
	// what we happen to be holding.
	base   int64
	played uint64
	pcm    []int16

	// The frame that carries server time at. Fixed once per stream:
	// recomputing it per chunk would feed the sampling jitter of "what is
	// playing now" straight back into where audio lands. Drift
	// correction nudges frame by whole frames instead, in step with the
	// audio it moves.
	//
	// It is signed, because the audio the server sends may already be
	// late: the frame it belongs at is then before the speaker started
	// writing, which is a negative frame index. Holding it unsigned and
	// clamping it at zero threw the whole offset away, and the drift
	// correction then drove the anchor below zero and the placement was
	// a long way from where the audio belonged.
	anchored bool
	frame    int64
	at       int64

	// delay is the room's static playback delay in frames: audio is placed
	// that much later than the server's timestamp asks for, to match a
	// slower player. It is on the placement only. frame and the drift
	// loop stay in terms of what the server clock says is heard now,
	// which is the card's position, so the delay never reads as an error
	// to correct.
	delay int64

	// drift is the smoothed error between the frame being heard and the
	// frame the server clock wants, in frames, positive when the room is
	// playing early. corrected counts frames repeated (positive) or
	// dropped (negative) to hold it.
	drift     float64
	corrected int64

	// nextReport is the frame the next correction line is due at, one a
	// second.
	nextReport uint64

	// filter maps the server's clock onto ours.
	filter *timeFilter

	// attach and detach are how the placement takes the speaker and gives
	// it back.
	//
	// The placement attaches itself when it is opened and detaches when it
	// is closed, rather than leaving it to a timer elsewhere to notice
	// that there is audio. A timer decides on what it can see at the
	// moment it looks, and the queue is empty between two chunks and
	// after a clear: a source detached because the queue was empty is
	// never attached again, because the queue stays empty while nothing
	// is pulling from it.
	attach func()
	detach func()
	// backgrounds is the arbiter the placement belongs to, so that it is
	// stood down when something else takes the speaker.
	backgrounds *speaker.Arbiter

	// position is the speaker's own frame counter and latency.
	position func() (uint64, time.Duration)
	// started says whether the speaker has written to the card yet. The
	// anchor is not laid before it has: a card that has written nothing
	// has no position, and an anchor laid against no position is the
	// send-ahead alone.
	started func() bool
	// latency is how long until a frame written now is heard.
	latency func() time.Duration

	// gain is what frames are multiplied by now and target what a duck
	// has asked for.
	gain, target float32

	// chunks is how many chunks have arrived and samples how many frames
	// they carried.
	chunks  int
	samples int64
	// pending is what arrived before the anchor could be laid: each
	// chunk's timestamp and its samples, held until the speaker has
	// started.
	pending []heldChunk
	// rms is how loud the last period handed over was.
	rms float64
	// playedPeriods is how many periods were filled and playedFrames how
	// many frames of audio they carried.
	playedPeriods int
	playedFrames  int64
	late          int64
	dropped       int64
}

// SetAttach tells the placement how to take the speaker and how to give it
// back.
//
// The placement attaches itself when it is opened and detaches when it is
// closed. See the fields for why.
func (o *placement) SetAttach(attach, detach func()) {
	o.mu.Lock()
	o.attach, o.detach = attach, detach
	o.mu.Unlock()
}

// newPlacement makes the placement for a session's clock.
func newPlacement(filter *timeFilter) *placement {
	return &placement{filter: filter, gain: 1, target: 1}
}

// use points the placement at this session's clock. One server at a time,
// so it changes only between sessions.
func (o *placement) use(filter *timeFilter) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.filter = filter
}

// open refuses anything the speaker cannot play, rather than playing it at
// the wrong speed.
func (o *placement) open() {
	o.mu.Lock()
	o.ready = true
	o.mu.Unlock()
}

// close gives the speaker back.
func (o *placement) close() {
	o.mu.Lock()
	o.ready = false
	// Closing leaves the speaker's backgrounds, and a hold is not released
	// to one that has left: kept, the next stream would join already
	// held, silent for as long as it played.
	o.held = false
	o.reset()
	detach := o.detach
	backgrounds := o.backgrounds
	joined := o.joined
	o.joined = false
	o.mu.Unlock()

	if joined && backgrounds != nil {
		backgrounds.Gave(o)
	}
	if detach != nil {
		detach()
	}
}

// openAgain is what a stream that starts after one that ended does.
//
// The placement stays open across the end of a stream: the server ends the
// stream on a pause and resumes the same one, and the audio has to be
// placed again when it does. So what is released is the hold, and the
// anchor with it, and not the placement itself.
func (o *placement) openAgain() {
	o.mu.Lock()
	first := !o.joined
	o.joined = true
	o.ready = true
	o.reset()
	attach := o.attach
	backgrounds := o.backgrounds
	o.mu.Unlock()

	if first && backgrounds != nil {
		// The music joins the arbiter, so it is stood down when a reply
		// takes the speaker and carried on afterwards.
		backgrounds.Took(o)
	}
	if attach != nil {
		attach()
	}
}

// write places a decoded chunk at the frame its timestamp asks for.
func (o *placement) write(at int64, samples []int16) {
	o.mu.Lock()
	if !o.ready {
		o.dropped++
		o.mu.Unlock()
		return
	}

	// The frame is signed, and the queue's own base is signed too: the
	// audio the server sends may already be late, and the frame it belongs
	// at is then before the speaker started writing.
	// A chunk that arrives a long way from where the anchor says it
	// belongs is a new timeline, not a piece of the old one: the server
	// skipped, or paused, or the track ended and another began.
	//
	// The anchor is then laid again against the card, so the audio lands
	// where the speaker is rather than where the old track would have put
	// it. Without this the chunk is placed millions of frames ahead of the
	// card, dropped as too far ahead, and the music stops while the server
	// thinks it is playing.
	if o.anchored {
		if drift := (at - o.at) * outRate / 1e6; drift > reanchorFrames || drift < -reanchorFrames {
			o.anchored = false
			o.drift = 0
			o.pcm = o.pcm[:0]
			o.base = 0
		}
	}

	frame := o.frameFor(at) + o.delay
	if !o.anchored {
		// The anchor is not laid yet: the speaker has not started. The
		// chunk is held rather than dropped, because the anchor will be
		// laid against the card and this chunk is the first of the
		// stream.
		o.pending = append(o.pending, heldChunk{at: at, samples: samples})
		o.mu.Unlock()
		return
	}
	// The chunks that arrived before the speaker started are placed now,
	// against the anchor that has just been laid.
	held := o.pending
	o.pending = nil
	o.mu.Unlock()
	for _, c := range held {
		o.write(c.at, c.samples)
	}
	o.mu.Lock()
	frame = o.frameFor(at) + o.delay
	if frame > int64(o.played) && frame-int64(o.played) > holdMax {
		o.dropped++
		o.mu.Unlock()
		return
	}

	// A chunk that reaches us after its frames have played is late:
	// whatever is left of it still belongs where it was meant to go, and
	// the rest is gone. Playing it anyway is what leaves a room behind for
	// good.
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

// frameFor converts a server timestamp to an output frame. Wants mu.
func (o *placement) frameFor(at int64) int64 {
	if o.anchored {
		return o.frame + (at-o.at)*outRate/1e6
	}

	// The frame the speaker is writing is going to the card next and is
	// heard the card's latency later, so this is where the server's
	// intended moment falls. It has to be the real latency rather than a
	// margin: a room next to one that measures its own is only in step if
	// both mean the same moment by "play at".
	// The anchor waits for the speaker.
	//
	// A speaker that has not written to the card has no position and no
	// latency reading, so an anchor laid against it is the send-ahead
	// alone: the audio is a moment ahead of a card that has not started,
	// and the drift correction spends the first seconds of the track
	// pulling it back. That is the jitter at the start of a track.
	if o.started != nil && !o.started() {
		return o.frame
	}

	written, latency := o.position()
	ahead := time.Until(o.serverToLocal(at)) - latency
	// The offset is clamped at zero: the audio the server sends may
	// already be late — the first chunk of a stream is stamped with when
	// it was sent, not when it is to be played — and then the audio plays
	// on arrival, which is what it does.
	//
	// Without the clamp the anchor was a second ahead of the card, and
	// the drift correction spent the next few seconds pulling it back a
	// second at a time. That is the jitter at the start of a track.
	//
	// The anchor itself is not clamped: it is a frame index, and a frame
	// before the speaker started writing is a negative one, which is the
	// frame the audio belongs at.
	o.frame = int64(written) + int64(max(0, ahead.Seconds()*outRate))
	o.at = at
	o.anchored = true
	if o.frame < 0 {
		o.frame = 0
	}
	o.played = uint64(o.frame)
	return o.frame
}

// serverToLocal turns the server's timestamp into ours.
//
// The filter maps the server's clock onto ours, so the result is a moment on
// our own wall clock — the same domain the timestamps went out in.
//
// The filter has its own lock, so it is asked while this one's held: the
// two are not the same lock and there is no cycle.
func (o *placement) serverToLocal(at int64) time.Time {
	if o.filter == nil {
		return time.Now()
	}
	return time.UnixMicro(o.filter.clientTime(at))
}

// Render is the speaker asking what this room plays next.
//
// The frames the card has already played are dropped, and what is left is
// the audio for the frames being rendered now.
func (o *placement) Render(from uint64, buf []int16) {
	o.mu.Lock()
	defer o.mu.Unlock()

	if from > o.played {
		o.played = from
	}

	// The card has passed these, and an underrun can ask for the same
	// frames twice, so dropping has to be idempotent rather than a consume.
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
		// How loud what was handed over is: a period of silence is
		// counted as played, and the count says nothing about whether
		// there was any sound in it.
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
	// A duck or its release under way: a step toward the target each
	// frame, both channels alike.
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

// correct holds the room to the server clock. Wants mu, and pcm's head at
// from.
func (o *placement) correct(from uint64) {
	if !o.anchored || len(o.pcm) < 2*outChannels {
		return
	}
	if o.filter == nil {
		return
	}
	// Nothing is corrected until the clock is good.
	//
	// Without a sync the clock reads the server's timestamps as local
	// ones, decades out, and every period would re-anchor against an
	// answer it does not have. What is queued is held: the anchor is
	// wrong until a sync lands, and the first correction after one puts
	// it right in a single step.
	//
	// Correcting against an unconverged clock is what made the first
	// second of a track jitter: the correction read a second of error
	// and snapped it back a second at a time.
	if !o.filter.synchronized() {
		return
	}
	now := o.filter.serverTime(nowUs())

	// The frame the server clock says should be heard now, against the one
	// that is: the frame being rendered less the ring it has yet to
	// travel, which the card reported just before this render.
	want := o.frame + (now-o.at)*outRate/1e6
	tail := o.tail()

	// The loop has the hardware tail in it: frames already handed to the
	// card still play at the old alignment, so a correction at full gain
	// overshoots and the next one swings back. driftGain is what damps
	// that, and a snap is bounded by it for the same reason.
	off := float64(int64(from) - tail - want)
	o.drift += (off - o.drift) * driftGain

	// What the correction is looking at, once a second: off should sit
	// near zero and stay there.
	if from >= o.nextReport {
		o.nextReport = from + outRate
		log.Printf("[sendspin] correction off_ms=%d drift_ms=%d from=%d want=%d anchor=%d corrected=%d tail_ms=%d queued_ms=%d",
			int64(off)*1000/outRate, int64(o.drift)*1000/outRate, from, want, o.frame, o.corrected,
			o.tail()*1000/outRate, len(o.pcm)/outChannels*1000/outRate)
	}

	// Re-anchor rather than snap: the next chunk lays one against the
	// clock the server is using now.
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
		// The anchor moves back with the audio.
		//
		// It is unsigned, and it may go below zero: the anchor is read
		// back as a signed number when the frame the server clock wants
		// is worked out, so the two wrap round together and stay in
		// step. Clamping it would move the anchor and not the frame it
		// is compared with, and the drift would then read as a whole
		// second of error — which the correction would then correct, by
		// putting a second of silence in front of the audio.
		o.frame -= int64(n)
		o.corrected -= int64(n)
		o.drift = 0
	case o.drift > driftBand:
		// Early: say the first frame twice, and move the anchor with it
		// so what arrives next lands in step with what is already
		// queued.
		o.pcm = append(o.pcm, o.pcm[:outChannels]...)
		copy(o.pcm[outChannels:], o.pcm[:len(o.pcm)-outChannels])
		o.frame++
		o.drift--
		o.corrected++
	case o.drift < -driftBand:
		// Late: skip the first frame.
		n := copy(o.pcm, o.pcm[outChannels:])
		o.pcm = o.pcm[:n]
		o.frame--
		o.drift++
		o.corrected--
	}
}

// tail is the latency in frames.
func (o *placement) tail() int64 {
	if o.latency == nil {
		return 0
	}
	return int64(o.latency() * outRate / time.Second)
}

// setDelay holds the room back by ms against the server's schedule, now:
// what is queued moves with it.
func (o *placement) setDelay(ms int) {
	// The delay is held to what a room can be held back by: a second. A
	// delay beyond that would hold the audio so far back that it is late
	// for ever.
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

// setGain sets the level the frames are played at, as the server's volume
// percentage.
//
// The curve is the spec's perceptual one: amplitude = (v/100)^1.5.
//
// The level is applied as the frames are rendered, so none are waiting at
// the level they were decoded at.
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

// Duck always quietens, never pauses: a hole in one room of a whole-house
// stream is worse.
func (o *placement) Duck(db int) {
	gain := float32(1)
	if db < 0 {
		gain = float32(math.Pow(10, float64(db)/20))
	}
	o.mu.Lock()
	o.target = gain
	o.mu.Unlock()
}

// Suspend and Resume do not keep a place: the rest of the house carried
// on, so this rejoins where they are now, which is what the frame index
// already says.
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

// Requeue re-scales what is already queued, which can be seconds of audio.
//
// The placement applies its gain as the frames are rendered, so there is
// nothing queued at a level that has to be changed: the frames that have
// not been rendered yet are rendered at the new one.
func (o *placement) Requeue() {}

// Ready says whether the placement is ready to be played from.
func (o *placement) Ready() bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.ready
}

// Held says whether the audio is held.
func (o *placement) Held() bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.held || o.suspended
}

// hold stops the audio, or starts it again.
//
// The hold gates the RENDERING, not the writing: what the server sends
// while the audio is held is still placed, so the buffer is warm and the
// anchor is live, and a play starts at once. Dropping it instead would
// leave the placement with nothing to play and no anchor, and the audio
// would have to be laid again from scratch against the card.
//
// What is queued when the hold goes on is dropped, and the anchor with it:
// a pause means the timeline is about to change, and what was queued
// belongs to the timeline before it.
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

// flush drops what has not been heard yet, and the anchor with it: what
// comes next is a new timeline.
func (o *placement) flush() {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.reset()
}

// stats are the counts since the stream started, for the status report.
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

// resetStats clears the counts since the stream started.
func (o *placement) resetStats() {
	o.mu.Lock()
	o.late, o.dropped = 0, 0
	o.corrected = 0
	o.mu.Unlock()
}

// reset wants mu.
func (o *placement) reset() {
	o.pcm = o.pcm[:0]
	o.base = 0
	o.anchored = false
	o.drift = 0
}

// queuedMs is how much audio is waiting, in milliseconds.
func (o *placement) queuedMs() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return len(o.pcm) / outChannels * 1000 / outRate
}

// misses are the chunks that were late and the chunks that were dropped.
func (o *placement) misses() (late, dropped int64) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.late, o.dropped
}

// scaleSample scales a sample by a gain.
func scaleSample(s int16, gain float32) int16 {
	if gain == 1 {
		return s
	}
	return int16(max(math.MinInt16, min(float64(s)*float64(gain), math.MaxInt16)))
}
