package esphome

import "errors"

var (
	errWrongIndicator = errors.New("esphome: the frame has the wrong indicator")
	errWrongType      = errors.New("esphome: the frame has the wrong type")
	errWrongBody      = errors.New("esphome: the frame has the wrong body")
)
