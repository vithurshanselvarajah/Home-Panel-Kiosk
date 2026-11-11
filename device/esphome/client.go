package esphome

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"sync"

	"google.golang.org/protobuf/proto"
)

// client is one connected Home Assistant.
//
// The connection is either encrypted or it is not, and the two read and
// write frames in different ways. The client holds whichever one it is
// so the server does not have to ask.
type client struct {
	conn net.Conn
	// plain is the connection when it is not encrypted. The first byte
	// has been read already, so the reader has to be given it back.
	plain io.Reader
	// logger is where the messages about the connection go.
	logger *slog.Logger
	// secure is the encrypted connection, when the connection is
	// encrypted.
	secure *noiseConnection
	// writeMu guards the writes.
	//
	// The state is pushed from whichever goroutine changed it, and the
	// answers are written from the goroutine that reads the request.
	// They can happen at the same time, and a frame that is written in
	// two halves by two goroutines is a frame that is wrong.
	writeMu sync.Mutex
	// closed says whether the connection has gone.
	closed bool
}

// accept takes a connection and gets it ready.
//
// How the connection is read depends on what the client does first, and
// the client decides.
//
// A device with a key expects an encrypted connection, and the client
// starts one by sending a one byte. A device with no key but which
// supports encryption answers a one byte as well: it does the
// handshake with the provisioning key, which is all zeros, so the
// client can find out the name of the device without the real key
// crossing the network.
//
// A client that starts with a zero byte is speaking in plain text. A
// device with a key refuses that, because the point of the key is that
// nothing is said in plain text.
//
// The first byte is read before anything else, so the mode is known
// before the server says anything. The server cannot send its hello
// first: the hello is part of the encrypted conversation, and a client
// that is not expecting it would read the wrong bytes.
func (s *Server) accept(conn net.Conn) (*client, error) {
	// The first byte says which conversation this is.
	var indicator [1]byte
	if _, err := io.ReadFull(conn, indicator[:]); err != nil {
		return nil, err
	}
	switch indicator[0] {
	case 0x00:
		// Plain text.
		if s.encryptionKey != "" {
			// The device has a key, so it does not speak in plain
			// text. The client is told so rather than being left
			// waiting: the answer is an error frame with the
			// encrypted indicator, which is what the client reads
			// as "this device needs a key".
			if err := refusePlaintext(conn); err != nil {
				return nil, err
			}
			return nil, errors.New("esphome: the connection is not encrypted and the device has a key")
		}
		// The indicator was read, so it has to be given back to the
		// frame reader: the frame starts with it.
		return &client{conn: conn, plain: io.MultiReader(bytes.NewReader(indicator[:]), conn), logger: s.log}, nil
	case 0x01:
		// Encrypted. The key is the one the device has, or the
		// provisioning key when it has none.
		key := s.encryptionKey
		if key == "" {
			key = provisioningKey
		}
		raw, err := decodeNoiseKey(key)
		if err != nil {
			return nil, err
		}
		secure, err := newNoiseConnection(conn, raw)
		if err != nil {
			return nil, err
		}
		// The indicator has been read, so the handshake has to be
		// given it back.
		secure.pending = indicator[:]
		// The server hello comes first, so the client can check it
		// has reached the device it meant to reach.
		if err := secure.writeServerHello(s.name, s.mac); err != nil {
			return nil, fmt.Errorf("esphome: the server hello could not be sent: %w", err)
		}
		if err := secure.readClientHandshake(); err != nil {
			return nil, fmt.Errorf("esphome: the handshake failed: %w", err)
		}
		return &client{conn: conn, secure: secure, logger: s.log}, nil
	}
	return nil, fmt.Errorf("esphome: unexpected frame indicator %d", indicator[0])
}

// refusePlaintext tells a client that the device needs a key.
//
// The answer is a frame with the encrypted indicator and an error in
// it. A client that is speaking in plain text to a device with a key
// would otherwise wait for a reply that never came, and the wait is
// ten seconds before it gives up.
//
// The frame is the indicator, the length of what follows as a sixteen
// bit number, the number of the error, and the text of the error.
func refusePlaintext(conn net.Conn) error {
	message := "Bad indicator byte"
	// The frame is the indicator, the length of what follows as a
	// sixteen bit number, the number of the error, and the text.
	//
	// The length covers the error number and the text, so it is one
	// more than the length of the text.
	length := len(message) + 1
	frame := make([]byte, 0, 4+len(message))
	frame = append(frame, 0x01)
	frame = append(frame, byte(length>>8), byte(length))
	frame = append(frame, 0x01)
	frame = append(frame, message...)
	_, err := conn.Write(frame)
	return err
}

// provisioningKey is the key the handshake is done with when the device
// has none.
//
// It is all zeros. The point of it is that the client learns the name
// of the device and finds out that a key is needed, and the real key
// never crosses the network in plain text.
//
// The client sends it as part of the handshake, and the handshake
// succeeds, so the client believes it is talking to an encrypted
// device. It is not: the device has no key yet.
const provisioningKey = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="

