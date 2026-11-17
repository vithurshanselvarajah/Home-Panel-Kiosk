package speaker

// cardFormat is what the playback device is opened at: the card's rate, the width of the samples it is
// sent (32 is S24_LE, see widenS16), and the routes it needs on before it will open. Most boards have one
// for every output (formatFor); the Echo Dot 3rd gen has two, and the player opens the device again when
// the output moves between them (Run).
type cardFormat struct {
	rate, bits int
	open       []kctl
}

// same is whether g opens the device as f does, so that a change of output needs no new stream.
func (f cardFormat) same(g cardFormat) bool { return f.rate == g.rate && f.bits == g.bits }

// CardRate is the rate the card is played at, and CardBits the width the
// samples are sent in.
//
// The card runs at the amplifier's own rate, because the tuning inside the
// amplifier was made for it: at 48 kHz every corner of its equaliser and its
// limiter sits an octave low, so the tone is wrong and the protection that
// should be catching the peaks is not catching them.
//
// A program that opens the card for the speaker has to open it at this rate,
// or the card is fed at a rate it was not set up for.
func CardRate() int { return formatFor(OutputSpeaker).rate }

// CardBits is the width the samples are sent in: 16 for a card that takes
// them as they are, 32 for one that wants 24 bits in a 32-bit container.
func CardBits() int { return formatFor(OutputSpeaker).bits }
