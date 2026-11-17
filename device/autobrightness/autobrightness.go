package autobrightness

import "math"

const (
	darkLux = 2.0

	brightLux = 400.0

	defaultDimmest = 0.12

	smooth = 0.25

	floor = 1
)

func allowed(lux, dark float64) float64 {
	f := dark + (1-dark)*math.Log10(math.Max(lux, darkLux)/darkLux)/math.Log10(brightLux/darkLux)
	return math.Min(math.Max(f, dark), 1)
}

func Level(ceiling int, auto bool, lux float64) int {
	target := float64(ceiling) * 1023 / 100
	if auto {
		target *= allowed(lux, defaultDimmest)
	}
	return int(math.Max(math.Round(target), floor))
}

func Smoothed(level, target float64) float64 {
	next := level + (target-level)*smooth
	if math.Abs(target-next) < 0.5 {
		return target
	}
	return next
}
