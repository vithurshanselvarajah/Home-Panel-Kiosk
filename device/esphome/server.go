package esphome

import (
	"log/slog"
	"net"
	"sync"
	"time"

	"google.golang.org/protobuf/proto"
)

// Server is the device side of the ESPHome native API.
//
// Home Assistant opens a connection to it and asks what the device is
// and what it can do. The server answers, and then pushes the state of
// the entities as it changes.
type Server struct {
	// registry holds the entities the device offers.
	registry *Registry
	// services holds the services the device offers.
	services *ServiceRegistry
	// mu guards the services.
	mu sync.Mutex
	// name is the name Home Assistant shows.
	name string
	// friendlyName is the name a person reads.
	friendlyName string
	// manufacturer is who made the device.
	manufacturer string
	// mac is the address that identifies the device.
	mac string
	// version is the firmware version, which is the app's version.
	version string
	// encryptionKey is the base64 key. It is empty when the connection
	// is not encrypted.
	encryptionKey string
	// log is where the messages go.
	log *slog.Logger
	// connections are the clients that are connected now.
	connections sync.Map
	// writeMu guards the writes on each connection.
	writeMu sync.Mutex
}

// ServerOptions is what the server is made with.
type ServerOptions struct {
	// Name is the device name, as Home Assistant knows it.
	Name string
	// FriendlyName is the name a person reads.
	FriendlyName string
	// Manufacturer is who made the device.
	Manufacturer string
	// MAC is the address that identifies the device.
	MAC string
	// Version is the firmware version.
	Version string
	// EncryptionKey is the base64 Noise key. It is empty when the
	// connection is not encrypted.
	EncryptionKey string
	// Registry holds the entities.
	Registry *Registry
	// Services holds the services. It is made when it is not given.
	Services *ServiceRegistry
	// Log is where the messages go.
	Log *slog.Logger
}

// NewServer makes a server.
//
// The registry is where the entities live, and the server does not make
// any of its own: what the panel offers is the panel's business.
func NewServer(options ServerOptions) *Server {
	if options.Log == nil {
		options.Log = slog.Default()
	}
	if options.Registry == nil {
		options.Registry = NewRegistry()
	}
	if options.Services == nil {
		options.Services = NewServiceRegistry()
	}
	manufacturer := options.Manufacturer
	if manufacturer == "" {
		manufacturer = defaultManufacturer
	}
	server := &Server{
		registry:      options.Registry,
		services:      options.Services,
		name:          options.Name,
		friendlyName:  options.FriendlyName,
		manufacturer:  manufacturer,
		mac:           options.MAC,
		version:       options.Version,
		encryptionKey: options.EncryptionKey,
		log:           options.Log,
	}
	// A state change is pushed to every client that is connected.
	server.registry.OnChange(func(entity *Entity) {
		server.pushState(entity)
	})
	return server
}

// Registry gives access to the entities.
func (s *Server) Registry() *Registry { return s.registry }

// SetServices replaces the services the device offers.
//
// They are set after the server is made, because the services belong to
// the device rather than to the protocol, and the device decides what
// it can do.
func (s *Server) SetServices(services *ServiceRegistry) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.services = services
}

// Services gives access to the services.
func (s *Server) Services() *ServiceRegistry {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.services
}

// Listen serves the API on the given listener.
//
// The listener is already bound, so the caller decides the port and
// the address. The service gives the device a port that no other
// device on the network has, because every device needs one and they
// cannot all have 6053.
func (s *Server) Listen(listener net.Listener) error {
	for {
		conn, err := listener.Accept()
		if err != nil {
			return err
		}
		go s.serve(conn)
	}
}

// serve handles one client.
//
// The client is Home Assistant, or something speaking the same
// protocol. The connection is closed when the client goes away.
func (s *Server) serve(conn net.Conn) {
	defer conn.Close()

	client, err := s.accept(conn)
	if err != nil {
		s.log.Warn("the connection was refused", "error", err)
		return
	}
	defer client.close()

	s.connections.Store(client, struct{}{})
	defer s.connections.Delete(client)

	s.log.Info("home assistant connected", "name", s.name, "address", conn.RemoteAddr())

	// The client asks what the device is, what it can do, and then
	// subscribes to the state. The messages come in that order, but the
	// server answers whatever is asked for, so a client that asks in a
	// different order is not a problem.
	for {
		frame, err := client.read()
		if err != nil {
			s.log.Debug("the connection closed", "error", err)
			return
		}
		s.log.Debug("a message arrived", "type", frame.Type, "bytes", len(frame.Payload))
		if err := s.handle(client, frame); err != nil {
			s.log.Warn("a message could not be answered", "error", err)
			return
		}
	}
}

