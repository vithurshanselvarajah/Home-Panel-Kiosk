package autobrightness

import (
	"math"
	"testing"
)

// The automatic brightness turns the room's light into the screen's
// brightness.
//
// The brightness setting is a ceiling: the brightest the panel is
// allowed to be. The room's light decides how much of that ceiling the
// screen gets.
//
// These check the curve, because a curve that is wrong is a screen that
// is wrong in a way nobody can describe: too bright in a dark room, or
// too dim by a window.

func TestADarkRoomGetsTheDimmest(t *testing.T) {
	// A dark room reads as 0, 1 or 2 depending on the unit, and all of
	// them are the dimmest.
	for _, lux := range []float64{0, 1, 2} {
		level := Level(100, true, lux)
		dark := Level(100, true, 2)
		if level != dark {
			t.Errorf("a room of %v lux gets %d, a room of 2 lux gets %d", lux, level, dark)
		}
	}
}

func TestABrightRoomGetsTheWholeCeiling(t *testing.T) {
	// At brightLux the screen is at the ceiling the person set.
	level := Level(100, true, brightLux)
	if level < 1020 {
		t.Errorf("a bright room gets %d, it should get the whole ceiling", level)
	}
}

func TestTheRoomScalesTheCeiling(t *testing.T) {
	// A dimmer room gets a dimmer screen, and the ceiling still holds:
	// the screen is never brighter than the person asked for.
	dim := Level(100, true, 10)
	bright := Level(100, true, 200)
	if dim >= bright {
		t.Errorf("a dim room gets %d and a bright room %d, the dim one should be lower", dim, bright)
	}
	if bright > 1023 {
		t.Errorf("a bright room gets %d, which is over the ceiling", bright)
	}
}

func TestTheCeilingIsTheCeiling(t *testing.T) {
	// The brightness setting is the brightest the panel may be, so a
	// half-bright setting is never brighter than half.
	level := Level(50, true, brightLux)
	if level > 512 {
		t.Errorf("a ceiling of 50%% gives %d, it should be about half", level)
	}
}

func TestWithoutTheRoomTheSettingIsTheBrightness(t *testing.T) {
	// With the automatic brightness off, the room does not matter.
	for _, lux := range []float64{0, 10, 200, 1000} {
		level := Level(80, false, lux)
		if level < 800 || level > 820 {
			t.Errorf("with the room not followed, a room of %v lux gives %d, it should be 80%%", lux, level)
		}
	}
}

func TestTheBrightnessIsNeverZero(t *testing.T) {
	// A level of zero switches the panel's PWM off, and the panel stops
	// following levels afterwards.
	for _, lux := range []float64{0, 0.1, 1, 2} {
		level := Level(100, true, lux)
		if level < 1 {
			t.Errorf("a room of %v lux gives a level of %d, which switches the panel's PWM off", lux, level)
		}
	}
}

func TestTheLevelIsSmoothedTowardsTheTarget(t *testing.T) {
	// A shadow that passes does not make the panel jump: the level
	// moves a quarter of the way each time.
	level := Smoothed(1000, 500)
	if level <= 500 || level >= 1000 {
		t.Errorf("the level moved from 1000 to %v, it should move a quarter of the way", level)
	}
	if math.Abs(level-875) > 0.001 {
		t.Errorf("the level moved to %v, it should move to 875", level)
	}
}

func TestASettledLevelIsTheTarget(t *testing.T) {
	// Smoothing alone would stop half a step short, which rounds a
	// level under it, so a level that has been reached is the target.
	if got := Smoothed(500, 500); got != 500 {
		t.Errorf("a settled level is %v, it should be the target", got)
	}
	if got := Smoothed(500.4, 500); got != 500 {
		t.Errorf("a settled level is %v, it should be the target", got)
	}
}
