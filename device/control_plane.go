package main

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/vithurshanselvarajah/Home-Panel-Kiosk/device/esphome"
)

// The control plane.
//
// The panel and the device program are two processes, and this is where
// they meet. The device program owns the Home Assistant connection, so
// it is the one that answers Home Assistant and it is the one that
// holds the entities.
//
// The panel talks to it over a socket, which is where the commands
// come from and where the state goes out. The socket is in memory: it
// is a live reading and not a record.

// ControlPlane is everything the control plane needs.
type ControlPlane struct {
	// server is the socket the panel talks over.
	server *ControlServer
	// advertiser is the announcement on the network.
	advertiser *esphome.Advertiser
	// listener is the Home Assistant API socket.
	listener net.Listener
	// api is the Home Assistant server.
	api *esphome.Server
	// voice is the voice assistant.
	//
	// The panel is a voice satellite: the wake word fires here, the
	// microphone streams to Home Assistant, and the reply comes back as
	// audio on the same connection.
	voice *esphome.Voice
	// client is the Home Assistant connection the voice assistant uses.
	//
	// It is the connection that is open now. A voice turn needs somewhere
	// to send the audio, and there is nowhere to send it when Home
	// Assistant is not listening.
	client *esphome.Client
	// log is where the messages go.
	log *slog.Logger
	// closeOnce makes the closing happen once.
	closeOnce sync.Once
}

// startControlPlane starts the Home Assistant connection and the socket
// the panel talks over.
//
// The port is chosen rather than fixed. Every device on the network
// needs one, and they cannot all have 6053, so the port is counted up
// from there until one is free.
//
// The address for the announcement is the address of the machine on the
// network, not the loopback: Home Assistant connects to the address it
// was told, and the loopback would only work on this machine.
func startControlPlane(
	ctx context.Context,
	log *slog.Logger,
	port int,
	key string,
	name string,
	board string,
	socket string,
) *ControlPlane {
	plane := &ControlPlane{log: log}

	registry := panelEntities(PanelCapabilities{
		CanControlScreen: true,
		HasBacklight:     true,
		HasSlideshow:     true,
	})

	api := esphome.NewServer(esphome.ServerOptions{
		Name:          name,
		FriendlyName:  board,
		MAC:           panelMAC(name),
		Version:       "0.0.2",
		EncryptionKey: key,
		Registry:      registry,
		Log:           log,
	})
	plane.api = api

	// The voice assistant makes the panel a voice satellite.
	//
	// It is the panel that starts the turn, not Home Assistant: the wake
	// word fires here, and the microphone is streamed to Home Assistant
	// for the length of the turn and no longer.
	voice := esphome.NewVoice(esphome.VoiceOptions{Log: log})
	api.SetVoice(voice)
	plane.voice = voice

	// The port has to be free before the announcement says which port
	// it is on, so the listener is made first.
	listener, err := listenForAPI(port)
	if err != nil {
		log.Error("the Home Assistant port could not be opened", "error", err)
		return plane
	}
	plane.listener = listener
	chosen := listener.Addr().(*net.TCPAddr).Port

	go func() {
		if err := api.Listen(listener); err != nil {
			log.Error("the Home Assistant API stopped", "error", err)
		}
	}()
	log.Info("the Home Assistant API is listening", "port", chosen)

	// The announcement tells Home Assistant that the device is there.
	advertiser, err := esphome.Advertise(esphome.AdvertiseOptions{
		Name:         name,
		FriendlyName: board,
		MAC:          panelMAC(name),
		Port:         chosen,
		Version:      "0.0.2",
		ProjectName:  name,
		Encrypted:    key != "",
		Addresses:    panelAddresses(),
		Log:          log,
	})
	if err != nil {
		log.Warn("the device could not be announced", "error", err)
	} else {
		plane.advertiser = advertiser
	}

	// The socket the panel talks over.
	//
	// The commands Home Assistant sends are turned into the commands
	// the panel already understands. The names are the ones the panel has
	// always used, so an automation that was written against the old
	// broker still works.
	//
	// The socket is made before the commands are wired, because the
	// wiring needs it and the socket needs the commands.
	var server *ControlServer
	if socket != "" {
		server = NewControlServer(ControlOptions{
			Socket:   socket,
			Registry: registry,
			Log:      log,
		})
		if err := server.Listen(); err != nil {
			log.Error("the control socket could not be opened", "error", err)
			server = nil
		} else {
			plane.server = server
			log.Info("the control socket is open", "path", socket)
		}
	}

	// The panel shows whether it is linked, and only the server knows:
	// the socket between the panel and this program is on this machine
	// and is up whatever Home Assistant is doing.
	api.OnConnect(func(connected bool) {
		if server != nil {
			server.SetConnected(connected)
		}
	})

	// The services and the entities are what Home Assistant acts on.
	// Each one turns into a command for the panel.
	services := panelServices(func(command ControlCommand) {
		if server != nil {
			server.SendCommand(command)
		}
	})
	api.SetServices(services)
	wireEntities(registry, server, log)

	return plane
}

