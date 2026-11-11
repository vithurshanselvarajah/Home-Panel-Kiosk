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

// The Noise handshake, as ESPHome and Home Assistant speak it.
//
// The pattern is Noise_NNpsk0_25519_ChaChaPoly_SHA256, with the prologue
// "NoiseAPIInit\x00\x00". The device is the responder: Home Assistant
// opens the connection and sends the first handshake message.
//
// The pre-shared key is the encryption key the user puts in the ESPHome
// configuration. It is base64 and it is 32 bytes.
//
// The cipher suite is built here rather than taken from a named
// constructor because the library exposes the parts and the parts are
// what the protocol names: X25519 for the Diffie-Hellman, ChaChaPoly for
// the AEAD and SHA-256 for the hash.
//
// The hash matters: the protocol name is hashed into the handshake, so
// a different hash gives a different key and the handshake fails.
var cipherSuite = noise.NewCipherSuite(
	noise.DH25519,
	noise.CipherChaChaPoly,
	noise.HashSHA256,
)

// The noise protocol name and the prologue.
var (
	noiseProtocolName = []byte("Noise_NNpsk0_25519_ChaChaPoly_SHA256")
	noisePrologue     = []byte("NoiseAPIInit\x00\x00")
)

// noiseProtocolSelected is the byte the server sends to say which
// protocol it chose. Only one exists.
const noiseProtocolSelected byte = 0x01

// The maximum size of a name in the server hello.
const maxNameLength = 32

// decodeNoiseKey turns a base64 key into the 32 bytes the handshake
// needs.
//
// The key is what the user typed into the ESPHome configuration, so a
// wrong one is a plain mistake and the message says so.
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

// noiseConnection is an encrypted connection to one client.
type noiseConnection struct {
	conn net.Conn
	// send and receive are the two cipher states the handshake produces.
	send *noise.CipherState
	recv *noise.CipherState
	// sendNonce and recvNonce count the frames, because a nonce cannot
	// repeat with the same key.
	sendNonce uint64
	recvNonce uint64
	// handshake holds the state until the handshake is done.
	handshake *noise.HandshakeState
	// buffer holds bytes that arrived early.
	buffer []byte
}

// newNoiseConnection prepares an encrypted connection.
//
// The handshake itself is driven by readHandshake and writeHandshake.
func newNoiseConnection(conn net.Conn, key []byte) (*noiseConnection, error) {
	// The NN pattern with the pre-shared key mixed into the first
	// message is what the protocol calls NNpsk0. The library has the
	// pattern and the pre-shared key token, and it puts the token where
	// the placement says, so the name it builds is
	// "Noise_NNpsk0_25519_ChaChaPoly_SHA256" — the name the protocol
	// hashes, and the name the other side hashes too.
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

// newInitiatorHandshake prepares the client half of a handshake.
//
// The device is always the responder, so this is only used by the tests,
// which need both ends.
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

// writeServerHello sends the server hello.
//
// It names the device and gives its MAC address, so the client can check
// that it has reached the device it meant to reach. The client checks
// the name and the address against what it was told to expect, and
// refuses the connection if they do not match.
//
// The hello is a frame that is not encrypted: it is what tells the
// client which protocol was chosen, and it has to be readable before
// the keys exist.
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

	// The frame is the indicator, a sixteen bit length, and then the
	// bytes. The first byte of the payload says which protocol was
	// chosen.
	return n.writeRawFrame(hello)
}

// readClientHandshake reads the client's handshake and answers it.
//
// The client sends two frames. The first is the noise hello marker: an
// indicator, a length of zero, and nothing else. The second carries the
// handshake itself, and it starts with a zero byte that marks it as a
// handshake message rather than an ordinary frame.
//
// The server's answer is two frames as well: the server hello, which
// names the device, and then the handshake response, which is the
// encrypted static key and the tag that proves the client knows the
// pre-shared key.
func (n *noiseConnection) readClientHandshake() error {
	// The hello marker: an indicator and a length of zero.
	var marker [3]byte
	if _, err := io.ReadFull(n.conn, marker[:]); err != nil {
		return err
	}
	if marker[0] != 0x01 {
		return fmt.Errorf("esphome: unexpected encrypted frame indicator %d", marker[0])
	}
	if length := binary.BigEndian.Uint16(marker[1:3]); length != 0 {
		return fmt.Errorf("esphome: the hello marker should be empty, it is %d bytes", length)
	}

	// The handshake frame.
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
	// The first byte of the handshake frame is zero.
	if message[0] != 0 {
		return errors.New("esphome: the handshake frame does not start with a zero byte")
	}

	// Reading the client's message is the first half of the handshake.
	if _, _, _, err := n.handshake.ReadMessage(nil, message[1:]); err != nil {
		return fmt.Errorf("esphome: the noise handshake failed: %w", err)
	}

	// The server's own handshake message is written after the read, and
	// it is what proves to the client that this side knows the key.
	//
	// The cipher states come back from this call, not from the read: the
	// handshake finishes with the server's message, so the write is
	// where they are produced.
	//
	// The states are in the order the pattern defines: the first is for
	// messages travelling from the initiator to the responder, and the
	// second is for the other way. The device is the responder, so the
	// first state is what it receives on and the second is what it
	// sends with.
	reply, receive, send, err := n.handshake.WriteMessage(nil, nil)
	if err != nil {
		return fmt.Errorf("esphome: the noise handshake could not be answered: %w", err)
	}
	n.recv = receive
	n.send = send

	// The reply goes out in the same shape as any other frame, with the
	// zero byte in front that marks it as a handshake message.
	if err := n.writeRawFrame(append([]byte{0}, reply...)); err != nil {
		return err
	}
	return nil
}

// writeRawFrame writes bytes in the encrypted frame shape, without
// encrypting them.
//
// The handshake frames are sent this way: they are the frames that set
// the encryption up, so they cannot be encrypted with it.
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

// writeEncrypted writes one frame on an encrypted connection.
//
// The type and the body are encrypted together, under a header that
// says how long the type is and how long the body is. The header is
// plaintext, because the reader needs it before it can decrypt.
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

// readEncrypted reads one frame from an encrypted connection.
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