// handle answers one message from the client.
func (s *Server) handle(client *client, frame Frame) error {
	switch frame.Type {
	case TypeHelloRequest:
		request := &HelloRequest{}
		if err := proto.Unmarshal(frame.Payload, request); err != nil {
			return err
		}
		s.log.Debug("the client said hello", "client", request.ClientInfo)
		return client.write(TypeHelloResponse, &HelloResponse{
			ApiVersionMajor: apiVersionMajor,
			ApiVersionMinor: apiVersionMinor,
			ServerInfo:      s.friendlyName,
		})

	case TypePingRequest:
		return client.write(TypePingResponse, &PingResponse{})

	case TypeDisconnectRequest:
		request := &DisconnectRequest{}
		if err := proto.Unmarshal(frame.Payload, request); err != nil {
			s.log.Info("home assistant disconnected")
			return client.disconnect()
		}
		s.log.Info("home assistant disconnected", "reason", request.Reason.String())
		return client.disconnect()

	case TypeDeviceInfoRequest:
		return client.write(TypeDeviceInfoResponse, s.deviceInfo())

	case TypeDeviceCapabilitiesRequest:
		// The device has none of the optional capabilities: no
		// bluetooth proxy, no voice assistant and no serial proxy. The
		// answer is empty, and Home Assistant reads the absence of a
		// capability as the device not having it.
		return client.write(TypeDeviceCapabilitiesResponse, &DeviceCapabilitiesResponse{})

	case TypeListEntitiesRequest:
		return s.listEntities(client)

	case TypeSubscribeStatesRequest:
		// The client wants the state of everything, now and as it
		// changes. The current state is sent first, so the client has
		// a complete picture before anything changes.
		return s.pushAllStates(client)

	case TypeSwitchCommandRequest:
		request := &SwitchCommandRequest{}
		if err := proto.Unmarshal(frame.Payload, request); err != nil {
			return err
		}
		return s.command(client, request.Key, EntityValue{Kind: KindSwitch, Bool: request.State})

	case TypeNumberCommandRequest:
		request := &NumberCommandRequest{}
		if err := proto.Unmarshal(frame.Payload, request); err != nil {
			return err
		}
		return s.command(client, request.Key, EntityValue{Kind: KindNumber, Number: float64(request.State)})

	case TypeSelectCommandRequest:
		request := &SelectCommandRequest{}
		if err := proto.Unmarshal(frame.Payload, request); err != nil {
			return err
		}
		return s.command(client, request.Key, EntityValue{Kind: KindSelect, Text: request.State})

	case TypeTextCommandRequest:
		request := &TextCommandRequest{}
		if err := proto.Unmarshal(frame.Payload, request); err != nil {
			return err
		}
		return s.command(client, request.Key, EntityValue{Kind: KindText, Text: request.State})

	case TypeButtonCommandRequest:
		request := &ButtonCommandRequest{}
		if err := proto.Unmarshal(frame.Payload, request); err != nil {
			return err
		}
		return s.command(client, request.Key, EntityValue{Kind: KindButton, Bool: true})

	case TypeSubscribeHomeAssistantStates:
		// The client wants to be told what the state of Home
		// Assistant's entities is. The kiosk has no use for that yet,
		// but the request is answered so the client does not wait.
		return nil

	case TypeSubscribeHomeassistantServices:
		return nil

	case TypeExecuteServiceRequest:
		request := &ExecuteServiceRequest{}
		if err := proto.Unmarshal(frame.Payload, request); err != nil {
			return err
		}
		return s.callService(client, request)
	}
	// A message the device does not know is ignored, as the protocol
	// says: a device that answers a message it does not understand is
	// worse than one that says nothing.
	s.log.Debug("a message was ignored", "type", frame.Type)
	return nil
}

// callService runs a service the client asked for.
//
// The arguments come over the wire as a list of typed values, and they
// are put into a map by name so the service can ask for them by name.
func (s *Server) callService(client *client, request *ExecuteServiceRequest) error {
	service := s.services.ByKey(request.Key)
	if service == nil {
		s.log.Warn("a service was asked for that the device does not have", "key", request.Key)
		return nil
	}
	args, err := serviceArguments(service, request.Args)
	if err != nil {
		s.log.Warn("a service was called wrongly", "service", service.Name, "error", err)
		return nil
	}
	s.log.Debug("a service was called", "service", service.Name)
	if err := service.Call(args); err != nil {
		s.log.Warn("a service failed", "service", service.Name, "error", err)
	}
	return nil
}

// serviceArguments turns the arguments on the wire into a map by name.
//
// The wire carries them in order with their kind, and the service knows
// what it asked for, so the two are put together here.
func serviceArguments(service *Service, given []*ExecuteServiceArgument) (map[string]any, error) {
	args := make(map[string]any, len(service.Args))
	for index, arg := range service.Args {
		if index >= len(given) {
			continue
		}
		value := given[index]
		switch arg.Kind {
		case ServiceArgType_SERVICE_ARG_TYPE_STRING:
			args[arg.Name] = value.String_
		case ServiceArgType_SERVICE_ARG_TYPE_INT:
			// The signed field is the one the protocol uses from
			// version 1.3 of the API, and the device speaks that.
			args[arg.Name] = int(value.Int_)
		case ServiceArgType_SERVICE_ARG_TYPE_FLOAT:
			args[arg.Name] = float64(value.Float_)
		case ServiceArgType_SERVICE_ARG_TYPE_BOOL:
			args[arg.Name] = value.Bool_
		}
	}
	return args, nil
}