// wireEntities turns what Home Assistant asks for into what the panel
// is told to do.
//
// The entity is the thing Home Assistant knows: a switch for the
// screen, a number for the brightness, a select for the view. The
// command is the thing the panel understands. This is where the two
// meet.
func wireEntities(registry *esphome.Registry, server *ControlServer, log *slog.Logger) {
	send := func(command ControlCommand) {
		if server == nil {
			return
		}
		server.SendCommand(command)
	}

	for _, entity := range registry.All() {
		entity := entity
		entity.OnCommand(func(value esphome.EntityValue) {
			log.Debug("an entity was changed", "entity", entity.ObjectID, "value", value.String())
			switch entity.ObjectID {
			case "screen":
				// The switch is the screen being on or off.
				if value.Bool {
					send(ControlCommand{Action: "wake"})
				} else {
					send(ControlCommand{Action: "screen_off"})
				}
			case "brightness":
				send(ControlCommand{Action: "brightness", Value: int(value.Number)})
			case "volume":
				send(ControlCommand{Action: "volume", Value: int(value.Number)})
			case "view":
				if value.Text == "slideshow" {
					send(ControlCommand{Action: "slideshow"})
				} else {
					send(ControlCommand{Action: "dashboard"})
				}
			case "dashboard":
				send(ControlCommand{Action: "dashboard"})
			case "display_mode":
				send(ControlCommand{Action: "display_mode", Mode: value.Text})
			case "exit":
				send(ControlCommand{Action: "exit"})
			default:
				log.Debug("an entity was changed and nothing knows what to do", "entity", entity.ObjectID)
			}
		})
	}
}

// listenForAPI opens the API socket.
//
// The port is counted up from the one that is asked for until a free
// one is found. Every ESPHome device on the network has its own port,
// so a fixed one cannot work when there is more than one.
func listenForAPI(port int) (net.Listener, error) {
	if port == 0 {
		port = 6053
	}
	var lastErr error
	for attempt := 0; attempt < 100; attempt++ {
		listener, err := net.Listen("tcp", ":"+strconv.Itoa(port+attempt))
		if err == nil {
			return listener, nil
		}
		lastErr = err
	}
	return nil, fmt.Errorf("home-panel: no port was free: %w", lastErr)
}

// panelAddresses are the addresses the panel can be reached at.
//
// Only the ones that are on the network the assistant is on are wanted.
// A machine has more: a loopback, and a bridge for each container
// network it runs. Home Assistant keeps the device under the address it
// was given, so an address on a bridge it cannot reach is an address
// it will keep failing on.
//
// The address is the one on the interface that carries the default
// route: that is the interface that reaches the outside world, and so
// the one the assistant reaches the panel through.
//
// A tunnel is not that interface. A tunnel carries the addresses of
// the machines it connects to, so it answers for the assistant as well
// as for the panel, and an address found on one is not the panel's
// own.
func panelAddresses() []net.IP {
	interfaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	// The interface that carries the default route is the one the
	// panel is on the network with.
	outward := defaultRouteInterface()
	var addresses []net.IP
	for _, candidate := range interfaces {
		if candidate.Flags&net.FlagUp == 0 || candidate.Flags&net.FlagLoopback != 0 {
			continue
		}
		// A point to point interface is a tunnel.
		if candidate.Flags&net.FlagPointToPoint != 0 {
			continue
		}
		// A bridge carries the addresses of the containers on it.
		if candidate.Flags&net.FlagUp != 0 && isBridge(candidate) {
			continue
		}
		if outward != "" && candidate.Name != outward {
			continue
		}
		addrs, err := candidate.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			ip, _, err := net.ParseCIDR(addr.String())
			if err != nil {
				continue
			}
			if ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
				continue
			}
			addresses = append(addresses, ip)
		}
	}
	return addresses
}

// defaultRouteInterface is the interface that carries the default route.
//
// It is read from the routing table, which is the route that goes
// nowhere in particular and so the one that leaves the machine. The
// address on that interface is the one other machines use to reach
// this one.
func defaultRouteInterface() string {
	routes, err := os.ReadFile("/proc/net/route")
	if err != nil {
		return ""
	}
	lines := strings.Split(string(routes), "\n")
	for _, line := range lines[1:] {
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		// The destination is zero when it is the default route.
		if fields[1] != "00000000" {
			continue
		}
		return fields[0]
	}
	return ""
}

