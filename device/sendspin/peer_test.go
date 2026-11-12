package sendspin

import (
	"errors"
	"os/exec"
	"strings"
)

// errSkipPeer says the python peer could not be run.
//
// The peer needs python3 and aiosendspin. Neither is needed to build the
// device, so the tests that use the peer are skipped when it is not
// there.
var errSkipPeer = errors.New("the python peer is not available")

// runPeer runs the python peer and gives back what it printed.
//
// The peer drives aiosendspin, the library Music Assistant speaks the
// protocol with. A peer written for the test would only prove that the
// test agrees with itself.
//
// The client id is the panel's own identity, and the peer is told it so
// the server will take an unpaired session: a server that has not been
// told to trust a client will not play to it, and the test would then be
// about pairing rather than about the metadata.
func runPeer(mode, address, clientID string) ([]string, error) {
	cmd, err := peerCommand(mode, address, clientID)
	if err != nil {
		return nil, err
	}
	out, err := cmd.Output()
	if err != nil {
		return nil, peerError(err)
	}
	return splitLines(string(out)), nil
}

// startPeer runs the python peer in the background.
//
// It is used when the panel has to be watched while the peer is still
// running: the metadata is only there for as long as the music is.
func startPeer(mode, address, clientID string) (*exec.Cmd, error) {
	cmd, err := peerCommand(mode, address, clientID)
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, peerError(err)
	}
	return cmd, nil
}

// peerCommand is the command that runs the peer.
//
// It is errSkipPeer when the python or the library is not there, because
// neither is needed to build the device.
func peerCommand(mode, address, clientID string) (*exec.Cmd, error) {
	python, err := exec.LookPath("python3")
	if err != nil {
		return nil, errSkipPeer
	}
	if err := exec.Command(python, "-c", "import aiosendspin").Run(); err != nil {
		return nil, errSkipPeer
	}
	return exec.Command(python, "-c", sendspinPeerSource, mode, address, clientID), nil
}

// peerError turns the peer's failure into something worth reading.
func peerError(err error) error {
	if err == nil {
		return nil
	}
	if exit, ok := err.(*exec.ExitError); ok {
		return errors.New(strings.TrimSpace(string(exit.Stderr)))
	}
	return err
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

// sendspinPeerSource is the python peer.
//
// It uses aiosendspin's own server, so the handshake, the roles and the
// messages are all the ones Music Assistant uses.
const sendspinPeerSource = `
import asyncio, sys, time

from aiosendspin.models.types import ConnectionReason
from aiosendspin.noise.keys import Identity
from aiosendspin.noise.trust_store import InMemoryServerPairingStore, TrustedUnpairedClient
from aiosendspin.server import SendspinServer
from aiosendspin.server.roles.metadata.state import Metadata

MODE = sys.argv[1]
ADDRESS = sys.argv[2]
CLIENT_ID = sys.argv[3]

async def main():
    # The server will not play to a client it has not been told to trust,
    # so the panel's own identity is trusted before it is connected to.
    store = InMemoryServerPairingStore()
    await store.add_trusted_unpaired(TrustedUnpairedClient(client_id=CLIENT_ID))

    server = SendspinServer(
        loop=asyncio.get_running_loop(),
        identity=Identity.generate(),
        server_name="Test Server",
        pairing_store=store,
        allow_unencrypted=False,
    )
    server.connect_to_client(
        "ws://" + ADDRESS + "/sendspin",
        connection_reason=ConnectionReason.PLAYBACK,
    )

    client = None
    deadline = time.time() + 20
    while time.time() < deadline:
        if server.connected_clients:
            client = server.connected_clients[0]
            break
        await asyncio.sleep(0.1)
    if client is None:
        print("the panel was never reached")
        return

    print("roles=" + ",".join(sorted(client.active_role_ids)))

    if MODE == "play":
        # The server plays something to the panel, which is what a music
        # server does with a player it has found.
        role = client.group.group_role("player")
        if role is None:
            print("the player role was not taken")
            return
        print("the panel took the player role")

        import math
        import struct
        from aiosendspin.audio import AudioFormat

        rate = 48000
        bit_depth = 16
        channels = 2
        fmt = AudioFormat(
            codec="pcm_s16le",
            sample_rate=rate,
            channels=channels,
            bit_depth=bit_depth,
        )

        push_stream = client.group.start_stream()
        print("the stream was started")

        # The audio, a second of a tone.
        block = bytearray()
        for i in range(rate // 10):
            v = int(12000 * math.sin(2 * math.pi * 440 * i / rate))
            block += struct.pack("<hh", v, v)

        for _ in range(20):
            push_stream.prepare_audio(bytes(block), fmt)
            await push_stream.commit_audio()
            await asyncio.sleep(0.1)
        print("the audio was sent")
        await asyncio.sleep(1.0)
        return

    if MODE == "metadata":
        role = client.group.group_role("metadata")
        if role is None:
            print("the metadata role was not taken")
            return
        role.set_metadata(Metadata(
            title="Suck the Blood",
            artist="The Sleepy Jackson",
            album="Fumbling Towards Fitness",
            album_artist="The Sleepy Jackson",
            artwork_url="http://192.168.1.20/cover.jpg",
            year=2001,
            track=3,
            track_duration=195000,
            track_progress=30000,
            playback_speed=1000,
        ))
        await asyncio.sleep(2.0)
        role.set_metadata(None)
        await asyncio.sleep(1.0)
        print("done")
        return

asyncio.run(main())
`
