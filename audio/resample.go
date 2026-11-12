package audio

// Resampler converts mono PCM from one rate to another.
//
// It is for the voice reply, which arrives at the rate Home Assistant
// speaks at and has to be played at the rate the speaker runs at. The
// reply is band-limited to the rate it arrived at, so interpolating
// between the samples is enough: repeating each sample instead would
// leave an audible copy of the speech an octave below the real one.
//
// The state is carried between calls, because the audio arrives in
// chunks. A reply converted one chunk at a time has to be interpolated
// across the chunk boundaries, or every boundary is a click.
//
// A Resampler is not safe for concurrent use: one reply is converted by
// one goroutine, and a second reply gets a second Resampler.
type Resampler struct {
	// step is how far into the input one output sample advances.
	step float64
	// j is the index of the next output sample.
	j int
	// n is how many input samples have been consumed.
	n int
	// prev is the last input sample.
	prev int16
	// have says whether there is a previous sample yet.
	have bool
}

// NewResampler makes a resampler that converts from one rate to another.
//
// A conversion to the same rate is a copy, and the resampler passes the
// samples through.
func NewResampler(from, to int) *Resampler {
	r := &Resampler{}
	if from > 0 && to > 0 && from != to {
		r.step = float64(from) / float64(to)
	}
	return r
}

// Convert converts a block of PCM.
//
// The block is signed 16-bit little-endian mono, and so is the result.
// The result is a new buffer, and the caller owns it.
//
// The tail of the audio is finished by Flush, because only the caller
// knows that the last chunk has arrived.
func (r *Resampler) Convert(pcm []byte) []byte {
	if r == nil || r.step == 0 {
		return pcm
	}

	out := make([]byte, 0, len(pcm)*2+8)
	for i := 0; i+1 < len(pcm); i += 2 {
		cur := int16(uint16(pcm[i]) | uint16(pcm[i+1])<<8)
		if !r.have {
			// The first sample is the one before the first interval, so
			// there is nothing to interpolate towards yet.
			r.have = true
			r.prev = cur
			r.n = 1
			continue
		}
		r.n++

		// The outputs that fall between the previous sample and this one
		// are interpolated between them.
		d := int32(cur) - int32(r.prev)
		for {
			position := float64(r.j) * r.step
			if position >= float64(r.n-1) {
				break
			}
			local := position - float64(r.n-2)
			out = appendSample(out, int32(r.prev)+int32(float64(d)*local))
			r.j++
		}
		r.prev = cur
	}
	return out
}

// Flush finishes the conversion.
//
// The last of the audio is held rather than interpolated: there is no
// next sample to interpolate towards, and holding the last sample is
// what keeps the end of the audio from dropping a fraction of a
// millisecond.
//
// It is called once, when the audio has all arrived.
func (r *Resampler) Flush() []byte {
	if r == nil || r.step == 0 || !r.have {
		return nil
	}

	out := make([]byte, 0, 8)
	for float64(r.j)*r.step < float64(r.n) {
		out = appendSample(out, int32(r.prev))
		r.j++
	}
	return out
}

// appendSample adds one sample to a buffer of little-endian bytes.
func appendSample(out []byte, v int32) []byte {
	if v > 32767 {
		v = 32767
	}
	if v < -32768 {
		v = -32768
	}
	return append(out, byte(v), byte(uint16(v)>>8))
}
