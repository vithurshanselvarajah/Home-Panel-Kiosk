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
	// voice is the voice assistant, when the device has one.
	voice *Voice
	// mediaPlayer is the speaker, when the device has one.
	mediaPlayer *MediaPlayer
	// sendspin is the music player, when the device has one.
	sendspin *Sendspin
	// onConnect is called when Home Assistant connects or disconnects.
	//
	// The panel shows whether it is linked, and only the server knows:
	// the panel is talking to the sidecar over a socket on this machine,
	// which says nothing about whether Home Assistant is talking to the
	// sidecar over the network.
	onConnect func(connected bool)
	// mu guards the services.
	mu sync.Mutex
	// listener is the socket the API is served on.
	//
	// It is kept so the API can be closed, which stops the accepting
	// and frees the port.
	listener net.Listener
	// mediaPlayerKeyValue is the key the speaker is known by.
	mediaPlayerKeyValue uint32
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

// OnConnect sets the callback that is called when Home Assistant
// connects or disconnects.
//
// It is how the panel learns whether it is linked, because the panel
// cannot tell: the socket between the two of them is on this machine
// and is up whatever Home Assistant is doing.
func (s *Server) OnConnect(fn func(connected bool)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onConnect = fn
}

// reportConnected tells the listener whether Home Assistant is there.
func (s *Server) reportConnected(connected bool) {
	s.mu.Lock()
	fn := s.onConnect
	s.mu.Unlock()
	if fn != nil {
		fn(connected)
	}
}

// reportLink tells the listener whether Home Assistant is there.
//
// There can be more than one connection: Home Assistant connects, and
// the panel's own discovery probe connects as well, and the probe goes
// away again.
//
// So the link is reported while *any* connection is open. A connection
// that reported on its own would say "disconnected" when it closed
// while another connection was still up, and the panel would say it
// was disconnected while Home Assistant was talking to it.
func (s *Server) reportLink() {
	open := false
	s.connections.Range(func(_, _ any) bool {
		open = true
		return false
	})
	s.reportConnected(open)
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

// SetSendspin gives the server its music player.
//
// The music player's identity is what Home Assistant pairs a music server
// with, so it has to be in the device's capabilities.
func (s *Server) SetSendspin(player *Sendspin) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sendspin = player
}

// PushCapabilities tells the clients what the device can do.
//
// The capabilities are asked for when a client connects, and they can
// change afterwards: the music player's identity is in them, and the
// player is not running until the configuration has been read.
//
// So they are pushed when they change, or a client that connected
// before the player started would never learn about it.
func (s *Server) PushCapabilities() {
	s.connections.Range(func(_, value any) bool {
		client, ok := value.(*Client)
		if !ok {
			return true
		}
		if err := client.write(TypeDeviceCapabilitiesResponse, &DeviceCapabilitiesResponse{
			VoiceAssistant: &VoiceAssistantCapabilities{
				FeatureFlags: s.voiceAssistantFlags(),
			},
			Sendspin: &SendspinCapabilities{
				FeatureFlags: s.sendspinFlags(),
				ClientId:     s.sendspinClientID(),
			},
		}); err != nil {
			s.log.Debug("the capabilities could not be sent", "error", err)
		}
		return true
	})
}

// SetMediaPlayer gives the server its speaker.
//
// It is set after the server is made, because whether the device has a
// speaker is the device's business.
func (s *Server) SetMediaPlayer(player *MediaPlayer) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.mediaPlayer = player
}

// MediaPlayer gives access to the speaker.
//
// It is nil when the device has no speaker.
func (s *Server) MediaPlayer() *MediaPlayer {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.mediaPlayer
}

// mediaPlayerKey is the key the speaker is known by.
//
// The keys of the entities are handed out by the registry, and the
// speaker is not in the registry: it has its own message and its own
// shape. So it is given a key of its own, past the entities'.
//
// The key has to be stable across a run, because Home Assistant uses it
// to tell one entity from another, and a key that changes is an entity
// that Home Assistant forgets and makes again.
func (s *Server) mediaPlayerKey() uint32 {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.mediaPlayerKeyValue == 0 {
		s.mediaPlayerKeyValue = s.registry.NextKeyValue() + 1
	}
	return s.mediaPlayerKeyValue
}

// Services gives access to the services.
func (s *Server) Services() *ServiceRegistry {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.services
}

// SetVoice gives the server its voice assistant.
//
// It is set after the server is made, because whether the device is a
// voice assistant is the device's business and not the protocol's.
func (s *Server) SetVoice(voice *Voice) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.voice = voice
}

// Voice gives access to the voice assistant.
//
// It is nil when the device is not a voice assistant.
func (s *Server) Voice() *Voice {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.voice
}

// Listen serves the API on the given listener.
//
// The listener is already bound, so the caller decides the port and
// the address. The service gives the device a port that no other
// device on the network has, because every device needs one and they
// cannot all have 6053.
func (s *Server) Listen(listener net.Listener) error {
	s.mu.Lock()
	s.listener = listener
	s.mu.Unlock()
	for {
		conn, err := listener.Accept()
		if err != nil {
			return err
		}
		enableKeepalive(conn)
		go s.serve(conn)
	}
}

