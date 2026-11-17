package esphome

import (
	"log/slog"
	"net"
	"sync"
	"time"

	"google.golang.org/protobuf/proto"
)

type Server struct {
	registry *Registry

	services *ServiceRegistry

	voice *Voice

	mediaPlayer *MediaPlayer

	sendspin *Sendspin

	onConnect func(connected bool)

	mu sync.Mutex

	listener net.Listener

	mediaPlayerKeyValue uint32

	name string

	friendlyName string

	manufacturer string

	mac string

	version string

	encryptionKey string

	log *slog.Logger

	connections sync.Map
}

type ServerOptions struct {
	Name string

	FriendlyName string

	Manufacturer string

	MAC string

	Version string

	EncryptionKey string

	Registry *Registry

	Services *ServiceRegistry

	Log *slog.Logger
}

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

	server.registry.OnChange(func(entity *Entity) {
		server.pushState(entity)
	})
	return server
}

func (s *Server) OnConnect(fn func(connected bool)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onConnect = fn
}

func (s *Server) reportConnected(connected bool) {
	s.mu.Lock()
	fn := s.onConnect
	s.mu.Unlock()
	if fn != nil {
		fn(connected)
	}
}

func (s *Server) reportLink() {
	open := false
	s.connections.Range(func(_, _ any) bool {
		open = true
		return false
	})
	s.reportConnected(open)
}

func (s *Server) Registry() *Registry { return s.registry }

func (s *Server) SetServices(services *ServiceRegistry) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.services = services
}

func (s *Server) SetSendspin(player *Sendspin) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sendspin = player
}

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

func (s *Server) SetMediaPlayer(player *MediaPlayer) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.mediaPlayer = player
}

func (s *Server) MediaPlayer() *MediaPlayer {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.mediaPlayer
}

func (s *Server) mediaPlayerKey() uint32 {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.mediaPlayerKeyValue == 0 {
		s.mediaPlayerKeyValue = s.registry.NextKeyValue() + 1
	}
	return s.mediaPlayerKeyValue
}

func (s *Server) Services() *ServiceRegistry {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.services
}

func (s *Server) SetVoice(voice *Voice) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.voice = voice
}

func (s *Server) Voice() *Voice {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.voice
}

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

const keepaliveIdle = 30 * time.Second

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

	defer s.reportLink()
	defer s.connections.Delete(client)
	s.reportLink()

	stop := make(chan struct{})
	go client.ping(stop)
	defer close(stop)

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

		return client.write(TypeSendspinPairingTokenResponse, s.sendspinPairingToken())

	case TypeListEntitiesRequest:
		return s.listEntities(client)

	case TypeSubscribeStatesRequest:

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

	if handled, err := s.handleVoiceMessage(client, frame.Type, frame.Payload); handled {
		return err
	}

	s.log.Debug("a message was ignored", "type", frame.Type)
	return nil
}

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

			args[arg.Name] = int(value.Int_)
		case ServiceArgType_SERVICE_ARG_TYPE_FLOAT:
			args[arg.Name] = float64(value.Float_)
		case ServiceArgType_SERVICE_ARG_TYPE_BOOL:
			args[arg.Name] = value.Bool_
		}
	}
	return args, nil
}

func (s *Server) command(client *Client, key uint32, value EntityValue) error {
	s.log.Debug("a command arrived", "key", key, "value", value.String())
	if err := s.registry.HandleCommand(key, value); err != nil {
		s.log.Warn("a command was refused", "key", key, "error", err)
		return nil
	}
	return nil
}

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

		VoiceAssistantFeatureFlags: s.voiceAssistantFlags(),
	}
}

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

func (s *Server) sendspinFlags() uint32 {
	if !s.sendspinPlayer().Enabled() {
		return 0
	}
	return 1 << 0
}

func (s *Server) sendspinPlayer() *Sendspin {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sendspin
}

func (s *Server) sendspinClientID() string {
	return s.sendspinPlayer().ClientID()
}

func (s *Server) sendspinPairingToken() *SendspinPairingTokenResponse {

	if s.encryptionKey == "" {
		return &SendspinPairingTokenResponse{
			Status: SendspinPairingTokenStatus_SENDSPIN_PAIRING_TOKEN_STATUS_ENCRYPTION_REQUIRED,
		}
	}
	player := s.sendspinPlayer()
	if player == nil || !player.Enabled() {
		return &SendspinPairingTokenResponse{
			Status: SendspinPairingTokenStatus_SENDSPIN_PAIRING_TOKEN_STATUS_DISABLED,
		}
	}
	token := player.PairingToken()
	if token == "" {

		return &SendspinPairingTokenResponse{
			Status: SendspinPairingTokenStatus_SENDSPIN_PAIRING_TOKEN_STATUS_NOT_READY,
		}
	}
	return &SendspinPairingTokenResponse{
		Status: SendspinPairingTokenStatus_SENDSPIN_PAIRING_TOKEN_STATUS_OK,
		Token:  token,
	}
}

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

const (
	apiVersionMajor uint32 = 1
	apiVersionMinor uint32 = 16
)

const defaultManufacturer = "Home Panel"
