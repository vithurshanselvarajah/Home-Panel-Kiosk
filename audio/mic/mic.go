// Package mic turns the microphone's raw capture into what the wake word wants to hear.
//
// The Echo Show 8 has a four-microphone array, and the card hands it over at 96 kHz as 32-bit
// samples. Everything downstream wants 16 kHz mono 16-bit, because that is what the wake word model
// was trained on and what Home Assistant's voice pipeline speaks.
//
// So the capture goes through four steps, in this order:
//
//   - the four channels are mixed down to one, because the array's channels are four views of the
//     same room and the model wants one of them;
//   - the result is brought down from 96 kHz to 16 kHz;
//   - the echo canceller takes the panel's own voice out;
//   - the automatic gain control brings the speech up to a level the model knows, whatever the
//     room is like, and the noise estimator takes the steady hum and hiss out, leaving speech and
//     whatever is as sudden as speech.
//
// The order matters. The echo canceller runs before the leveler, because the leveler measures the
// room and the panel's own voice is not the room. The leveler runs before the denoiser, because the
// denoiser needs the noise still in the signal to measure against.
//
// Ported from TECHO5 (github.com/HuskerMinion/techo5), MIT licensed. Copyright (c) 2026 Yuri
// Gelfand.
package mic

import (
	"math"
	"time"

	"github.com/vithurshanselvarajah/Home-Panel-Kiosk/audio/aec"
	"github.com/vithurshanselvarajah/Home-Panel-Kiosk/audio/denoise"
	"github.com/vithurshanselvarajah/Home-Panel-Kiosk/audio/echoref"
	"github.com/vithurshanselvarajah/Home-Panel-Kiosk/audio/leveler"
	"github.com/vithurshanselvarajah/Home-Panel-Kiosk/audio/rate"
)

// The rates. The card hands the microphones over at the amplifier's rate, and everything
// downstream works at the rate the wake word model wants.
//
// CardRate and CardChannels are the card's own format. The audio package
// has the same two numbers, because it has to open the card in this
// format to tell a real microphone from a dummy: the dummy opens for the
// level meter's format but not for this one. The two sets of numbers
// have to agree, so they are checked against each other.
const (
	// CardRate is the rate the card captures the microphones at.
	CardRate = 96000

	// OutRate is the rate the wake word wants.
	OutRate = 16000

	// CardChannels is how many microphone channels the card hands over.
	CardChannels = 4

	// cardFull is the microphones' full scale.
	//
	// The card hands the samples over as 32-bit words, but the converter's 24 bits are
	// left-aligned in the word: the word is the 24-bit value shifted up by eight bits, so the
	// full scale of the converter is 2^23 and a word at the converter's full scale is 2^31.
	//
	// The word is scaled against the converter's full scale, so the shift is undone by the
	// scaling: a word of 2^23, which is the converter at full scale, comes out at 2^15.
	cardFull = 1 << 23
)

// The filter lengths, in the same proportions TECHO5 uses them at.
//
// The decimator's delay is (taps-1)/2 input samples, so 192 at 96 kHz is 1 ms — the same delay the
// microphones' own filter has, which is what keeps the echo reference aligned with the speech.
const (
	decTaps = 192

	// cancelTaps is how far the echo filter reaches, in samples, so 64 ms of tail. Not a setting:
	// measured on this enclosure, cancellation rises with every tap available, so there is no
	// room-dependent best value to look for, only what the device can afford.
	cancelTaps = 1024

	// cancelMu is how fast the echo filter adapts.
	cancelMu = 0.1
)

// refQuiet is the mean square per sample, at int16 scale, below which the loopback counts as
// silence. Below it there is no echo to remove, so the filter is skipped entirely and the frame
// costs nothing — which is what keeps this free on an idle device.
const refQuiet = 1e-7 * 32768 * 32768 / 4

// Pipeline is one microphone's chain: the card's capture in, the wake word's audio out.
//
// It is not safe for concurrent use: the whole point is that it holds the state of the room's
// noise, the room's floor and the echo path, and those are one thing.
type Pipeline struct {
	// dec brings the card's rate down to the output's, one channel.
	dec *rate.Decimator

	// level is the automatic gain control.
	level *leveler.Leveler

	// noise is the noise estimator.
	noise *denoise.Stream

	// cancel is the echo canceller, or nil when the panel has no reference to cancel with.
	cancel *aec.Canceller

	// The buffers, kept between calls so that a frame costs no allocation.
	mono    []float32
	outMono []int16
	refL    []float32
	refR    []float32
	ref     []int16

	// clock is the mapping from the capture's sample count to when each sample was recorded.
	clock echoref.Clock

	// heard is how many samples have been taken from the card.
	heard uint64

	// leveling and denoising say whether the leveler and the denoiser are applied. The echo
	// canceller always is: it has nothing to learn from silence.
	leveling  bool
	denoising bool
}

