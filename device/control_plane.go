package main

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strconv"
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
		MAC:           panelMAC(),
		Version:       "0.0.2",
		EncryptionKey: key,
		Registry:      registry,
		Log:           log,
	})
	plane.api = api

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
		MAC:          panelMAC(),
		Port:         chosen,
		Version:      "0.0.2",
		ProjectName:  name,
		Encrypted:    key != "",
		Log:          log,
	})
	if err != nil {
		log.Warn("the device could not be announced", "error", err)
	} else {
		plane.advertiser = advertiser
	}

	// The socket the panel talks over.
	if socket != "" {
		server := NewControlServer(ControlOptions{
			Socket:   socket,
			Registry: registry,
			Log:      log,
			OnCommand: func(command ControlCommand) {
				log.Info("the panel asked for something", "action", command.Action)
			},
		})
		if err := server.Listen(); err != nil {
			log.Error("the control socket could not be opened", "error", err)
		} else {
			plane.server = server
			log.Info("the control socket is open", "path", socket)
		}
	}
	return plane
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

// panelMAC is the address that identifies the panel.
//
// It is taken from the machine's own hardware when there is one, and
// made up when there is not: a device with no network card still has
// to be told apart from every other device.
//
// The address is what Home Assistant keeps the device under, so it has
// to be the same every time the panel starts.
func panelMAC() string {
	for _, path := range []string{"/sys/class/net"} {
		entries, err := os.ReadDir(path)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			address, err := os.ReadFile(filepath.Join(path, entry.Name(), "address"))
			if err != nil {
				continue
			}
			mac := string(address)
			if len(mac) == 17 && mac[2] == ':' {
				return mac
			}
		}
	}
	// There is no hardware address, so one is made from the host name.
	// It has to look like an address, because Home Assistant treats it
	// as one.
	host, err := os.Hostname()
	if err != nil {
		host = "home-panel"
	}
	var sum uint32
	for i := 0; i < len(host); i++ {
		sum = sum*31 + uint32(host[i])
	}
	return fmt.Sprintf("02:00:00:%02x:%02x:%02x",
		sum&0xFF, (sum>>8)&0xFF, (sum>>16)&0xFF)
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
