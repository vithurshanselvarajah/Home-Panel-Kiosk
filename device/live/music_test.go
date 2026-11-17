package live

import (
	"bufio"
	"encoding/json"
	"io"
	"net"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestTheMusicServerPlaysToThePanel(t *testing.T) {
	skipUnlessLive(t, "Home Assistant", haURL(), haToken())

	d := startDevice(t, "")
	d.waitFor("connected to Home Assistant", 90*time.Second, func(s deviceState) bool {
		return s.Connection == "connected"
	})

	state := d.waitFor("the music player is listening", 60*time.Second, func(s deviceState) bool {
		return s.MusicState != ""
	})
	t.Logf("the music player is %q", state.MusicState)

	peer, peerOutput := startSendspinPeer(t, "play", d)
	defer func() {
		if peer != nil && peer.Process != nil {
			_ = peer.Process.Kill()
		}
	}()

	go func() {
		scanner := bufio.NewScanner(peerOutput)
		for scanner.Scan() {
			if line := strings.TrimSpace(scanner.Text()); line != "" {
				t.Logf("the server: %s", line)
			}
		}
	}()

	d.waitFor("playing the server's music", 60*time.Second, func(s deviceState) bool {
		return d.speakerBytes() > 10000
	})
	t.Logf("the panel has played %d bytes", d.speakerBytes())
}

func startSendspinPeer(t *testing.T, mode string, d *device) (*exec.Cmd, io.Reader) {
	t.Helper()
	clientID := os.Getenv("LIVE_SENDSPIN_CLIENT_ID")
	if clientID == "" {

		clientID = liveClientID(t, d)
	}
	cmd := exec.Command("python3", "-c", sendspinPeer, mode, "127.0.0.1:8928", clientID)
	cmd.Env = append(os.Environ(), "PYTHONPATH="+aiosendspinPath())
	output, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}

	errors, err := cmd.StderrPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("the music server could not be started: %v", err)
	}
	go func() {
		scanner := bufio.NewScanner(errors)
		for scanner.Scan() {
			if line := strings.TrimSpace(scanner.Text()); line != "" {
				t.Logf("the server said: %s", line)
			}
		}
	}()
	return cmd, output
}

func liveClientID(t *testing.T, d *device) string {
	t.Helper()
	lines, err := d.peer("capabilities", "")
	if err != nil {
		t.Fatalf("the capabilities could not be asked for: %v", err)
	}
	for _, line := range lines {
		if strings.HasPrefix(line, "sendspin_client:") {
			return strings.TrimPrefix(line, "sendspin_client:")
		}
	}
	return ""
}

func aiosendspinPath() string {
	if path := os.Getenv("LIVE_SENDSPIN_PATH"); path != "" {
		return path
	}
	return "/tmp/ssvenv2/lib/python3.14/site-packages"
}

