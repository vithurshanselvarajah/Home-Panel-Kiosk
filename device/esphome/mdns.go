package esphome

import (
	"fmt"
	"log/slog"
	"net"
	"strings"

	"github.com/grandcat/zeroconf"
)

const mdnsServiceType = "_esphomelib._tcp"

const mdnsDomain = "local."

type Advertiser struct {
	server *zeroconf.Server

	log *slog.Logger
}

type AdvertiseOptions struct {
	Name string

	FriendlyName string

	MAC string

	Port int

	Version string

	ProjectName string

	Encrypted bool

	Addresses []net.IP

	Log *slog.Logger
}

func Advertise(options AdvertiseOptions) (*Advertiser, error) {
	if options.Log == nil {
		options.Log = slog.Default()
	}

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

		records = append(records, "api_encryption=1")
	}

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

func announceInterfaces(addresses []net.IP) ([]net.Interface, error) {
	if len(addresses) == 0 {

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

		return nil, nil
	}
	return chosen, nil
}

func (a *Advertiser) Close() {
	if a == nil || a.server == nil {
		return
	}
	a.server.Shutdown()
}
