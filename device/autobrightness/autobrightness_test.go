package autobrightness

import (
	"math"
	"testing"
)

func TestADarkRoomGetsTheDimmest(t *testing.T) {

	for _, lux := range []float64{0, 1, 2} {
		level := Level(100, true, lux)
		dark := Level(100, true, 2)
		if level != dark {
			t.Errorf("a room of %v lux gets %d, a room of 2 lux gets %d", lux, level, dark)
		}
	}
}

func TestABrightRoomGetsTheWholeCeiling(t *testing.T) {

	level := Level(100, true, brightLux)
	if level < 1020 {
		t.Errorf("a bright room gets %d, it should get the whole ceiling", level)
	}
}

func TestTheRoomScalesTheCeiling(t *testing.T) {

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

	level := Level(50, true, brightLux)
	if level > 512 {
		t.Errorf("a ceiling of 50%% gives %d, it should be about half", level)
	}
}

func TestWithoutTheRoomTheSettingIsTheBrightness(t *testing.T) {

	for _, lux := range []float64{0, 10, 200, 1000} {
		level := Level(80, false, lux)
		if level < 800 || level > 820 {
			t.Errorf("with the room not followed, a room of %v lux gives %d, it should be 80%%", lux, level)
		}
	}
}

func TestTheBrightnessIsNeverZero(t *testing.T) {

	for _, lux := range []float64{0, 0.1, 1, 2} {
		level := Level(100, true, lux)
		if level < 1 {
			t.Errorf("a room of %v lux gives a level of %d, which switches the panel's PWM off", lux, level)
		}
	}
}

func TestTheLevelIsSmoothedTowardsTheTarget(t *testing.T) {

	level := Smoothed(1000, 500)
	if level <= 500 || level >= 1000 {
		t.Errorf("the level moved from 1000 to %v, it should move a quarter of the way", level)
	}
	if math.Abs(level-875) > 0.001 {
		t.Errorf("the level moved to %v, it should move to 875", level)
	}
}

func TestASettledLevelIsTheTarget(t *testing.T) {

	if got := Smoothed(500, 500); got != 500 {
		t.Errorf("a settled level is %v, it should be the target", got)
	}
	if got := Smoothed(500.4, 500); got != 500 {
		t.Errorf("a settled level is %v, it should be the target", got)
	}
}
