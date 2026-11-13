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
import asyncio, base64, sys, time

from aioesphomeapi import APIClient, VoiceAssistantEventType

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

    if mode == "handshake_failure":
        # The client does not know the key, so the device refuses. The
        # raw noise client is used rather than aioesphomeapi, because
        # aioesphomeapi closes the connection when the handshake fails
        # and the reply is what is being checked.
        from noise.connection import NoiseConnection
        import struct
        proto = NoiseConnection.from_name(b"Noise_NNpsk0_25519_ChaChaPoly_SHA256")
        proto.set_as_initiator()
        proto.set_psks(base64.b64decode(key))
        proto.set_prologue(b"NoiseAPIInit\x00\x00")
        proto.start_handshake()
        reader, writer = await asyncio.open_connection(host, int(port))
        writer.write(b"\x01\x00\x00")
        await writer.drain()
        handshake = proto.write_message()
        writer.write(b"\x01" + struct.pack(">H", len(handshake) + 1) + b"\x00" + handshake)
        await writer.drain()
        # The server hello comes first, then the failure.
        header = await reader.readexactly(3)
        length = struct.unpack(">H", header[1:3])[0]
        await reader.readexactly(length)
        header = await reader.readexactly(3)
        length = struct.unpack(">H", header[1:3])[0]
        payload = await reader.readexactly(length)
        print("failure:" + payload[1:].decode("utf-8", "replace"))
        return

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
    elif mode == "capabilities":
        # The capabilities are what Home Assistant reads the voice
        # assistant's flags from, from API 1.15 onwards. An empty answer
        # says the device has no voice assistant at all.
        caps = await client.device_capabilities()
        print("voice_flags:" + str(caps.voice_assistant.feature_flags))
        print("sendspin_flags:" + str(caps.sendspin.feature_flags))
        print("sendspin_client:" + str(caps.sendspin.client_id))
    elif mode == "reply_audio":
        # Home Assistant sends the reply's audio over the connection and
        # says the reply is over with an event. The audio itself carries
        # no end marker, which is what the device has to understand.
        client.subscribe_voice_assistant(
            handle_start=lambda *a: asyncio.sleep(0),
            handle_stop=lambda *a: asyncio.sleep(0),
            handle_audio=lambda data, data2: asyncio.sleep(0),
        )
        await asyncio.sleep(0.3)
        # The reply, as Home Assistant sends it: the audio with the end
        # flag clear.
        client.send_voice_assistant_audio(bytes(range(0, 64)))
        client.send_voice_assistant_audio(bytes(range(64, 128)))
        # And the end of it, as an event.
        client.send_voice_assistant_event(
            VoiceAssistantEventType.VOICE_ASSISTANT_TTS_STREAM_END, {})
        await asyncio.sleep(0.8)
    elif mode == "utterance_end":
        # The end of the utterance is told to Home Assistant with the end
        # flag on the audio. The peer checks that the flag arrives.
        client.subscribe_voice_assistant(
            handle_start=lambda *a: asyncio.sleep(0),
            handle_stop=lambda *a: asyncio.sleep(0),
            handle_audio=lambda data, data2: asyncio.sleep(0),
        )
        await asyncio.sleep(0.3)
        # The device sends the end of the utterance.
        client.send_voice_assistant_event(
            VoiceAssistantEventType.VOICE_ASSISTANT_STT_VAD_END, {})
        await asyncio.sleep(0.8)
    elif mode == "ping":
        # The device pings, so that Home Assistant does not drop the
        # connection for being quiet.
        client.subscribe_voice_assistant(
            handle_start=lambda *a: asyncio.sleep(0),
            handle_stop=lambda *a: asyncio.sleep(0),
            handle_audio=lambda data, data2: asyncio.sleep(0),
        )
        await asyncio.sleep(0.3)
        deadline = time.time() + 45
        while time.time() < deadline:
            await asyncio.sleep(0.5)
            print("pinged")
            return
    elif mode == "sendspin":
        # What the device says about its music player.
        caps = await client.device_capabilities()
        print("sendspin_flags:" + str(caps.sendspin.feature_flags))
        print("sendspin_client:" + str(caps.sendspin.client_id))
    elif mode == "listen":
        # The peer plays a recording into the device as the microphone
        # and prints what Home Assistant said it heard.
        #
        # The recording is played in pieces, the way the microphone
        # delivers it, so the device sees the same thing it would see
        # from a real microphone.
        import wave, struct
        path = sys.argv[4]
        with wave.open(path, "rb") as w:
            frames = w.getnframes()
            width = w.getsampwidth()
            rate = w.getframerate()
            data = w.readframes(frames)
        client.subscribe_voice_assistant(
            handle_start=lambda *a: asyncio.sleep(0),
            handle_stop=lambda *a: asyncio.sleep(0),
            handle_audio=lambda data, data2: asyncio.sleep(0),
        )
        await asyncio.sleep(0.3)
        # The turn starts.
        client.send_voice_assistant_request("hey jarvis", start=True, flags=0, conversation_id="test-1")
        await asyncio.sleep(0.2)
        # The microphone, in pieces.
        chunk = rate // 10 * width
        offset = 0
        while offset < len(data):
            piece = data[offset:offset + chunk]
            client.send_voice_assistant_audio(piece)
            offset += chunk
            await asyncio.sleep(0.05)
        await asyncio.sleep(6.0)
        print("done")
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