// New makes a pipeline for a card handing over channels channels at CardRate.
//
// The echo canceller is only made when the reference is wanted: a panel whose speaker is not
// running has nothing to cancel, and the filter would learn the room's noise as if it were echo.
func New(channels int, withEcho bool) *Pipeline {
	p := &Pipeline{
		dec:   rate.NewDecimator(CardRate/OutRate, decTaps),
		level: leveler.New(OutRate/100, OutRate),
		noise: denoise.NewStream(OutRate),
		clock: echoref.NewClock(OutRate),
		// Leveling and denoising are on by default: without them the speech arrives at a level
		// the model was never trained on, and in a room with a fan in it the model hears noise.
		leveling:  true,
		denoising: true,
	}

	if withEcho {
		if c, err := aec.New(aec.Config{Taps: cancelTaps, Mu: cancelMu}); err == nil {
			p.cancel = c
		}
	}

	// The buffers are sized for a tenth of a second, which is the block
	// the capture is read in. A block larger than that grows them: a
	// block that was too long would have its tail dropped, which is a
	// block of audio the wake word never hears.
	p.mono = make([]float32, CardRate/10)
	p.outMono = make([]int16, OutRate/10)
	p.refL = make([]float32, OutRate/10)
	p.refR = make([]float32, OutRate/10)
	p.ref = make([]int16, OutRate/10)
	return p
}

// SetLeveling turns the automatic gain control on or off.
func (p *Pipeline) SetLeveling(on bool) { p.leveling = on }

// SetDenoising turns the noise estimator on or off.
func (p *Pipeline) SetDenoising(on bool) { p.denoising = on }

// SetSensitivity sets how far over the room's own floor counts as something happening, in dB.
func (p *Pipeline) SetSensitivity(db int) { p.level.AtSensitivity(db) }

// AtGain tells the pipeline what the converter's analog gain is now, which decides how low the
// leveler's floors may go.
func (p *Pipeline) AtGain(db int) { p.level.AtGain(db) }

// Level is how loud the room is now, from 0 for quiet to 1 for someone talking close by.
func (p *Pipeline) Level() float64 { return p.level.Level() }

// Floor is the quietest the room has been heard to be, in dBFS.
func (p *Pipeline) Floor() float64 { return p.level.Floor() }

// Gain is what the leveler is currently applying, in dB.
func (p *Pipeline) Gain() float64 { return p.level.Gain() }

// Clipped is how many samples the leveler has had to hold at full scale.
func (p *Pipeline) Clipped() uint64 { return p.level.Clipped() }

// ERLE is how much quieter the echo canceller has made the room, in dB.
func (p *Pipeline) ERLE() float64 {
	if p.cancel == nil {
		return 0
	}
	return p.cancel.ERLE()
}

// Forget throws away what the pipeline learned about the room.
func (p *Pipeline) Forget() {
	p.level.Forget()
	p.noise.Forget()
	if p.cancel != nil {
		p.cancel.Reset()
	}
	p.clock.Reset()
}