// Close stops the API.
//
// It closes the listener, which stops the accepting, and the
// connections that are open, which stops the conversations.
//
// A device that is shutting down closes the API, and a test that has
// run a panel closes it too, so the port is free for the next one.
func (s *Server) Close() error {
	s.mu.Lock()
	listener := s.listener
	s.mu.Unlock()

	var err error
	if listener != nil {
		err = listener.Close()
	}
	s.connections.Range(func(_, value any) bool {
		if client, ok := value.(*Client); ok {
			client.close()
		}
		return true
	})
	return err
}

// keepaliveIdle is how long a connection may be quiet before the
// operating system probes it.
//
// It is for a connection that goes quiet without being closed: a Home
// Assistant that is killed rather than shut down leaves a socket that
// looks open and is not, and without keepalive the device waits on it
// for ever. The keepalive gives the operating system a way to notice
// that the peer has gone.
//
// The value is for "somebody knows something is wrong" rather than for
// the protocol, which has no opinion on it.
const keepaliveIdle = 30 * time.Second

// enableKeepalive turns on TCP keepalive for a connection.
//
// It is best effort: the options are the operating system's, and a
// platform without them gets a connection without keepalive rather than
// an error.
//
// The idle time is what matters and the standard library reaches it.
// The probe count and the probe interval are left at the system's
// defaults, which are a reasonable number of probes at a reasonable
// spacing.
func enableKeepalive(conn net.Conn) {
	tcp, ok := conn.(*net.TCPConn)
	if !ok {
		return
	}
	if err := tcp.SetKeepAlive(true); err != nil {
		return
	}
	_ = tcp.SetKeepAlivePeriod(keepaliveIdle)
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

	s.log.Info("home assistant connected", "name", s.name, "address", conn.RemoteAddr())
	// The link is reported again when the connection closes, and the
	// connection is taken out of the list first, so that the report is
	// about what is left rather than about what is going.
	//
	// The defers run in the reverse order they are written, so the
	// removal is written last and runs first.
	defer s.reportLink()
	defer s.connections.Delete(client)
	s.reportLink()

	// The device pings, so that a connection nobody is using is not
	// taken for a dead one.
	//
	// Home Assistant does not ping: it waits, and after a minute of
	// silence it drops the connection and starts again. So a panel that
	// says nothing is a panel that is reconnected every minute, and
	// everything it is doing is interrupted every minute.
	stop := make(chan struct{})
	go client.ping(stop)
	defer close(stop)

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
func (s *Server) handle(client *Client, frame Frame) error {
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
		// The optional capabilities are answered here.
		//
		// The voice assistant's flags have to be in this answer and not
		// only in the device information, because from API 1.15 onwards
		// this is where Home Assistant reads them from. An empty answer
		// says the device has no voice assistant at all, and Home
		// Assistant then never sends the reply audio over the connection.
		return client.write(TypeDeviceCapabilitiesResponse, &DeviceCapabilitiesResponse{
			VoiceAssistant: &VoiceAssistantCapabilities{
				FeatureFlags: s.voiceAssistantFlags(),
			},
			Sendspin: &SendspinCapabilities{
				FeatureFlags: s.sendspinFlags(),
				ClientId:     s.sendspinClientID(),
			},
		})

	case TypeSendspinPairingTokenRequest:
		// The music server wants the token it pairs with.
		//
		// Home Assistant fetches it from the device and passes it on to
		// the music server, which pairs with the player using it. A
		// device that says it hands out a token and then does not is a
		// device a music server never pairs with.
		return client.write(TypeSendspinPairingTokenResponse, s.sendspinPairingToken())

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

	case TypeMediaPlayerCommandRequest:
		request := &MediaPlayerCommandRequest{}
		if err := proto.Unmarshal(frame.Payload, request); err != nil {
			return err
		}
		if s.mediaPlayer != nil {
			s.mediaPlayer.HandleCommand(s.mediaPlayerKey(), request)
			s.pushMediaPlayerState()
		}
		return nil

	case TypeExecuteServiceRequest:
		request := &ExecuteServiceRequest{}
		if err := proto.Unmarshal(frame.Payload, request); err != nil {
			return err
		}
		return s.callService(client, request)
	}

	// The voice assistant's messages are handled apart, because there are
	// enough of them to be worth keeping out of the way.
	if handled, err := s.handleVoiceMessage(client, frame.Type, frame.Payload); handled {
		return err
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
func (s *Server) callService(client *Client, request *ExecuteServiceRequest) error {
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
func (s *Server) command(client *Client, key uint32, value EntityValue) error {
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
		// The voice assistant flags say what the device can do as a
		// voice satellite. They are read once, when home assistant
		// connects, so they are decided once too.
		VoiceAssistantFeatureFlags: s.voiceAssistantFlags(),
	}
}

// listEntities tells the client what the device offers.
//
// The entities are listed one message at a time, each with the type
// that matches its kind, and then a message that says the list is
// finished.
func (s *Server) listEntities(client *Client) error {
	for _, entity := range s.registry.All() {
		message, err := s.entityMessage(entity)
		if err != nil {
			return err
		}
		if err := client.write(message.Type, message.Body); err != nil {
			return err
		}
	}
	// The speaker is listed apart from the entities, because it has its
	// own message and its own key.
	if s.mediaPlayer != nil {
		key := s.mediaPlayerKey()
		message, err := s.mediaPlayer.Message(key)
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
func (s *Server) pushAllStates(client *Client) error {
	for _, entity := range s.registry.All() {
		if err := s.sendState(client, entity); err != nil {
			return err
		}
	}
	if s.mediaPlayer != nil {
		if err := client.write(TypeMediaPlayerStateResponse, s.mediaPlayer.StateMessage(s.mediaPlayerKey())); err != nil {
			return err
		}
	}
	return nil
}

// SendAnnounceFinished tells Home Assistant that an announcement has been
// played out.
//
// It is what Home Assistant waits for after it has asked the panel to
// speak, and without it the wait times out and the connection is
// dropped.
func (s *Server) SendAnnounceFinished() {
	s.connections.Range(func(key, _ any) bool {
		client, ok := key.(*Client)
		if !ok {
			return true
		}
		if err := s.sendAnnounceFinished(client); err != nil {
			s.log.Debug("the announcement could not be finished", "error", err)
		}
		return true
	})
}

// sendspinFlags are the capabilities of the music player.
//
// The only one is the pairing token: the flag says the device hands out
// the token that a music server pairs with, and Home Assistant passes it
// on to the server.
func (s *Server) sendspinFlags() uint32 {
	if !s.sendspinPlayer().Enabled() {
		return 0
	}
	return 1 << 0
}

// sendspinPlayer is the music player, if the device has one.
//
// It is read under the lock, because it is set after the server is made
// and read from the goroutines that answer the clients.
func (s *Server) sendspinPlayer() *Sendspin {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sendspin
}

// sendspinClientID is the music player's own identity.
//
// It is empty until the player has started once, and Home Assistant reads
// an empty one as "not started yet" rather than as a broken device.
func (s *Server) sendspinClientID() string {
	return s.sendspinPlayer().ClientID()
}

// sendspinPairingToken is the answer to a request for the token a
// music server pairs with.
//
// The status says what happened. The token is only given when there is
// one: a player that has not started has no token, and saying so is
// better than saying "not ready" for ever, because the music server
// asks again later.
func (s *Server) sendspinPairingToken() *SendspinPairingTokenResponse {
	player := s.sendspinPlayer()
	if player == nil || !player.Enabled() {
		return &SendspinPairingTokenResponse{
			Status: SendspinPairingTokenStatus_SENDSPIN_PAIRING_TOKEN_STATUS_DISABLED,
		}
	}
	token := player.PairingToken()
	if token == "" {
		// The player has not started yet, so the token does not exist
		// yet. The music server asks again later.
		return &SendspinPairingTokenResponse{
			Status: SendspinPairingTokenStatus_SENDSPIN_PAIRING_TOKEN_STATUS_NOT_READY,
		}
	}
	return &SendspinPairingTokenResponse{
		Status: SendspinPairingTokenStatus_SENDSPIN_PAIRING_TOKEN_STATUS_OK,
		Token:  token,
	}
}

// PushMediaPlayerState sends the speaker's state to every client.
//
// It is exported so the device program can report what the speaker is
// doing when the change comes from the panel rather than from Home
// Assistant.
// SetMediaPlayerVolume records the speaker's level.
//
// The speaker owns the volume. The panel's own slider and the system
// mixer follow it, and the music server is told about it, because it is
// one setting: the volume of the room.
func (s *Server) SetMediaPlayerVolume(volume float32) {
	if s.mediaPlayer == nil {
		return
	}
	s.mediaPlayer.SetVolume(volume)
	s.PushMediaPlayerState()
}

func (s *Server) PushMediaPlayerState() {
	s.pushMediaPlayerState()
}

// pushMediaPlayerState sends the speaker's state to every client.
func (s *Server) pushMediaPlayerState() {
	if s.mediaPlayer == nil {
		return
	}
	message := s.mediaPlayer.StateMessage(s.mediaPlayerKey())
	s.connections.Range(func(key, _ any) bool {
		connected, ok := key.(*Client)
		if !ok {
			return true
		}
		if err := connected.write(TypeMediaPlayerStateResponse, message); err != nil {
			s.log.Debug("the speaker's state could not be pushed", "error", err)
		}
		return true
	})
}

// pushState sends the state of one entity to every client.
func (s *Server) pushState(entity *Entity) {
	s.connections.Range(func(key, _ any) bool {
		connected, ok := key.(*Client)
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
func (s *Server) sendState(client *Client, entity *Entity) error {
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
