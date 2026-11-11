package esphome

import (
	"errors"
	"os/exec"
	"strings"
)

// errSkipPeer says the python peer could not be run.
//
// The peer needs python3 and aioesphomeapi. Neither is needed to build
// the device, so the tests that use the peer are skipped when it is not
// there.
var errSkipPeer = errors.New("the python peer is not available")

// runPeer runs the python peer and gives back what it printed.
//
// The peer drives aioesphomeapi, the library Home Assistant speaks the
// protocol with. A peer written for the test would only prove that the
// test agrees with itself.
func runPeer(mode, address, key string) ([]string, error) {
	python, err := exec.LookPath("python3")
	if err != nil {
		return nil, errSkipPeer
	}
	if err := exec.Command(python, "-c", "import aioesphomeapi").Run(); err != nil {
		return nil, errSkipPeer
	}
	out, err := exec.Command(python, "-c", apiPeerSource, mode, address, key).Output()
	if err != nil {
		if exit, ok := err.(*exec.ExitError); ok {
			return nil, errors.New(strings.TrimSpace(string(exit.Stderr)))
		}
		return nil, err
	}
	return splitLines(string(out)), nil
}

// splitLines turns the peer's output into a list.
func splitLines(text string) []string {
	var out []string
	for _, line := range strings.Split(strings.TrimSpace(text), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}

// apiPeerSource is the python peer.
//
// It uses aioesphomeapi's own client, so the handshake, the framing and
// the messages are all the ones Home Assistant uses.
const apiPeerSource = `
import asyncio, sys

from aioesphomeapi import APIClient

async def main():
    mode = sys.argv[1]
    address = sys.argv[2]
    key = sys.argv[3]

    # The mDNS mode does not talk to the device: it looks for it.
    if mode == "mdns":
        # The zeroconf library wants the async form inside a loop.
        from zeroconf import Zeroconf
        from zeroconf.asyncio import AsyncZeroconf
        aiozc = AsyncZeroconf()
        info = await aiozc.async_get_service_info("_esphomelib._tcp.local.", address, timeout=8000)
        if info is None:
            print("not-found")
        else:
            print("port:" + str(info.port))
            for k, v in (info.properties or {}).items():
                if k:
                    print(k.decode() + ":" + v.decode())
        await aiozc.async_close()
        return

    host, port = address.rsplit(":", 1)
    client = APIClient(host, int(port), noise_psk=key or None)
    await client.connect(login=True)

    if mode == "list_entities":
        info, entities, services = await client.device_info_and_list_entities()
        print("name:" + info.name)
        print("friendly:" + info.friendly_name)
        print("mac:" + info.mac_address)
        for entity in entities:
            print("entity:" + entity.name + ":" + type(entity).__name__)
        for service in services:
            print("service:" + service.name)
    elif mode == "states":
        # Subscribe to the state and read what the device sends.
        states = []
        client.subscribe_states(lambda s: states.append(s))
        await asyncio.sleep(1.5)
        for state in states:
            print("state:" + type(state).__name__ + ":" + str(getattr(state, "state", "")))
        # Now change the switch and read the state again.
        for state in states:
            key = getattr(state, "key", None)
            if key is not None and type(state).__name__ == "SwitchState":
                client.switch_command(key=key, state=True)
                await asyncio.sleep(0.8)
                for later in states:
                    print("after:" + type(later).__name__ + ":" + str(getattr(later, "state", "")))
                break
    await client.disconnect()

asyncio.run(main())

`
