package esphome

import (
	"encoding/base64"
	"io"
	"net"
	"os"
	"os/exec"
	"testing"
	"time"
)

func TestHandshakeAgainstAioesphomeapi(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 is not available")
	}
	if err := exec.Command(python, "-c", "import aioesphomeapi").Run(); err != nil {
		t.Skip("aioesphomeapi is not installed")
	}

	key := base64.StdEncoding.EncodeToString(make([]byte, 32))

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	type result struct {
		frameType uint32
		payload   []byte
		err       error
	}
	results := make(chan result, 1)

	go func() {
		conn, err := listener.Accept()
		if err != nil {
			results <- result{err: err}
			return
		}
		defer conn.Close()

		raw, err := decodeNoiseKey(key)
		if err != nil {
			results <- result{err: err}
			return
		}
		secure, err := newNoiseConnection(conn, raw)
		if err != nil {
			results <- result{err: err}
			return
		}

		var indicator [1]byte
		if _, err := io.ReadFull(conn, indicator[:]); err != nil {
			results <- result{err: err}
			return
		}
		secure.pending = indicator[:]

		if err := secure.writeServerHello("home-panel", "aabbccddeeff"); err != nil {
			results <- result{err: err}
			return
		}
		if err := secure.readClientHandshake(); err != nil {
			results <- result{err: err}
			return
		}
		frame, err := secure.readEncrypted()
		if err != nil {
			results <- result{err: err}
			return
		}

		if err := secure.writeEncrypted(TypeHelloResponse, nil); err != nil {
			results <- result{err: err}
			return
		}
		results <- result{frameType: frame.Type, payload: frame.Payload}
	}()

	peer := exec.Command(python, "-c", handshakePeerSource, key, listener.Addr().String())
	peer.Stdout = os.Stderr
	peer.Stderr = os.Stderr
	if err := peer.Start(); err != nil {
		t.Fatalf("the peer could not be started: %v", err)
	}
	defer peer.Process.Kill()

	select {
	case got := <-results:
		if got.err != nil {
			t.Fatalf("the server side failed: %v", got.err)
		}
		if got.frameType != TypeHelloRequest {
			t.Fatalf("the frame type is %d, it should be %d", got.frameType, TypeHelloRequest)
		}
		t.Logf("the peer sent a hello request: %x", got.payload)
	case <-time.After(30 * time.Second):
		t.Fatal("the peer did not finish in time")
	}
}

const handshakePeerSource = `
import asyncio, base64, struct, sys

from noise.connection import NoiseConnection

async def main():
    key = base64.b64decode(sys.argv[1])
    host, port = sys.argv[2].rsplit(":", 1)

    proto = NoiseConnection.from_name(b"Noise_NNpsk0_25519_ChaChaPoly_SHA256")
    proto.set_as_initiator()
    proto.set_psks(key)
    proto.set_prologue(b"NoiseAPIInit\x00\x00")
    proto.start_handshake()

    reader, writer = await asyncio.open_connection(host, int(port))

    # The client sends two frames. The first is the hello marker: an
    # indicator and a length of zero. The second is the handshake, with
    # a zero byte in front of it.
    writer.write(b"\x01\x00\x00")
    await writer.drain()
    handshake = proto.write_message()
    payload = b"\x00" + handshake
    writer.write(b"\x01" + struct.pack(">H", len(payload)) + payload)
    await writer.drain()

    # The server hello comes back first.
    header = await reader.readexactly(3)
    assert header[0] == 1, header
    length = struct.unpack(">H", header[1:3])[0]
    hello = await reader.readexactly(length)
    print("server hello:", hello)
    assert hello[0] == 1, "the server did not choose the protocol"
    name, _, mac = hello[1:].partition(b"\x00")
    print("server name:", name, "mac:", mac)
    assert mac.endswith(b"\x00"), "the mac address is not terminated"

    # Then the handshake response.
    header = await reader.readexactly(3)
    length = struct.unpack(">H", header[1:3])[0]
    response = await reader.readexactly(length)
    print("handshake reply frame: indicator", header[0], "length", length, "bytes", response.hex())
    proto.read_message(response[1:])
    print("handshake ok")

    # Now send an encrypted frame: a hello request.
    body = b"\x0a\x0eHome Assistant\x10\x01\x18\x10"
    header = struct.pack(">H", 1) + struct.pack(">H", len(body))
    frame = proto.encrypt(header + body)
    writer.write(b"\x01" + struct.pack(">H", len(frame)) + frame)
    await writer.drain()

    # Read the server's answer to check the cipher states line up.
    header = await reader.readexactly(3)
    length = struct.unpack(">H", header[1:3])[0]
    answer = await reader.readexactly(length)
    plain = proto.decrypt(answer)
    print("server answered:", plain[:8].hex())

asyncio.run(main())
`