const sendspinPeer = `
import asyncio, sys, time

from aiosendspin.models.types import ConnectionReason
from aiosendspin.noise.keys import Identity
from aiosendspin.noise.trust_store import InMemoryServerPairingStore, TrustedUnpairedClient
from aiosendspin.server import SendspinServer

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

    if MODE == "play" or MODE == "control":
        # The server says what the transport can do.
        #
        # The panel's buttons are the transport, and a button that the
        # server will not take is a button that does nothing.
        from aiosendspin.models.types import MediaCommand
        controller = client.group.group_role("controller")
        if controller is not None:
            controller.set_supported_commands([
                MediaCommand.PLAY,
                MediaCommand.PAUSE,
                MediaCommand.STOP,
                MediaCommand.NEXT,
                MediaCommand.PREVIOUS,
            ])
            print("the transport was set")

    if MODE == "control":
        # The server takes the controller role, which is what the
        # transport buttons on the panel's drawer drive.
        role = client.group.group_role("controller")
        if role is None:
            print("the controller role was not taken")
            return
        print("the panel took the controller role")

        # The server says what the transport can do, and the panel shows
        # only the buttons that work.
        from aiosendspin.models.types import MediaCommand
        role.set_supported_commands([
            MediaCommand.PLAY,
            MediaCommand.PAUSE,
            MediaCommand.NEXT,
            MediaCommand.PREVIOUS,
            MediaCommand.STOP,
        ])
        print("the transport is", role.supported_commands)

        # The panel is told what the transport can do, and it says so in
        # its state. The server reads the state to find that out.
        await asyncio.sleep(2)

        # The panel's buttons are pressed. The command goes from the
        # panel to the server, and the server is what does it.
        print("waiting for the commands")
        await asyncio.sleep(3)
        print("done")
        return

    if MODE == "play":
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
            sample_rate=rate,
            channels=channels,
            bit_depth=bit_depth,
        )

        push_stream = client.group.start_stream()
        print("the stream was started")

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

    if MODE == "chaos":
        # The server plays something and holds the session open, so the
        # transport commands can be thrown at the panel while the music
        # is playing.
        #
        # The server takes the controller role as well, because the
        # transport commands are for it, and it says what the transport
        # can do so the panel shows the buttons that work.
        from aiosendspin.models.types import MediaCommand

        player_role = client.group.group_role("player")
        metadata_role = client.group.group_role("metadata")
        controller_role = client.group.group_role("controller")
        if player_role is None:
            print("the player role was not taken")
            return
        print("the panel took the player role")
        if controller_role is not None:
            controller_role.set_supported_commands([
                MediaCommand.PLAY,
                MediaCommand.PAUSE,
                MediaCommand.STOP,
                MediaCommand.NEXT,
                MediaCommand.PREVIOUS,
                MediaCommand.SEEK,
            ])
            print("the transport was set")

        import math
        import struct
        from aiosendspin.audio import AudioFormat
        from aiosendspin.server.roles.metadata.state import Metadata

        rate = 48000
        bit_depth = 16
        channels = 2
        fmt = AudioFormat(sample_rate=rate, channels=channels, bit_depth=bit_depth)

        push_stream = client.group.start_stream()
        print("the stream was started")

        block = bytearray()
        for i in range(rate // 10):
            v = int(12000 * math.sin(2 * math.pi * 440 * i / rate))
            block += struct.pack("<hh", v, v)

        if metadata_role is not None:
            metadata_role.set_metadata(Metadata(
                title="Aurora",
                artist="Vithusel",
                album="Chaos",
                track_duration=195000,
                track_progress=0,
                playback_speed=1000,
            ))
            print("the metadata was set")

        # The music goes on for as long as the test needs it.
        #
        # The session is held open by the loop: a server that closed the
        # session would take the panel's music away, and the panel would
        # look broken when the fault was the server's.
        for _ in range(600):
            push_stream.prepare_audio(bytes(block), fmt)
            await push_stream.commit_audio()
            await asyncio.sleep(0.2)
        print("the audio was sent")
        return

asyncio.run(main())
`

func TestTheMusicServerControlsThePanel(t *testing.T) {
	skipUnlessLive(t, "Home Assistant", haURL(), haToken())

	d := startDevice(t, "")
	d.waitFor("connected to Home Assistant", 90*time.Second, func(s deviceState) bool {
		return s.Connection == "connected"
	})

	peer, peerOutput := startSendspinPeer(t, "control", d)
	defer func() {
		if peer != nil && peer.Process != nil {
			_ = peer.Process.Kill()
		}
	}()

	go func() {
		scanner := bufio.NewScanner(peerOutput)
		for scanner.Scan() {
			if line := strings.TrimSpace(scanner.Text()); line != "" {
				t.Logf("the server: %s", line)
			}
		}
	}()

	state := d.waitFor("the transport is known", 60*time.Second, func(s deviceState) bool {
		return len(s.MusicTransport) > 0
	})
	t.Logf("the transport is %v", state.MusicTransport)

	d.sendTransport("pause", "server")
	d.sendTransport("play", "server")
	t.Logf("the transport was driven")
}

func (d *device) sendTransport(action string, source string) {
	d.t.Helper()
	socket := d.controlSocket()
	if socket == "" {
		d.t.Fatal("the device program has no control socket")
	}
	conn, err := net.Dial("unix", socket)
	if err != nil {
		d.t.Fatalf("the control socket could not be opened: %v", err)
	}
	defer conn.Close()

	payload, err := json.Marshal(map[string]any{
		"action": "transport",
		"command": map[string]any{
			"action": action,
			"source": source,
		},
	})
	if err != nil {
		d.t.Fatal(err)
	}
	if _, err := conn.Write(append(payload, '\n')); err != nil {
		d.t.Fatalf("the press could not be sent: %v", err)
	}
}
