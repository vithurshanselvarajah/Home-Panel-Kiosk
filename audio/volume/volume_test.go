package volume

import (
	"math"
	"testing"
)

// TestTheCurveIsMonotonic checks that the level rises all the way.
//
// A curve that went down anywhere would mean a slider position that was
// quieter than the one before it, which is the kind of thing that is
// heard as a mistake rather than reasoned about.
func TestTheCurveIsMonotonic(t *testing.T) {
	for step := 1; step <= Steps; step++ {
		if DB(step) <= DB(step-1) {
			t.Errorf("step %d is %v dB, which is not above step %d's %v dB",
				step, DB(step), step-1, DB(step-1))
		}
	}
}

// TestTheTopIsBelowUnity checks that the loudest step is below unity.
//
// The amplifier's own protection is in front of the signal: its analog
// gain and its limiter. A level at or above unity would be asking for
// more than the amplifier can give, and the limiter would be doing the
// work that a curve should have done.
func TestTheTopIsBelowUnity(t *testing.T) {
	if db := DB(Steps); db >= 0 {
		t.Errorf("the top of the curve is %v dB, which is at or above unity", db)
	}
}

// TestTheBottomIsSilence checks that step zero is silence.
func TestTheBottomIsSilence(t *testing.T) {
	if Gain(0) != 0 {
		t.Errorf("step zero has a gain of %v, which is not silence", Gain(0))
	}
	if DB(0) != Mute {
		t.Errorf("step zero is %v dB, which is not the mute level %v", DB(0), Mute)
	}
}

// TestHalfIsQuieterThanHalf checks the shape of the curve.
//
// Half the dial is a quiet conversation and not half the amplitude, and
// the top is much louder than the middle but not four times as loud. A
// linear scale would put the middle at half the amplitude and the top at
// twice it, which is the shape that makes a top end sound harsh.
func TestHalfIsQuieterThanHalf(t *testing.T) {
	half := GainForPercent(50)
	if half <= 0 {
		t.Fatal("half the dial is silence")
	}
	if half >= 0.5 {
		t.Errorf("half the dial has a gain of %v, which is not quieter than half the amplitude", half)
	}
	// The whole range is a bounded number of decibels, so the loudest
	// step is not many times the quietest.
	//
	// The limit is twelve rather than eight because the top of the dial
	// was raised and the middle was not: the top is now two decibels
	// below unity where it used to be six, and half the dial is where it
	// always was. The ratio between them is therefore larger, and the
	// ratio is not the thing being kept — the two ends are.
	top := GainForPercent(100)
	if top/half > 12 {
		t.Errorf("the top is %v times the middle, which is too much", top/half)
	}
}

// TestTheTopIsNearlyUnity checks that the top of the dial is as loud as
// it goes.
//
// The card's master mixer is at unity, so the curve is the only thing
// between the samples and the amplifier. The top is two decibels below
// unity, which leaves the amplifier's own protection in front of the
// signal, and it is not any quieter than that.
func TestTheTopIsNearlyUnity(t *testing.T) {
	db := DB(Steps)
	if db < -3 {
		t.Errorf("the top of the curve is %v dB, which is quieter than it needs to be", db)
	}
	if db >= 0 {
		t.Errorf("the top of the curve is %v dB, which is at or above unity", db)
	}
}

// TestTheMiddleHasNotMoved checks that the quiet end is still quiet.
//
// The top of the dial was raised because it was not loud enough for a
// room. The bottom half of the dial is a quiet conversation and has to
// stay one: a panel that is loud at half the dial is a panel nobody
// leaves playing.
func TestTheMiddleHasNotMoved(t *testing.T) {
	// Half the dial is about twenty-two decibels down. It was that
	// before the top was raised, and it is that now.
	db := DBForPercent(50)
	if db < -23 || db > -21 {
		t.Errorf("half the dial is %v dB, which is not where it was", db)
	}
}

// TestPercentAndStepRoundTrip checks that a level survives being turned
// into a step and back.
func TestPercentAndStepRoundTrip(t *testing.T) {
	for percent := 0; percent <= 100; percent++ {
		step := Step(percent)
		if step < 0 || step > Steps {
			t.Fatalf("%d%% is step %d, which is off the curve", percent, step)
		}
		back := Percent(step)
		// One step is about three percent, so a percent either side is
		// as close as the curve allows.
		if back < percent-2 || back > percent+2 {
			t.Errorf("%d%% became step %d and back to %d%%", percent, step, back)
		}
	}
}

// TestTheGainIsTheDecibels checks that the gain and the curve agree.
//
// The gain is what the samples are scaled by, and the curve is what the
// level means, so they are the same thing said twice.
func TestTheGainIsTheDecibels(t *testing.T) {
	// Step zero is silence rather than a very quiet level, so it is not
	// the decibel curve's gain. Every other step is.
	for step := 1; step <= Steps; step++ {
		want := float32(math.Pow(10, DB(step)/20))
		if Gain(step) != want {
			t.Errorf("step %d has a gain of %v, want %v", step, Gain(step), want)
		}
	}
}

// TestOutOfRangeIsHeld checks that a level outside the range is held to
// it.
//
// Home Assistant and the panel can both send a level outside the range,
// and a level of 200 percent is not louder than a hundred.
func TestOutOfRangeIsHeld(t *testing.T) {
	if Step(-10) != 0 {
		t.Errorf("a negative level became step %d", Step(-10))
	}
	if Step(200) != Steps {
		t.Errorf("a level of 200%% became step %d", Step(200))
	}
	if Gain(Steps+10) != Gain(Steps) {
		t.Error("a level past the top is louder than the top")
	}
	if Gain(-10) != 0 {
		t.Error("a negative level is not silence")
	}
}