// isBridge says whether an interface is a bridge.
//
// The kernel does not say through a flag, so the name is used. A bridge
// carries the addresses of the containers on it, and those addresses
// mean nothing outside the machine.
func isBridge(candidate net.Interface) bool {
	name := candidate.Name
	return strings.HasPrefix(name, "docker") || strings.HasPrefix(name, "br-") ||
		strings.HasPrefix(name, "virbr") || strings.HasPrefix(name, "veth")
}

// panelMAC is the address that identifies the panel.
//
// Home Assistant keeps the device under this address, so it has to be
// the same every time the panel starts, and it has to be different for
// every panel.
//
// The hardware address of the machine is used when there is one, and
// the panel is told which name it answers to. The name is mixed in
// rather than the name being the whole of it, so two panels on the same
// machine — two kiosks, or a kiosk and a development run — are told
// apart.
func panelMAC(name string) string {
	seed := name
	if seed == "" {
		seed = "home-panel"
	}
	// The hardware address of the machine, when there is one.
	machine := ""
	entries, err := os.ReadDir("/sys/class/net")
	if err == nil {
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			address, err := os.ReadFile(filepath.Join("/sys/class/net", entry.Name(), "address"))
			if err != nil {
				continue
			}
			candidate := strings.TrimSpace(string(address))
			if len(candidate) == 17 && candidate[2] == ':' {
				machine = candidate
				break
			}
		}
	}
	if machine == "" {
		// There is no hardware address, so the host name is used.
		host, err := os.Hostname()
		if err != nil {
			host = "home-panel"
		}
		machine = "02:00:00:00:00:00"
		_ = host
	}
	// The name is mixed into the last three bytes, so two panels on the
	// same machine are told apart.
	sum := uint32(2166136261)
	for i := 0; i < len(seed); i++ {
		sum = (sum ^ uint32(seed[i])) * 16777619
	}
	parts := strings.Split(machine, ":")
	if len(parts) != 6 {
		return machine
	}
	parts[3] = fmt.Sprintf("%02x", (sum>>16)&0xFF)
	parts[4] = fmt.Sprintf("%02x", (sum>>8)&0xFF)
	parts[5] = fmt.Sprintf("%02x", sum&0xFF)
	return strings.Join(parts, ":")
}

// OnVoice wires the voice assistant's callbacks.
//
// The device program owns the microphone and the speaker, so it is the
// one that plays the reply and the one that sends the microphone's
// audio. The protocol only carries it.
func (p *ControlPlane) OnVoice(hooks esphome.VoiceHooks) {
	if p.voice == nil {
		return
	}
	p.voice.SetVoiceHooks(hooks)
}

// StartTurn begins a voice turn.
//
// The phrase is the wake word that fired, or is empty when the turn did
// not start with one.
//
// It fails when Home Assistant is not connected, because there is nowhere
// to send the microphone. The caller logs it and carries on: a panel that
// is not connected is still a panel with a screen.
func (p *ControlPlane) StartTurn(phrase string) error {
	if p.api == nil {
		return esphome.ErrNoClient
	}
	return p.api.StartTurn(phrase)
}

// SendVoiceAudio sends a block of microphone audio.
//
// It is called for every block while a turn is running, and the last one
// carries the end flag.
func (p *ControlPlane) SendVoiceAudio(pcm []byte, end bool) error {
	if p.api == nil {
		return esphome.ErrNoClient
	}
	return p.api.SendVoiceAudio(pcm, end)
}

// SetWakeWord tells the voice assistant what the local wake word is.
//
// It is what Home Assistant's picker shows, and it decides whether the
// panel can start a conversation by itself.
func (p *ControlPlane) SetWakeWord(model string, ready, enabled bool) {
	if p.voice == nil {
		return
	}
	p.voice.SetWakeWord(model, ready, enabled)
}

// Close stops the control plane.
func (p *ControlPlane) Close() {
	p.closeOnce.Do(func() {
		if p.server != nil {
			p.server.Close()
		}
		if p.advertiser != nil {
			p.advertiser.Close()
		}
		if p.listener != nil {
			p.listener.Close()
		}
	})
}

// netDial connects to the control socket.
//
// It is here so the test can reach the socket without reaching into
// the package's own details.
func netDial(path string) (net.Conn, error) {
	return net.Dial("unix", path)
}
