package main

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"os"
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
	// mediaPlayer is the speaker.
	//
	// It is the entity the reply to a voice turn comes back on, and the
	// one Music Assistant plays music through.
	mediaPlayer *esphome.MediaPlayer
	// audio is the panel's own audio, so the volume can be set on it.
	audio *deviceAudio
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
	// onSendspinStarted is called when the music player starts.
	//
	// The player's identity goes in the device's capabilities, and it is
	// not known until the player has started.
	onSendspinStarted func()
	// onWakeThreshold is called when the wake word's sensitivity is
	// changed.
	onWakeThreshold func(threshold float64)
	// onWakeEnabled is called when the wake word is switched.
	onWakeEnabled func(enabled bool)
	// onDucking is called when the ducking is changed.
	onDucking func(db float64)
	// onMuted is called when the panel's mute switch is changed.
	onMuted func(muted bool)
	// onLink is called when the link to Home Assistant comes up or goes
	// down.
	//
	// It is what the drawer shows as the connection, and nothing else
	// sets it.
	onLink func(connected bool)
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
	audio *deviceAudio,
) *ControlPlane {
	plane := &ControlPlane{log: log, audio: audio}

	registry := panelEntities(PanelCapabilities{
		CanControlScreen: true,
		HasBacklight:     true,
		HasSlideshow:     true,
	})
	// The voice settings are entities too, so Home Assistant can read
	// them and change them.
	voiceEntities(registry)

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

	// The speaker. Home Assistant does not surface a device at all if it
	// reports no entities, and a voice satellite with no speaker is a
	// satellite with nowhere to speak.
	player := esphome.NewMediaPlayer(log)
	api.SetMediaPlayer(player)
	plane.mediaPlayer = player

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
		// The drawer says whether the panel can reach Home Assistant, and
		// the link is what says. Nothing else sets it, so without this the
		// drawer says "Disconnected" for ever.
		if plane.onLink != nil {
			plane.onLink(connected)
		}
	})

	// The panel's own volume is the speaker's volume.
	//
	// The panel sends it as part of its state, because the panel is what
	// knows what the mixer is doing. So the speaker follows the panel,
	// and the panel follows the speaker, and whichever a person used last
	// is what both report.
	//
	// The one thing that has to be watched for is the two telling each
	// other about the same change for ever. The panel sends its volume
	// when it changes and the speaker sends it when it changes, and
	// each one only acts on what it has just been told, so a change
	// settles rather than going round.
	//
	// The panel's own gain follows too, because the panel plays the
	// music and the reply at its own gain. A speaker that said one level
	// and a panel that played at another would be two settings wearing
	// one name.
	if server != nil {
		server.SetVolumeFollower(func(level int) {
			api.SetMediaPlayerVolume(float32(level) / 100)
			if plane.audio != nil {
				plane.audio.setVolume(level)
			}
		})
	}

	// The services and the entities are what Home Assistant acts on.
	// Each one turns into a command for the panel.
	services := panelServices(func(command ControlCommand) {
		if server != nil {
			server.SendCommand(command)
		}
	})
	api.SetServices(services)
	wireEntities(registry, server, plane, log)

	return plane
}

