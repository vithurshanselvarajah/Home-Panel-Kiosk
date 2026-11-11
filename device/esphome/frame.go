// Package esphome speaks the ESPHome native API.
//
// Home Assistant talks to an ESPHome device over this protocol. The
// device is the server: Home Assistant opens the connection and the
// device answers. This package is the device half.
//
// The framing is simple. A plaintext frame is a zero byte, the length of
// the body as a varint, the type of the message as a varint, and then the
// protobuf body. The length covers the body only, not the type.
//
// When the connection is encrypted with a Noise key, the zero byte
// becomes a one byte, the length becomes a big endian sixteen bit number
// and the type and the body are encrypted together.
//
// The protocol is shared with every ESPHome device in the world, so the
// encoding here is checked against aioesphomeapi's own generated types:
// tools/check_wire.py prints the bytes and wire_test.go asserts the same
// values on this side.
package esphome

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

// The message type numbers.
//
// These are part of the wire format and cannot change: Home Assistant
// looks a message up by number, not by name.
const (
	TypeHelloRequest                    uint32 = 1
	TypeHelloResponse                   uint32 = 2
	TypeDisconnectRequest               uint32 = 5
	TypeDisconnectResponse              uint32 = 6
	TypePingRequest                     uint32 = 7
	TypePingResponse                    uint32 = 8
	TypeDeviceInfoRequest               uint32 = 9
	TypeDeviceInfoResponse              uint32 = 10
	TypeListEntitiesRequest             uint32 = 11
	TypeListEntitiesDoneResponse        uint32 = 19
	TypeSubscribeStatesRequest          uint32 = 20
	TypeHomeassistantActionRequest      uint32 = 35
	TypeSubscribeHomeassistantServices  uint32 = 34
	TypeSubscribeHomeAssistantStates    uint32 = 38
	TypeSubscribeHomeAssistantState     uint32 = 39
	TypeHomeAssistantStateResponse      uint32 = 40
	TypeListEntitiesServicesResponse    uint32 = 41
	TypeExecuteServiceRequest           uint32 = 42
	TypeDeviceCapabilitiesRequest       uint32 = 149
	TypeDeviceCapabilitiesResponse      uint32 = 150
	TypeHomeassistantActionResponse     uint32 = 130

	TypeBinarySensorStateResponse uint32 = 21
	TypeSensorStateResponse       uint32 = 25
	TypeSwitchStateResponse       uint32 = 26
	TypeTextSensorStateResponse   uint32 = 27
	TypeNumberStateResponse       uint32 = 50
	TypeSelectStateResponse       uint32 = 53
	TypeTextStateResponse         uint32 = 98

	TypeSwitchCommandRequest uint32 = 33
	TypeNumberCommandRequest uint32 = 51
	TypeSelectCommandRequest uint32 = 54
	TypeButtonCommandRequest uint32 = 62
	TypeTextCommandRequest   uint32 = 99
)

// The entity listing types.
//
// Each entity kind is listed with its own message, and the type number
// is the one Home Assistant matches on.
const (
	TypeListEntitiesBinarySensorResponse uint32 = 12
	TypeListEntitiesSensorResponse       uint32 = 16
	TypeListEntitiesSwitchResponse       uint32 = 17
	TypeListEntitiesTextSensorResponse   uint32 = 18
	TypeListEntitiesNumberResponse       uint32 = 49
	TypeListEntitiesSelectResponse       uint32 = 52
	TypeListEntitiesButtonResponse       uint32 = 61
	TypeListEntitiesTextResponse         uint32 = 97
)

// maxFrameSize is the largest frame the protocol allows.
//
// The length field is a sixteen bit number, so a frame can never be
// larger. The cap matters: without it a peer could ask for a frame of
// any size and the reader would try to hold it all in memory.
const maxFrameSize = 65535

// errFrameTooLarge is returned when a peer announces a frame larger than
// the protocol allows.
var errFrameTooLarge = errors.New("esphome: the frame is larger than the protocol allows")

// readVarint reads a varint from the reader.
//
// A varint is seven bits per byte, with the high bit set on every byte
// but the last. A varint longer than five bytes is a protocol error
// rather than a large number: the length field is a sixteen bit number
// and the type field is small, so nothing legitimate needs more.
func readVarint(r io.Reader) (uint32, error) {
	var value uint32
	for shift := uint(0); shift < 35; shift += 7 {
		var b [1]byte
		if _, err := io.ReadFull(r, b[:]); err != nil {
			return 0, err
		}
		value |= uint32(b[0]&0x7F) << shift
		if b[0]&0x80 == 0 {
			return value, nil
		}
	}
	return 0, errors.New("esphome: the varint is too long")
}

// writeVarint writes a varint.
func writeVarint(w io.Writer, value uint32) error {
	var buf [5]byte
	n := binary.PutUvarint(buf[:], uint64(value))
	_, err := w.Write(buf[:n])
	return err
}

// Frame is one message on the wire.
type Frame struct {
	// Type is the message type number.
	Type uint32
	// Payload is the protobuf body.
	Payload []byte
}

// readFrame reads one frame from a plaintext connection.
//
// The length is read before the type, and both are varints. A length
// above the protocol's cap is refused rather than allocated.
func readFrame(r io.Reader) (Frame, error) {
	var indicator [1]byte
	if _, err := io.ReadFull(r, indicator[:]); err != nil {
		return Frame{}, err
	}
	if indicator[0] != 0 {
		return Frame{}, fmt.Errorf("esphome: unexpected frame indicator %d", indicator[0])
	}
	length, err := readVarint(r)
	if err != nil {
		return Frame{}, err
	}
	if length > maxFrameSize {
		return Frame{}, errFrameTooLarge
	}
	messageType, err := readVarint(r)
	if err != nil {
		return Frame{}, err
	}
	// The length covers the body only. The type has already been read.
	body := make([]byte, length)
	if length > 0 {
		if _, err := io.ReadFull(r, body[:length]); err != nil {
			return Frame{}, err
		}
	}
	return Frame{Type: messageType, Payload: body}, nil
}

// writeFrame writes one frame to a plaintext connection.
func writeFrame(w io.Writer, messageType uint32, payload []byte) error {
	if _, err := w.Write([]byte{0}); err != nil {
		return err
	}
	if err := writeVarint(w, uint32(len(payload))); err != nil {
		return err
	}
	if err := writeVarint(w, messageType); err != nil {
		return err
	}
	if len(payload) == 0 {
		return nil
	}
	_, err := w.Write(payload)
	return err
}
