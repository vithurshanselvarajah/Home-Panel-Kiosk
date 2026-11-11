package esphome

import "errors"

// The errors the tests compare against.
var (
	errWrongType = errors.New("esphome: the frame has the wrong type")
	errWrongBody = errors.New("esphome: the frame has the wrong body")
)