// command applies a command from the client.
func (s *Server) command(client *client, key uint32, value EntityValue) error {
	s.log.Debug("a command arrived", "key", key, "value", value.String())
	if err := s.registry.HandleCommand(key, value); err != nil {
		s.log.Warn("a command was refused", "key", key, "error", err)
		return nil
	}
	return nil
}

// deviceInfo describes the device to the client.
//
// The project name is the manufacturer and the model joined by a dot,
// which is the shape Home Assistant expects. It takes the two parts
// apart to show them, so a name with no dot in it is a name it cannot
// read.
func (s *Server) deviceInfo() *DeviceInfoResponse {
	return &DeviceInfoResponse{
		Name:           s.name,
		FriendlyName:   s.friendlyName,
		MacAddress:     s.mac,
		EsphomeVersion: s.version,
		ProjectName:    s.manufacturer + "." + s.name,
		ProjectVersion: s.version,
		Manufacturer:   s.manufacturer,
		Model:          s.name,
	}
}

// listEntities tells the client what the device offers.
//
// The entities are listed one message at a time, each with the type
// that matches its kind, and then a message that says the list is
// finished.
func (s *Server) listEntities(client *client) error {
	for _, entity := range s.registry.All() {
		message, err := s.entityMessage(entity)
		if err != nil {
			return err
		}
		if err := client.write(message.Type, message.Body); err != nil {
			return err
		}
	}
	for _, service := range s.services.All() {
		if err := client.write(TypeListEntitiesServicesResponse, s.serviceMessage(service)); err != nil {
			return err
		}
	}
	return client.write(TypeListEntitiesDoneResponse, &ListEntitiesDoneResponse{})
}

// serviceMessage builds the message that describes one service.
//
// The arguments are described with their kind, so Home Assistant knows
// what to ask for: a box for a piece of text, a number for a number,
// and a toggle for true or false.
func (s *Server) serviceMessage(service *Service) *ListEntitiesServicesResponse {
	args := make([]*ListEntitiesServicesArgument, 0, len(service.Args))
	for _, arg := range service.Args {
		args = append(args, &ListEntitiesServicesArgument{
			Name:         arg.Name,
			Type:         arg.Kind,
			Description:  arg.Description,
			Optional:     arg.Optional,
			DefaultValue: arg.Default,
		})
	}
	return &ListEntitiesServicesResponse{
		Name:        service.Name,
		Key:         service.Key,
		Description: service.Description,
		Args:        args,
	}
}

// pushAllStates sends the state of every entity.
func (s *Server) pushAllStates(client *client) error {
	for _, entity := range s.registry.All() {
		if err := s.sendState(client, entity); err != nil {
			return err
		}
	}
	return nil
}

// pushState sends the state of one entity to every client.
func (s *Server) pushState(entity *Entity) {
	s.connections.Range(func(key, _ any) bool {
		connected, ok := key.(*client)
		if !ok {
			return true
		}
		if err := s.sendState(connected, entity); err != nil {
			s.log.Debug("the state could not be pushed", "error", err)
		}
		return true
	})
}

// sendState sends the state of one entity to one client.
func (s *Server) sendState(client *client, entity *Entity) error {
	value := entity.StateOf()
	switch entity.Kind {
	case KindSwitch:
		return client.write(TypeSwitchStateResponse, &SwitchStateResponse{
			Key: entity.Key, State: value.Bool,
		})
	case KindNumber:
		return client.write(TypeNumberStateResponse, &NumberStateResponse{
			Key: entity.Key, State: float32(value.Number),
		})
	case KindSelect:
		return client.write(TypeSelectStateResponse, &SelectStateResponse{
			Key: entity.Key, State: value.Text,
		})
	case KindText:
		return client.write(TypeTextStateResponse, &TextStateResponse{
			Key: entity.Key, State: value.Text,
		})
	case KindSensor:
		return client.write(TypeSensorStateResponse, &SensorStateResponse{
			Key: entity.Key, State: float32(value.Number),
		})
	case KindTextSensor:
		return client.write(TypeTextSensorStateResponse, &TextSensorStateResponse{
			Key: entity.Key, State: value.Text,
		})
	case KindBinarySensor:
		return client.write(TypeBinarySensorStateResponse, &BinarySensorStateResponse{
			Key: entity.Key, State: value.Bool,
		})
	}
	return nil
}

// apiVersion is the version of the protocol the device speaks.
//
// The major version is for changes to the protocol itself and the minor
// version is for changes to the messages within it. Home Assistant
// checks the major version and warns about the minor one.
const (
	apiVersionMajor uint32 = 1
	apiVersionMinor uint32 = 16
)

// defaultManufacturer is who made the device, when nobody says.
//
// The project name is the manufacturer and the model joined by a dot,
// and Home Assistant takes the two parts apart to show them, so there
// has to be a manufacturer.
const defaultManufacturer = "Home Panel"

// keepAlive is how long the server waits for a message before it
// considers the connection dead.
//
// The client pings on its own, so a long silence means the client has
// gone.
const keepAlive = 60 * time.Second
