package esphome

import (
	"fmt"
	"log/slog"
	"net"
	"strings"

	"github.com/grandcat/zeroconf"
)

// The mDNS service type an ESPHome device advertises.
//
// Home Assistant watches for this and offers to set the device up when
// it sees one. The type is the name without the domain: the library
// puts the domain on the end.
const mdnsServiceType = "_esphomelib._tcp"

// mdnsDomain is the domain the service is in.
const mdnsDomain = "local."

// Advertiser announces the device on the network.
//
// Home Assistant finds a device by looking for the mDNS service, and
// the records carry what it needs to set the device up: the address, the
// port, the name, and the key when the connection is encrypted.
//
// The address matters as much as the records: Home Assistant keeps
// the device under the address it found, so the one that is advertised
// has to be the one the device can be reached at.
type Advertiser struct {
	// server is the mDNS registration.
	server *zeroconf.Server
	// log is where the messages go.
	log *slog.Logger
}

// AdvertiseOptions is what the advertisement is made with.
type AdvertiseOptions struct {
	// Name is the instance name, which is the device name.
	Name string
	// FriendlyName is the name a person reads.
	FriendlyName string
	// MAC is the address that identifies the device.
	MAC string
	// Port is the port the API is on.
	Port int
	// Version is the firmware version.
	Version string
	// ProjectName is the name of the project the device is.
	ProjectName string
	// Encrypted says whether the connection needs a key.
	Encrypted bool
	// Addresses are the addresses the device can be reached at.
	Addresses []net.IP
	// Log is where the messages go.
	Log *slog.Logger
}

// Advertise announces the device.
//
// The advertisement is not essential: a device can be added to Home
// Assistant by hand, with its address and its port. It is what makes
// the device turn up on its own, though, so it is worth doing.
func Advertise(options AdvertiseOptions) (*Advertiser, error) {
	if options.Log == nil {
		options.Log = slog.Default()
	}
	// The instance name is the device name. The library puts the
	// service type and the domain on the end of it.
	instance := options.Name

	records := []string{
		"version=" + options.Version,
		"project_version=" + options.Version,
		"project_name=" + options.ProjectName,
		"network=ethwifi",
		"mac=" + strings.ToLower(strings.ReplaceAll(options.MAC, ":", "")),
		"friendly_name=" + options.FriendlyName,
	}
	if options.Encrypted {
		// The flag says the connection needs a key. Home Assistant
		// asks for the key when the device is set up.
		records = append(records, "api_encryption=1")
	}

	// The interface the device is announced on is chosen rather than
	// left to the library.
	//
	// The library announces on every interface the machine has, and a
	// machine has more than one: a laptop with Docker has a bridge for
	// each container network, and a Raspberry Pi with a second network
	// has that too. Home Assistant takes the first address it is given
	// and keeps the device there, so an address on a bridge that the
	// assistant cannot reach is an address it will keep failing on.
	//
	// So the interface is chosen: the one that carries the address the
	// device is on the network with.
	interfaces, err := announceInterfaces(options.Addresses)
	if err != nil {
		return nil, err
	}

	server, err := zeroconf.Register(instance, mdnsServiceType, mdnsDomain, options.Port, records, interfaces)
	if err != nil {
		return nil, fmt.Errorf("esphome: the device could not be announced: %w", err)
	}
	options.Log.Info("the device was announced", "name", options.Name, "port", options.Port)
	return &Advertiser{server: server, log: options.Log}, nil
}

// announceInterfaces picks the interfaces the announcement is made on.
//
// The address the device is reached at is the one that matters, and it
// is on one interface. The loopback and the bridges are left out: the
// assistant is on the network and not on them.
func announceInterfaces(addresses []net.IP) ([]net.Interface, error) {
	if len(addresses) == 0 {
		// No address was given, so the library chooses. It announces on
		// everything, which is what it did before.
		return nil, nil
	}
	found, err := net.Interfaces()
	if err != nil {
		return nil, fmt.Errorf("esphome: the network interfaces could not be read: %w", err)
	}
	var chosen []net.Interface
	for _, address := range addresses {
		if address.IsLoopback() || address.IsLinkLocalUnicast() {
			continue
		}
		for _, candidate := range found {
			addrs, err := candidate.Addrs()
			if err != nil {
				continue
			}
			for _, addr := range addrs {
				ip, _, err := net.ParseCIDR(addr.String())
				if err != nil {
					continue
				}
				if ip.Equal(address) {
					chosen = append(chosen, candidate)
				}
			}
		}
	}
	if len(chosen) == 0 {
		// The address is on an interface that could not be found. The
		// announcement is made on everything rather than not at all,
		// because an announcement on the wrong interface is better than
		// no announcement.
		return nil, nil
	}
	return chosen, nil
}

// Close stops the announcement.
func (a *Advertiser) Close() {
	if a == nil || a.server == nil {
		return
	}
	a.server.Shutdown()
}