// log writes a message about the connection.
func (c *client) log(message string, args ...any) {
	if c.logger == nil {
		return
	}
	c.logger.Debug(message, args...)
}

// read reads one frame.
func (c *client) read() (Frame, error) {
	if c.secure != nil {
		return c.secure.readEncrypted()
	}
	return readFrame(c.plain)
}

// write writes one frame.
func (c *client) write(messageType uint32, message proto.Message) error {
	payload, err := proto.Marshal(message)
	if err != nil {
		return err
	}
	c.log("a reply is sent", "type", messageType, "bytes", len(payload))
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if c.closed {
		return errors.New("esphome: the connection is closed")
	}
	if c.secure != nil {
		return c.secure.writeEncrypted(messageType, payload)
	}
	return writeFrame(c.conn, messageType, payload)
}

// disconnect ends the connection politely.
//
// The client is told first, so it does not treat the close as a fault.
func (c *client) disconnect() error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if c.closed {
		return nil
	}
	c.closed = true
	payload, err := proto.Marshal(&DisconnectResponse{})
	if err != nil {
		return err
	}
	if c.secure != nil {
		return c.secure.writeEncrypted(TypeDisconnectResponse, payload)
	}
	return writeFrame(c.conn, TypeDisconnectResponse, payload)
}

// close ends the connection.
func (c *client) close() {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	c.closed = true
	c.conn.Close()
}

// entityMessage builds the message that describes one entity.
//
// Each kind of entity has its own message on the wire, and the message
// carries the key that Home Assistant uses to name it. The kind decides
// the message and the type number that goes with it.
func (s *Server) entityMessage(entity *Entity) (entityMessage, error) {
	switch entity.Kind {
	case KindSwitch:
		return entityMessage{
			Type: TypeListEntitiesSwitchResponse,
			Body: &ListEntitiesSwitchResponse{
				Key:         entity.Key,
				Name:        entity.Name,
				ObjectId:    entity.ObjectID,
				Icon:        entity.Icon,
				DeviceClass: entity.DeviceClass,
			},
		}, nil
	case KindNumber:
		return entityMessage{
			Type: TypeListEntitiesNumberResponse,
			Body: &ListEntitiesNumberResponse{
				Key:         entity.Key,
				Name:        entity.Name,
				ObjectId:    entity.ObjectID,
				Icon:        entity.Icon,
				MinValue:    float32(entity.Min),
				MaxValue:    float32(entity.Max),
				Step:        float32(entity.Step),
				DeviceClass: entity.DeviceClass,
			},
		}, nil
	case KindSelect:
		return entityMessage{
			Type: TypeListEntitiesSelectResponse,
			Body: &ListEntitiesSelectResponse{
				Key:      entity.Key,
				Name:     entity.Name,
				ObjectId: entity.ObjectID,
				Icon:     entity.Icon,
				Options:  entity.Options,
			},
		}, nil
	case KindText:
		return entityMessage{
			Type: TypeListEntitiesTextResponse,
			Body: &ListEntitiesTextResponse{
				Key:       entity.Key,
				Name:      entity.Name,
				ObjectId:  entity.ObjectID,
				Icon:      entity.Icon,
				Mode:      TextMode(entity.Mode),
				MinLength: entity.MinLength,
				MaxLength: entity.MaxLength,
			},
		}, nil
	case KindButton:
		return entityMessage{
			Type: TypeListEntitiesButtonResponse,
			Body: &ListEntitiesButtonResponse{
				Key:         entity.Key,
				Name:        entity.Name,
				ObjectId:    entity.ObjectID,
				Icon:        entity.Icon,
				DeviceClass: entity.DeviceClass,
			},
		}, nil
	case KindBinarySensor:
		return entityMessage{
			Type: TypeListEntitiesBinarySensorResponse,
			Body: &ListEntitiesBinarySensorResponse{
				Key:         entity.Key,
				Name:        entity.Name,
				ObjectId:    entity.ObjectID,
				Icon:        entity.Icon,
				DeviceClass: entity.DeviceClass,
			},
		}, nil
	case KindTextSensor:
		return entityMessage{
			Type: TypeListEntitiesTextSensorResponse,
			Body: &ListEntitiesTextSensorResponse{
				Key:         entity.Key,
				Name:        entity.Name,
				ObjectId:    entity.ObjectID,
				Icon:        entity.Icon,
				DeviceClass: entity.DeviceClass,
			},
		}, nil
	case KindSensor:
		return entityMessage{
			Type: TypeListEntitiesSensorResponse,
			Body: &ListEntitiesSensorResponse{
				Key:               entity.Key,
				Name:              entity.Name,
				ObjectId:          entity.ObjectID,
				Icon:              entity.Icon,
				UnitOfMeasurement: entity.Unit,
				DeviceClass:       entity.DeviceClass,
			},
		}, nil
	}
	return entityMessage{}, fmt.Errorf("esphome: %s is not a kind of entity the protocol has", entity.Kind)
}

// entityMessage is a message about an entity, and the type it is sent
// as.
type entityMessage struct {
	// Type is the message type number.
	Type uint32
	// Body is the message itself.
	Body proto.Message
}