// wireEntities turns what Home Assistant asks for into what the panel
// is told to do.
//
// The entity is the thing Home Assistant knows: a switch for the
// screen, a number for the brightness, a select for the view. The
// command is the thing the panel understands. This is where the two
// meet.
func wireEntities(registry *esphome.Registry, server *ControlServer, plane *ControlPlane, log *slog.Logger) {
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
			case "wake_word_threshold":
				// The sensitivity is a percentage and the wake word
				// wants a fraction.
				plane.setWakeThreshold(value.Number / 100)
			case "wake_word_enabled":
				plane.setWakeEnabled(value.Bool)
			case "music_ducking_db":
				plane.setDucking(value.Number)
			case "muted":
				plane.setMuted(value.Bool)
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
// panelMAC is the hardware address the device is known by.
//
// Home Assistant identifies a device by this address, so it has to be
// stable: a device that changes its address is a new device, and the old
// one is left behind with all its entities.
//
// It is the hardware address of the interface the panel is on the network
// with — the one that carries the default route — with the last three
// bytes mixed from the device's name. The name is mixed in so that two
// panels on one machine are told apart, and the address of the interface
// is used rather than a made-up one so that the same panel is the same
// device after a restart.
//
// The interface matters: a machine has several, and taking the first one
// found means the address changes when the machine's interfaces are
// enumerated in a different order or when a new one appears. The panel
// announces itself on the interface that carries the default route, and
// the address has to be that interface's.
func panelMAC(name string) string {
	seed := name
	if seed == "" {
		seed = "home-panel"
	}

	// The panel's own address is made up, and it is the same every time.
	//
	// The machine's hardware address is not used, because it changes: a
	// laptop that is on the network by wifi one day and by a cable the
	// next has a different address, and Home Assistant keeps the device
	// under the address it was given. So a panel that took the machine's
	// address would be a new device every time the machine's interfaces
	// changed, and the old one would be left behind.
	//
	// The address is made up instead, and it is made up from the panel's
	// own name, so it is the same on every run and on every machine the
	// panel runs on.
	parts := []string{"02", "00", "00", "00", "00", "00"}

	// The name is mixed into the last three bytes, so two panels on the
	// same machine are told apart.
	sum := uint32(2166136261)
	for i := 0; i < len(seed); i++ {
		sum = (sum ^ uint32(seed[i])) * 16777619
	}
	parts[3] = fmt.Sprintf("%02x", (sum>>16)&0xFF)
	parts[4] = fmt.Sprintf("%02x", (sum>>8)&0xFF)
	parts[5] = fmt.Sprintf("%02x", sum&0xFF)
	return strings.Join(parts, ":")
}

// SendCommand sends a command to the panel.
//
// It is how the panel asks the application to do something the
// application owns — the system volume, for one.
func (p *ControlPlane) SendCommand(command ControlCommand) {
	if p.server == nil {
		return
	}
	p.server.SendCommand(command)
}

// SetMediaPlayerVolume records the speaker's level.
//
// The speaker owns the volume. The panel's own slider and the system
// mixer follow it, and the music server is told about it, because it is
// one setting: the volume of the room.
func (p *ControlPlane) SetMediaPlayerVolume(volume float32) {
	if p.mediaPlayer == nil {
		return
	}
	p.mediaPlayer.SetVolume(volume)
	p.api.PushMediaPlayerState()
}

// SetMediaPlayerMute records whether the speaker is muted.
func (p *ControlPlane) SetMediaPlayerMute(muted bool) {
	if p.mediaPlayer == nil {
		return
	}
	p.mediaPlayer.SetMuted(muted)
	p.api.PushMediaPlayerState()
}

// SetMediaPlayerState records what the speaker is doing and tells Home
// Assistant.
//
// The player card in Home Assistant shows this, so it is pushed as it
// changes rather than being asked for.
func (p *ControlPlane) SetMediaPlayerState(state esphome.MediaPlayerState) {
	if p.mediaPlayer == nil {
		return
	}
	p.mediaPlayer.SetState(state)
	p.api.PushMediaPlayerState()
}

// OnSendspinStarted sets what is called when the music player starts.
//
// The player's identity goes in the device's capabilities, and it is not
// known until the player has started. Home Assistant reads it from there
// and passes it to the music server, which pairs with the player using
// the token the device hands out.
func (p *ControlPlane) OnSendspinStarted(fn func()) {
	p.onSendspinStarted = fn
}

// PushCapabilities tells Home Assistant what the device can do.
//
// The capabilities are asked for when Home Assistant connects, and the
// music player's identity is in them. The player is not running until
// the configuration has been read, so they are pushed when it has.
func (p *ControlPlane) PushCapabilities() {
	if p.api == nil {
		return
	}
	p.api.PushCapabilities()
}

// SetSendspin gives the server the music player.
//
// The music player's identity is in the device's capabilities, because
// that is how a music server tells that the player it found is the
// player Home Assistant already knows about.
func (p *ControlPlane) SetSendspin(player *esphome.Sendspin) {
	if p.api == nil {
		return
	}
	p.api.SetSendspin(player)
}

// AnnounceFinished tells Home Assistant that an announcement has been
// played out.
//
// It is what Home Assistant waits for after it has asked the panel to
// speak. Without it the wait times out and the connection is dropped.
func (p *ControlPlane) AnnounceFinished() {
	if p.api == nil {
		return
	}
	p.api.SendAnnounceFinished()
}

// OnMediaPlayer wires the speaker's callbacks.
//
// The panel holds the sound card, so it is the one that plays the audio
// and the one that knows when it has finished.
func (p *ControlPlane) OnMediaPlayer(hooks MediaPlayerHooks) {
	if p.mediaPlayer == nil {
		return
	}
	p.mediaPlayer.OnPlay(hooks.OnPlay)
	p.mediaPlayer.OnCommand(hooks.OnCommand)
	p.mediaPlayer.OnVolume(hooks.OnVolume)
	p.mediaPlayer.OnMute(hooks.OnMute)
	p.mediaPlayer.OnState(hooks.OnState)
}

// MediaPlayerHooks are the callbacks the speaker makes.
type MediaPlayerHooks struct {
	// OnPlay is called with a URL to play.
	OnPlay func(url string, announcement bool)
	// OnCommand is called with a command.
	OnCommand func(command esphome.MediaPlayerCommand)
	// OnVolume is called with a level, 0.0 to 1.0.
	OnVolume func(volume float32)
	// OnMute is called with whether the player is muted.
	OnMute func(muted bool)
	// OnState is called when the player changes what it is doing.
	OnState func(state esphome.MediaPlayerState)
}

// Transcript says what Home Assistant heard.
//
// The panel shows it while the assistant is thinking about it, so the
// person can see that it heard what they said.
func (p *ControlPlane) Transcript() string {
	if p.voice == nil {
		return ""
	}
	return p.voice.Transcript()
}

// ReplyPlayedOut says the reply has been played out.
//
// The reply is over when the speaker has drained it, and the speaker is
// the one that knows.
func (p *ControlPlane) ReplyPlayedOut() {
	if p.voice == nil {
		return
	}
	p.voice.ReplyPlayedOut()
}

// Spoken says what Home Assistant is saying.
//
// The panel shows it while the reply is being played.
func (p *ControlPlane) Spoken() string {
	if p.voice == nil {
		return ""
	}
	return p.voice.Spoken()
}

// SpokenProgress says how much of the reply has been played.
//
// It is between zero and one, and the panel shows the answer's words
// as far along as it is.
func (p *ControlPlane) SpokenProgress() float64 {
	if p.audio == nil {
		return 0
	}
	return p.audio.spokenProgress()
}

// OnLink sets the callback for the link to Home Assistant.
//
// It is called when the link comes up and when it goes down, and it is
// what the drawer shows as the connection.
func (p *ControlPlane) OnLink(fn func(connected bool)) {
	p.onLink = fn
}

// OnTransport sets the handler for the music transport.
//
// The panel has the transport buttons and the music server has the
// transport, so the button is pressed on the panel and the command
// travels through here to the server.
func (p *ControlPlane) OnTransport(fn func(command TransportCommand)) {
	if p.server == nil {
		return
	}
	p.server.SetTransportHandler(fn)
}

// OnListen sets what is called when something asks for a turn to
// start.
//
// The wake word normally starts one, and the wake word is local. But a
// panel can be asked to start one by something else — an automation, or
// a person pressing a button on the screen — and the turn is the same
// either way.
func (p *ControlPlane) OnListen(fn func(phrase string)) {
	if p.server == nil {
		return
	}
	p.server.SetListenHandler(fn)
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

// SendVoiceEnd says the utterance is over.
//
// It is an empty block of audio with the end flag on it, and it is how
// the device tells home assistant that it has stopped talking.
//
// Without it the pipeline is left open on the device's side, and a turn
// that ends badly can sit there until something times out.
func (p *ControlPlane) SendVoiceEnd() error {
	if p.api == nil {
		return esphome.ErrNoClient
	}
	return p.api.SendVoiceEnd()
}

// setWakeThreshold sets how sensitive the wake word is.
//
// It is the score the wake word has to reach, as a fraction of the
// scale. A panel in a noisy room wants a higher one.
func (p *ControlPlane) setWakeThreshold(threshold float64) {
	if p.onWakeThreshold != nil {
		p.onWakeThreshold(threshold)
	}
	p.publishVoiceSettings()
}

// PublishVoiceSettings puts the voice settings into the entities.
//
// It is called once the configuration has been read, so Home Assistant
// reads the values the device is really using rather than the ones it
// was built with.
func (p *ControlPlane) PublishVoiceSettings() {
	p.publishVoiceSettings()
}

// publishVoiceSettings puts the voice settings into the entities, so
// Home Assistant can read them.
func (p *ControlPlane) publishVoiceSettings() {
	if p.server == nil || p.audio == nil {
		return
	}
	p.server.SetVoiceSettings(
		p.audio.wakeThreshold(),
		p.audio.wakeEnabled(),
		p.audio.duckLevel(),
		p.audio.mutedNow(),
	)
}

// setWakeEnabled switches the wake word on or off.
func (p *ControlPlane) setWakeEnabled(enabled bool) {
	if p.onWakeEnabled != nil {
		p.onWakeEnabled(enabled)
	}
	p.publishVoiceSettings()
}

// setDucking sets how far the music is turned down under a reply.
//
// It is in decibels and it is negative, so the music is quieter than it
// would be rather than louder.
func (p *ControlPlane) setDucking(db float64) {
	if p.onDucking != nil {
		p.onDucking(db)
	}
	p.publishVoiceSettings()
}

// setMuted turns the panel's own mute switch on or off.
func (p *ControlPlane) setMuted(muted bool) {
	if p.onMuted != nil {
		p.onMuted(muted)
	}
	p.publishVoiceSettings()
}

// OnWakeThreshold sets what happens when the wake word's sensitivity
// is changed.
func (p *ControlPlane) OnWakeThreshold(fn func(threshold float64)) {
	p.onWakeThreshold = fn
}

// OnWakeEnabled sets what happens when the wake word is switched on or
// off.
func (p *ControlPlane) OnWakeEnabled(fn func(enabled bool)) {
	p.onWakeEnabled = fn
}

// OnDucking sets what happens when the ducking is changed.
func (p *ControlPlane) OnDucking(fn func(db float64)) {
	p.onDucking = fn
}

// OnMuted sets what happens when the panel's mute switch is changed.
func (p *ControlPlane) OnMuted(fn func(muted bool)) {
	p.onMuted = fn
}

// OnWakeWord sets the callback for the wake word being switched.
//
// It is called when home assistant's picker switches the wake word on or
// off, and when the panel's own settings do the same.
func (p *ControlPlane) OnWakeWord(fn func(enabled bool)) {
	if p.voice == nil {
		return
	}
	p.voice.SetWakeWordCallback(fn)
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
