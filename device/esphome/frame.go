package esphome

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

const (
	TypeHelloRequest                   uint32 = 1
	TypeHelloResponse                  uint32 = 2
	TypeDisconnectRequest              uint32 = 5
	TypeDisconnectResponse             uint32 = 6
	TypePingRequest                    uint32 = 7
	TypePingResponse                   uint32 = 8
	TypeDeviceInfoRequest              uint32 = 9
	TypeDeviceInfoResponse             uint32 = 10
	TypeListEntitiesRequest            uint32 = 11
	TypeListEntitiesDoneResponse       uint32 = 19
	TypeSubscribeStatesRequest         uint32 = 20
	TypeHomeassistantActionRequest     uint32 = 35
	TypeSubscribeHomeassistantServices uint32 = 34
	TypeSubscribeHomeAssistantStates   uint32 = 38
	TypeSubscribeHomeAssistantState    uint32 = 39
	TypeHomeAssistantStateResponse     uint32 = 40
	TypeListEntitiesServicesResponse   uint32 = 41
	TypeExecuteServiceRequest          uint32 = 42
	TypeDeviceCapabilitiesRequest      uint32 = 149
	TypeDeviceCapabilitiesResponse     uint32 = 150
	TypeSendspinPairingTokenRequest    uint32 = 159
	TypeSendspinPairingTokenResponse   uint32 = 160
	TypeHomeassistantActionResponse    uint32 = 130

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

const (
	TypeListEntitiesBinarySensorResponse uint32 = 12
	TypeListEntitiesSensorResponse       uint32 = 16
	TypeListEntitiesSwitchResponse       uint32 = 17
	TypeListEntitiesTextSensorResponse   uint32 = 18
	TypeListEntitiesNumberResponse       uint32 = 49
	TypeListEntitiesSelectResponse       uint32 = 52
	TypeListEntitiesButtonResponse       uint32 = 61
	TypeListEntitiesTextResponse         uint32 = 97
	TypeListEntitiesMediaPlayerResponse  uint32 = 63
	TypeMediaPlayerCommandRequest        uint32 = 65
	TypeMediaPlayerStateResponse         uint32 = 64
)

const maxFrameSize = 65535

var errFrameTooLarge = errors.New("esphome: the frame is larger than the protocol allows")

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

func writeVarint(w io.Writer, value uint32) error {
	var buf [5]byte
	n := binary.PutUvarint(buf[:], uint64(value))
	_, err := w.Write(buf[:n])
	return err
}

type Frame struct {
	Type uint32

	Payload []byte
}

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

	body := make([]byte, length)
	if length > 0 {
		if _, err := io.ReadFull(r, body[:length]); err != nil {
			return Frame{}, err
		}
	}
	return Frame{Type: messageType, Payload: body}, nil
}

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
