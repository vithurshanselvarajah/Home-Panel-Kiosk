package esphome

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/grandcat/zeroconf"
)

func TestAdvertiserIsVisible(t *testing.T) {
	advertiser, err := Advertise(AdvertiseOptions{
		Name:         "home-panel-test",
		FriendlyName: "Home Panel Test",
		MAC:          "aabbccddeeff",
		Port:         6053,
		Version:      "0.0.2",
		ProjectName:  "home-panel",
	})
	if err != nil {
		t.Skipf("the network does not allow an announcement: %v", err)
	}
	defer advertiser.Close()

	resolver, err := zeroconf.NewResolver(nil)
	if err != nil {
		t.Skipf("no resolver could be made: %v", err)
	}
	entries := make(chan *zeroconf.ServiceEntry, 4)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	go func() {
		if err := resolver.Lookup(ctx, "home-panel-test."+mdnsServiceType+"."+mdnsDomain, mdnsServiceType, mdnsDomain, entries); err != nil {
			t.Logf("the lookup failed: %v", err)
		}
	}()

	select {
	case entry, ok := <-entries:
		if !ok || entry == nil {
			t.Skip("the Go resolver could not read the answer on this network")
		}
		if entry.Port != 6053 {
			t.Errorf("the entry says the port is %d, it should be 6053", entry.Port)
		}
	case <-ctx.Done():
		t.Skip("the Go resolver could not read the answer on this network")
	}
}

func TestAdvertiserIsVisibleToAnotherResolver(t *testing.T) {
	advertiser, err := Advertise(AdvertiseOptions{
		Name:         "home-panel-other",
		FriendlyName: "Home Panel Other",
		MAC:          "aabbccddeeff",
		Port:         6053,
		Version:      "0.0.2",
		ProjectName:  "home-panel",
	})
	if err != nil {
		t.Skipf("the network does not allow an announcement: %v", err)
	}
	defer advertiser.Close()

	lines, err := runPeer("mdns", "home-panel-other."+mdnsServiceType+"."+mdnsDomain, "")
	if err != nil {
		if errors.Is(err, errSkipPeer) {
			t.Skip("the python peer is not available")
		}
		t.Fatalf("the peer failed: %v", err)
	}
	joined := strings.Join(lines, "\n")
	if strings.Contains(joined, "not-found") {
		t.Fatalf("the device was not found on the network: %v", lines)
	}

	for _, want := range []string{"mac:aabbccddeeff", "friendly_name:Home Panel Other", "project_name:home-panel"} {
		if !strings.Contains(joined, want) {
			t.Errorf("the record %q was not in the announcement: %v", want, lines)
		}
	}
}
