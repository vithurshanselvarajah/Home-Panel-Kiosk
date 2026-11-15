// Package autobrightness turns the room's light into the screen's
// brightness.
//
// The screen's brightness setting is a ceiling: the brightest the panel
// is allowed to be. The room's light decides how much of that ceiling
// the screen actually gets, so a panel in a dark room is dim and a panel
// by a window in the sun is bright.
//
// The curve is a logarithm, from the dimmest at darkLux to the whole
// ceiling at brightLux. A logarithm because the eye reads light that
// way: a room twice as bright does not look twice as bright.
//
// The reading is smoothed, so a passing shadow does not flicker the
// panel.
//
// It follows TECHO5's approach, which is the same hardware and the same
// sensor.
package autobrightness

import "math"

const (
	// darkLux is the light a dark room reads as.
	//
	// These sensors read a dark room as 0, 1 or 2 depending on the
	// unit, and a curve from zero would put a room at 1 lux a tenth of
	// the ceiling above a room at zero — which is where a dark
	// bedroom's panel stayed too bright. Starting at two also keeps a
	// reading that wavers between one and two from stepping the panel
	// up and down.
	darkLux = 2.0

	// brightLux is the light at which the screen gets the whole ceiling.
	brightLux = 400.0

	// defaultDimmest is the fraction of the ceiling a dark room gets.
	//
	// It is the dimmest setting: the lowest the screen goes in a dark
	// room, as a fraction of what the ceiling allows.
	defaultDimmest = 0.12

	// smooth is how much of the way to the target each step moves.
	//
	// A quarter of the way, so a shadow that passes does not flicker the
	// panel and a change of the room is not a jump.
	smooth = 0.25

	// floor is the dimmest an on-screen backlight goes.
	//
	// It is above the driver's lowest level, because a level of zero
	// switches the PWM off and the panel stops following levels
	// afterwards.
	floor = 1
)

// allowed is the fraction of the ceiling a room this bright gets.
//
// dark is the fraction a dark room gets.
func allowed(lux, dark float64) float64 {
	f := dark + (1-dark)*math.Log10(math.Max(lux, darkLux)/darkLux)/math.Log10(brightLux/darkLux)
	return math.Min(math.Max(f, dark), 1)
}

// Level is the backlight level for a screen that is on.
//
// ceiling is the brightness setting, as a percentage: the brightest the
// panel is allowed to be. lux is the room's light, and auto says whether
// the room is being followed at all.
//
// The result is out of the driver's own range, 0 to 1023.
func Level(ceiling int, auto bool, lux float64) int {
	target := float64(ceiling) * 1023 / 100
	if auto {
		target *= allowed(lux, defaultDimmest)
	}
	return int(math.Max(math.Round(target), floor))
}

// Smoothed is the brightness that moves towards a target.
//
// It is what keeps a passing shadow from flickering the panel: the
// level moves a quarter of the way to the target on each reading
// rather than jumping to it.
//
// The level is the target itself when it has been reached: smoothing
// alone would stop up to half a step short, which rounds a step under
// it.
func Smoothed(level, target float64) float64 {
	next := level + (target-level)*smooth
	if math.Abs(target-next) < 0.5 {
		return target
	}
	return next
}
