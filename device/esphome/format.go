package esphome

import "strconv"

// formatFloat renders a number the way the protocol expects.
//
// A whole number is rendered without a decimal point, so a brightness of
// 45 reads as "45" and not "45.000000".
func formatFloat(value float64) string {
	if value == float64(int64(value)) {
		return strconv.FormatInt(int64(value), 10)
	}
	return strconv.FormatFloat(value, 'f', -1, 64)
}
