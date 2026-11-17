package esphome

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"

	"github.com/flynn/noise"
)

var cipherSuite = noise.NewCipherSuite(
	noise.DH25519,
	noise.CipherChaChaPoly,
	noise.HashSHA256,
)

var noisePrologue = []byte("NoiseAPIInit\x00\x00")

const noiseProtocolSelected byte = 0x01

const maxNameLength = 32

func decodeNoiseKey(encoded string) ([]byte, error) {
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("esphome: the encryption key is not valid base64: %w", err)
	}
	if len(raw) != 32 {
		return nil, fmt.Errorf("esphome: the encryption key is %d bytes, it must be 32", len(raw))
	}
	return raw, nil
}

type noiseConnection struct {
	conn net.Conn

	send *noise.CipherState
	recv *noise.CipherState

	sendNonce uint64
	recvNonce uint64

	handshake *noise.HandshakeState

	pending []byte
}

func newNoiseConnection(conn net.Conn, key []byte) (*noiseConnection, error) {

	state, err := noise.NewHandshakeState(noise.Config{
		CipherSuite:           cipherSuite,
		Random:                rand.Reader,
		Pattern:               noise.HandshakeNN,
		Prologue:              noisePrologue,
		Initiator:             false,
		PresharedKey:          key,
		PresharedKeyPlacement: 0,
	})
	if err != nil {
		return nil, fmt.Errorf("esphome: the noise handshake could not be prepared: %w", err)
	}
	return &noiseConnection{conn: conn, handshake: state}, nil
}

func newInitiatorHandshake(key []byte) (*noise.HandshakeState, error) {
	state, err := noise.NewHandshakeState(noise.Config{
		CipherSuite:           cipherSuite,
		Random:                rand.Reader,
		Pattern:               noise.HandshakeNN,
		Prologue:              noisePrologue,
		Initiator:             true,
		PresharedKey:          key,
		PresharedKeyPlacement: 0,
	})
	if err != nil {
		return nil, fmt.Errorf("esphome: the noise handshake could not be prepared: %w", err)
	}
	return state, nil
}

func (n *noiseConnection) writeServerHello(name, mac string) error {
	if len(name) > maxNameLength {
		name = name[:maxNameLength]
	}
	if len(mac) > maxNameLength {
		mac = mac[:maxNameLength]
	}
	hello := make([]byte, 0, 2+len(name)+len(mac))
	hello = append(hello, noiseProtocolSelected)
	hello = append(hello, name...)
	hello = append(hello, 0)
	hello = append(hello, mac...)
	hello = append(hello, 0)

	return n.writeRawFrame(hello)
}

func (n *noiseConnection) readClientHandshake() error {

	marker := make([]byte, 3)
	if len(n.pending) > 0 {
		copy(marker, n.pending)
		n.pending = nil
	}
	if _, err := io.ReadFull(n.conn, marker[1:]); err != nil {
		return err
	}
	if marker[0] != 0x01 {
		return fmt.Errorf("esphome: unexpected encrypted frame indicator %d", marker[0])
	}
	if length := binary.BigEndian.Uint16(marker[1:3]); length != 0 {
		return fmt.Errorf("esphome: the hello marker should be empty, it is %d bytes", length)
	}

	var header [3]byte
	if _, err := io.ReadFull(n.conn, header[:]); err != nil {
		return err
	}
	if header[0] != 0x01 {
		return fmt.Errorf("esphome: unexpected encrypted frame indicator %d", header[0])
	}
	length := int(binary.BigEndian.Uint16(header[1:3]))
	if length == 0 || length > maxFrameSize {
		return errFrameTooLarge
	}
	message := make([]byte, length)
	if _, err := io.ReadFull(n.conn, message[:]); err != nil {
		return err
	}

	if message[0] != 0 {
		return errors.New("esphome: the handshake frame does not start with a zero byte")
	}

	if _, _, _, err := n.handshake.ReadMessage(nil, message[1:]); err != nil {

		_ = n.writeHandshakeFailure()
		return fmt.Errorf("esphome: the noise handshake failed: %w", err)
	}

	reply, receive, send, err := n.handshake.WriteMessage(nil, nil)
	if err != nil {
		return fmt.Errorf("esphome: the noise handshake could not be answered: %w", err)
	}
	n.recv = receive
	n.send = send

	if err := n.writeRawFrame(append([]byte{0}, reply...)); err != nil {
		return err
	}
	return nil
}

func (n *noiseConnection) writeHandshakeFailure() error {
	message := "Handshake MAC failure"
	length := len(message) + 1
	frame := make([]byte, 0, 4+len(message))
	frame = append(frame, 0x01)
	frame = append(frame, byte(length>>8), byte(length))
	frame = append(frame, 0x01)
	frame = append(frame, message...)
	_, err := n.conn.Write(frame)
	return err
}

func (n *noiseConnection) writeRawFrame(payload []byte) error {
	out := make([]byte, 3)
	out[0] = 0x01
	binary.BigEndian.PutUint16(out[1:3], uint16(len(payload)))
	if _, err := n.conn.Write(out); err != nil {
		return err
	}
	_, err := n.conn.Write(payload)
	return err
}

func (n *noiseConnection) writeEncrypted(messageType uint32, payload []byte) error {
	header := make([]byte, 4)
	binary.BigEndian.PutUint16(header[0:2], uint16(messageType))
	binary.BigEndian.PutUint16(header[2:4], uint16(len(payload)))

	plaintext := make([]byte, 0, len(header)+len(payload))
	plaintext = append(plaintext, header...)
	plaintext = append(plaintext, payload...)

	encrypted, err := n.send.Encrypt(nil, nil, plaintext)
	if err != nil {
		return err
	}
	n.sendNonce++

	out := make([]byte, 3)
	out[0] = 0x01
	binary.BigEndian.PutUint16(out[1:3], uint16(len(encrypted)))
	if _, err := n.conn.Write(out); err != nil {
		return err
	}
	_, err = n.conn.Write(encrypted)
	return err
}

func (n *noiseConnection) readEncrypted() (Frame, error) {
	var header [3]byte
	if _, err := io.ReadFull(n.conn, header[:]); err != nil {
		return Frame{}, err
	}
	if header[0] != 0x01 {
		return Frame{}, fmt.Errorf("esphome: unexpected encrypted frame indicator %d", header[0])
	}
	length := int(binary.BigEndian.Uint16(header[1:3]))
	if length == 0 || length > maxFrameSize {
		return Frame{}, errFrameTooLarge
	}
	encrypted := make([]byte, length)
	if _, err := io.ReadFull(n.conn, encrypted[:]); err != nil {
		return Frame{}, err
	}
	plaintext, err := n.recv.Decrypt(nil, nil, encrypted)
	if err != nil {
		return Frame{}, fmt.Errorf("esphome: the encrypted frame could not be read: %w", err)
	}
	n.recvNonce++
	if len(plaintext) < 4 {
		return Frame{}, errors.New("esphome: the encrypted frame is too short")
	}
	messageType := binary.BigEndian.Uint16(plaintext[0:2])
	bodyLength := int(binary.BigEndian.Uint16(plaintext[2:4]))
	if bodyLength+4 > len(plaintext) {
		return Frame{}, errors.New("esphome: the encrypted frame is shorter than it says")
	}
	body := make([]byte, bodyLength)
	copy(body, plaintext[4:4+bodyLength])
	return Frame{Type: uint32(messageType), Payload: body}, nil
}
