package esphome

import (
	"errors"
	"fmt"
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
// A connection with an encryption key is encrypted: the handshake is
// done and the frames after it are ciphered. A connection without one
// is plaintext, which is what a device with no key does.
func (s *Server) accept(conn net.Conn) (*client, error) {
	if s.encryptionKey == "" {
		return &client{conn: conn}, nil
	}
	key, err := decodeNoiseKey(s.encryptionKey)
	if err != nil {
		return nil, err
	}
	secure, err := newNoiseConnection(conn, key)
	if err != nil {
		return nil, err
	}
	// The server hello comes first, so the client can check it has
	// reached the device it meant to reach.
	if err := secure.writeServerHello(s.name, s.mac); err != nil {
		return nil, fmt.Errorf("esphome: the server hello could not be sent: %w", err)
	}
	if err := secure.readClientHandshake(); err != nil {
		return nil, fmt.Errorf("esphome: the handshake failed: %w", err)
	}
	return &client{conn: conn, secure: secure}, nil
}

// read reads one frame.
func (c *client) read() (Frame, error) {
	if c.secure != nil {
		return c.secure.readEncrypted()
	}
	return readFrame(c.conn)
}

// write writes one frame.
func (c *client) write(messageType uint32, message proto.Message) error {
	payload, err := proto.Marshal(message)
	if err != nil {
		return err
	}
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
				Key:      entity.Key,
				Name:     entity.Name,
				ObjectId: entity.ObjectID,
				Icon:     entity.Icon,
				Mode:     TextMode(entity.Mode),
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