// Process takes a block of the card's capture and returns what the wake word should hear.
//
// The card's samples are 32-bit, left-aligned in the container, so a sample's value sits in the top
// bits and the bottom are zero. They are brought down to 16-bit on the way, which is where the
// conversion loses the least: the input is already at 24 bits of precision inside a 32-bit word.
//
// The returned slice is reused on the next call.
func (p *Pipeline) Process(pcm []byte, channels int) []int16 {
	frameBytes := channels * 4
	if frameBytes <= 0 {
		return nil
	}
	frames := len(pcm) / frameBytes
	if frames == 0 {
		return nil
	}
	// A block longer than the buffer grows it. A block that was too
	// long and had its tail dropped would be a block of audio the wake
	// word never hears, and the loss would be at the end of every read.
	if frames > len(p.mono) {
		p.mono = make([]float32, frames)
		p.outMono = make([]int16, frames/CardRate*OutRate+1)
		p.refL = make([]float32, len(p.outMono))
		p.refR = make([]float32, len(p.outMono))
		p.ref = make([]int16, len(p.outMono))
	}

	// The four channels are averaged into one. An average of the array is one fixed view of the
	// room, which is what the echo canceller needs: a beamformer would steer at the loudest sound,
	// and during playback the loudest sound is the panel's own speaker.
	for f := 0; f < frames; f++ {
		off := f * frameBytes
		var sum float64
		for c := 0; c < channels; c++ {
			sum += float64(int32(uint32(pcm[off+c*4]) | uint32(pcm[off+c*4+1])<<8 |
				uint32(pcm[off+c*4+2])<<16 | uint32(pcm[off+c*4+3])<<24))
		}
		// The converter's 24 bits are left-aligned in the word: the
		// word is the 24-bit value shifted up by eight bits.
		//
		// A recording of the unit settles it: the low byte of the word
		// is set in 0.0% of samples and the top byte in 53%. A
		// sign-extended sample sets the low byte, because the low bits
		// are the value's own; a left-aligned one does not, because the
		// low bits are the converter's padding.
		//
		// The shift is undone by the scaling rather than separately. The
		// full scale of the converter is 2^23, and a word at that full
		// scale is 2^31, so dividing the word by 2^23 leaves the
		// converter's own scale, which is then the 16-bit scale the
		// model wants. A word of 2^23 is the converter at full scale and
		// comes out as 2^15, which is the 16-bit full scale.
		v := sum / float64(channels) / float64(cardFull)
		p.mono[f] = float32(v * math.MaxInt16)
	}

	// Down to the wake word's rate.
	out := p.outMono[:0]
	for _, v := range p.mono[:frames] {
		y, ok := p.dec.Push(v)
		if !ok {
			continue
		}
		if y > 32767 {
			y = 32767
		} else if y < -32768 {
			y = -32768
		}
		out = append(out, int16(y))
	}
	if len(out) == 0 {
		return nil
	}

	// The echo canceller runs before the leveler, because the leveler measures the room and the
	// panel's own voice is not the room.
	if p.cancel != nil {
		p.cancelEcho(out)
	}

	// The leveler, then the denoiser. The leveler raises the speech to a level the model knows;
	// the denoiser takes the steady noise out of what is left.
	if p.leveling {
		p.level.Apply(out)
	}
	if p.denoising {
		p.noise.Apply(out)
	}

	p.heard += uint64(len(out))
	return out
}

// cancelEcho subtracts the panel's own voice from the frame.
//
// The reference is what the speaker played, read at the moment the microphones would have heard
// it. When there is no playback there is no reference, and the frame is left alone.
func (p *Pipeline) cancelEcho(frame []int16) {
	// The reference is read at the time the frame was recorded, which the clock knows.
	start := p.clock.At(p.heard)
	if !echoref.Fill(p.refL[:len(frame)], p.refR[:len(frame)], start) {
		return
	}

	// The reference is the average of the two channels, which is what the microphones see of it.
	for i := range frame {
		p.ref[i] = int16((p.refL[i] + p.refR[i]) / 2)
	}

	// A reference that is too quiet to have an echo in it is skipped: adapting on silence would
	// fit the filter to the room's own noise, which is the one thing it must never learn.
	var power float64
	for _, v := range p.ref {
		power += float64(v) * float64(v)
	}
	if power/float64(len(p.ref)) < refQuiet {
		return
	}

	if out, err := p.cancel.Process(frame, p.ref); err == nil {
		copy(frame, out)
	}
}

// Push plays a period into the echo reference.
//
// The speaker tells the reference what it sent and when its last frame will be heard, so the
// microphones can be told what they are about to hear again.
func (p *Pipeline) Push(pcm []byte, rate int, heardEnd time.Time) {
	echoref.Push(pcm, rate, heardEnd)
}

// Mark tells the pipeline when a sample of the capture was recorded.
//
// The capture and the playback run off the same AFE clocks, so the two never drift; what is left is
// the jitter of reading a delay and the clock together, which the clock smooths away.
func (p *Pipeline) Mark(n uint64, at time.Time) {
	p.clock.Mark(n, at)
}

// Reset forgets the room, for a capture that starts over.
func (p *Pipeline) Reset() {
	p.clock.Reset()
	p.heard = 0
	p.dec.Reset()
}
