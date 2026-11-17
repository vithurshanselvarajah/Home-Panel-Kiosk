package esphome

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"sync"
	"time"

	"google.golang.org/protobuf/proto"
)

type Client struct {
	conn net.Conn

	plain io.Reader

	logger *slog.Logger

	secure *noiseConnection

	writeMu sync.Mutex

	closed bool
}

func (s *Server) accept(conn net.Conn) (*Client, error) {

	var indicator [1]byte
	if _, err := io.ReadFull(conn, indicator[:]); err != nil {
		return nil, err
	}
	switch indicator[0] {
	case 0x00:

		if s.encryptionKey != "" {

			if err := refusePlaintext(conn); err != nil {
				return nil, err
			}
			return nil, errors.New("esphome: the connection is not encrypted and the device has a key")
		}

		return &Client{conn: conn, plain: io.MultiReader(bytes.NewReader(indicator[:]), conn), logger: s.log}, nil
	case 0x01:

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

		secure.pending = indicator[:]

		if err := secure.writeServerHello(s.name, s.mac); err != nil {
			return nil, fmt.Errorf("esphome: the server hello could not be sent: %w", err)
		}
		if err := secure.readClientHandshake(); err != nil {
			return nil, fmt.Errorf("esphome: the handshake failed: %w", err)
		}
		return &Client{conn: conn, secure: secure, logger: s.log}, nil
	}
	return nil, fmt.Errorf("esphome: unexpected frame indicator %d", indicator[0])
}

func refusePlaintext(conn net.Conn) error {
	message := "Bad indicator byte"

	length := len(message) + 1
	frame := make([]byte, 0, 4+len(message))
	frame = append(frame, 0x01)
	frame = append(frame, byte(length>>8), byte(length))
	frame = append(frame, 0x01)
	frame = append(frame, message...)
	_, err := conn.Write(frame)
	return err
}

const provisioningKey = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="

func (c *Client) log(message string, args ...any) {
	if c.logger == nil {
		return
	}
	c.logger.Debug(message, args...)
}

func (c *Client) read() (Frame, error) {
	if c.secure != nil {
		return c.secure.readEncrypted()
	}
	return readFrame(c.plain)
}

func (c *Client) write(messageType uint32, message proto.Message) error {
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

const pingInterval = 30 * time.Second

func (c *Client) ping(stop <-chan struct{}) {
	ticker := time.NewTicker(pingInterval)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			if err := c.write(TypePingRequest, &PingRequest{}); err != nil {
				return
			}
		}
	}
}

func (c *Client) disconnect() error {
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

func (c *Client) close() {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	c.closed = true
	c.conn.Close()
}

func (s *Server) entityMessage(entity *Entity) (entityMessage, error) {
	switch entity.Kind {
	case KindSwitch:
		return entityMessage{
			Type: TypeListEntitiesSwitchResponse,
			Body: &ListEntitiesSwitchResponse{
				Key:            entity.Key,
				Name:           entity.Name,
				ObjectId:       entity.ObjectID,
				Icon:           entity.Icon,
				DeviceClass:    entity.DeviceClass,
				EntityCategory: entity.Category,
			},
		}, nil
	case KindNumber:
		return entityMessage{
			Type: TypeListEntitiesNumberResponse,
			Body: &ListEntitiesNumberResponse{
				Key:            entity.Key,
				Name:           entity.Name,
				ObjectId:       entity.ObjectID,
				Icon:           entity.Icon,
				MinValue:       float32(entity.Min),
				MaxValue:       float32(entity.Max),
				Step:           float32(entity.Step),
				DeviceClass:    entity.DeviceClass,
				EntityCategory: entity.Category,
			},
		}, nil
	case KindSelect:
		return entityMessage{
			Type: TypeListEntitiesSelectResponse,
			Body: &ListEntitiesSelectResponse{
				Key:            entity.Key,
				Name:           entity.Name,
				ObjectId:       entity.ObjectID,
				Icon:           entity.Icon,
				Options:        entity.Options,
				EntityCategory: entity.Category,
			},
		}, nil
	case KindText:
		return entityMessage{
			Type: TypeListEntitiesTextResponse,
			Body: &ListEntitiesTextResponse{
				Key:            entity.Key,
				Name:           entity.Name,
				ObjectId:       entity.ObjectID,
				Icon:           entity.Icon,
				Mode:           TextMode(entity.Mode),
				MinLength:      entity.MinLength,
				MaxLength:      entity.MaxLength,
				EntityCategory: entity.Category,
			},
		}, nil
	case KindButton:
		return entityMessage{
			Type: TypeListEntitiesButtonResponse,
			Body: &ListEntitiesButtonResponse{
				Key:            entity.Key,
				Name:           entity.Name,
				ObjectId:       entity.ObjectID,
				Icon:           entity.Icon,
				DeviceClass:    entity.DeviceClass,
				EntityCategory: entity.Category,
			},
		}, nil
	case KindBinarySensor:
		return entityMessage{
			Type: TypeListEntitiesBinarySensorResponse,
			Body: &ListEntitiesBinarySensorResponse{
				Key:            entity.Key,
				Name:           entity.Name,
				ObjectId:       entity.ObjectID,
				Icon:           entity.Icon,
				DeviceClass:    entity.DeviceClass,
				EntityCategory: entity.Category,
			},
		}, nil
	case KindTextSensor:
		return entityMessage{
			Type: TypeListEntitiesTextSensorResponse,
			Body: &ListEntitiesTextSensorResponse{
				Key:            entity.Key,
				Name:           entity.Name,
				ObjectId:       entity.ObjectID,
				Icon:           entity.Icon,
				DeviceClass:    entity.DeviceClass,
				EntityCategory: entity.Category,
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
				EntityCategory:    entity.Category,
			},
		}, nil
	}
	return entityMessage{}, fmt.Errorf("esphome: %s is not a kind of entity the protocol has", entity.Kind)
}

type entityMessage struct {
	Type uint32

	Body proto.Message
}
