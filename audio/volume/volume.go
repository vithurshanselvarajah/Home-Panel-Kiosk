// Package volume is the panel's volume, as a curve.
//
// The volume is a percentage in the panel, in Home Assistant, and on the device program, but what
// reaches the speaker is a level in decibels, and the two are not the same thing. A percentage of a
// linear scale is not a percentage of how loud something sounds: half the dial on a linear scale is
// half the amplitude, which is a quarter of the power, which is about six decibels down — but the
// useful range of a room is not spread evenly over decibels either.
//
// So the volume is mapped through a curve, and the curve is applied in one place only. A level that
// is attenuated twice — once in the mixer and once in the samples — is not a louder or a quieter
// volume, it is a volume that nobody can reason about: the same slider position sounds different
// depending on which path the audio took.
//
// The curve is TECHO5's (github.com/HuskerMinion/techo5), MIT licensed: about 1.5 dB a step up to
// half the dial, 1.2 dB a step above it, with the top a few decibels below unity rather than at it,
// so the amplifier's own protection stays in front of the signal.
//
// The top has been raised from -6 dB to -3.1 dB. At -6 dB the top of the dial was not loud enough
// for a room: forty per cent more amplitude is about 1.9 dB, and that is what the top is now. It
// is still below unity, so the amplifier's protection is still in front of the signal.
package volume

import "math"

// Steps is the number of volume steps, the same count the panel's percentage is divided into.
const Steps = 30

// curve is the level in decibels for each step. Step 0 is silence, step 1 is the first audible
// level, and step Steps is the top.
//
// The shape is TECHO5's: linear in decibels, roughly 1.5 dB a step to the middle of the dial and
// 1.2 dB a step above it, so that half the dial is a quiet conversation and the top is room-loud
// rather than painful.
var curve = [Steps + 1]float64{
	-90, -43.1, -41.6, -40.1, -38.6, -37.1, -35.6, -34.1, -32.6, -31.1,
	-29.6, -28.1, -26.6, -25.1, -23.6, -22.1, -20.9, -19.7, -18.5, -17.3,
	-16.1, -14.9, -13.7, -12.5, -11.3, -10.1, -8.9, -7.7, -6.5, -5.3, -3.1,
}

// Mute is the attenuation the curve uses for step 0.
const Mute = -90

// Step turns a percentage into a step on the curve.
func Step(percent int) int {
	percent = clampPercent(percent)
	return int(math.Round(float64(percent) / 100 * Steps))
}

// Percent turns a step on the curve back into a percentage.
func Percent(step int) int {
	step = clampStep(step)
	return int(math.Round(float64(step) / Steps * 100))
}

// DB is the level in decibels for a step.
func DB(step int) float64 {
	return curve[clampStep(step)]
}

// Gain is the linear gain for a step, which is what the samples are scaled by.
//
// Step 0 is silence rather than a very quiet level, so a muted panel is genuinely silent.
func Gain(step int) float32 {
	db := DB(step)
	if db <= Mute {
		return 0
	}
	return float32(math.Pow(10, db/20))
}

// GainForPercent is the linear gain for a percentage, which is what the panel's slider sets.
func GainForPercent(percent int) float32 { return Gain(Step(percent)) }

// DBForPercent is the level in decibels for a percentage.
func DBForPercent(percent int) float64 { return DB(Step(percent)) }

// clampPercent holds a percentage in the range the panel accepts.
func clampPercent(percent int) int {
	if percent < 0 {
		return 0
	}
	if percent > 100 {
		return 100
	}
	return percent
}

// clampStep holds a step in the range the curve has.
func clampStep(step int) int {
	if step < 0 {
		return 0
	}
	if step > Steps {
		return Steps
	}
	return step
}
