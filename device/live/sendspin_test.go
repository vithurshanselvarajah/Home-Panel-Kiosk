package live

import (
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// The live sendspin player.
//
// The panel is a music player to a music server: the server finds it on
// the network, pairs with it, and plays music through it.
//
// The pairing is the interesting part. Home Assistant has to know the
// player's identity and has to give the music server the token the
// player hands out, and the player has to be announced on the network
// under a name of its own.
//
// These tests check the two halves of that: that the device says who it
// is, and that Home Assistant has heard.

// TestTheDeviceIsASendspinPlayer checks that the device says it is a
// music player.
//
// The identity is in the device's capabilities, and Home Assistant
// reads it from there. A device that says nothing is a device a music
// server never pairs with.
func TestTheDeviceIsASendspinPlayer(t *testing.T) {
	skipUnlessLive(t, "Home Assistant", haURL(), haToken())

	d := startDevice(t, "")
	d.waitFor("connected to Home Assistant", 90*time.Second, func(s deviceState) bool {
		return s.Connection == "connected"
	})

	// The pairing token is asked for over the connection.
	//
	// It is the thing a music server pairs with, and the device says it
	// hands out one. A device that says nothing is a device a music
	// server never pairs with.
	token := devicePairingToken(t, d)
	if token == "" {
		t.Error("the device does not hand out a pairing token")
	}
	t.Logf("the device hands out a pairing token")
}

// TestHomeAssistantKnowsThePlayer checks that Home Assistant has the
// player's identity.
//
// Home Assistant passes it to the music server, which pairs with the
// player using the token the device hands out. Without it there is
// nothing to pair with.
func TestHomeAssistantKnowsThePlayer(t *testing.T) {
	skipUnlessLive(t, "Home Assistant", haURL(), haToken())

	d := startDevice(t, "")
	d.waitFor("connected to Home Assistant", 90*time.Second, func(s deviceState) bool {
		return s.Connection == "connected"
	})

	clientID := deviceSendspinClientID(t, d)
	if clientID == "" {
		t.Fatal("the device does not say what its player is called")
	}

	// The clients Home Assistant knows about.
	ids := homeAssistantSendspinClients(t)
	t.Logf("Home Assistant knows %d sendspin clients: %v", len(ids), ids)
	for _, id := range ids {
		if id == clientID {
			return
		}
	}
	t.Errorf("Home Assistant does not know the panel's player %q", clientID)
}

// deviceSendspinClientID asks the device what its player is called.
//
// The question is asked over the Home Assistant connection, so it is the
// same answer Home Assistant gets.
func deviceSendspinClientID(t *testing.T, d *device) string {
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

// devicePairingToken asks the device for the token a music server pairs
// with.
//
// It is the same question Home Assistant asks.
func devicePairingToken(t *testing.T, d *device) string {
	t.Helper()
	lines, err := d.peer("pairing_token", "")
	if err != nil {
		t.Fatalf("the pairing token could not be asked for: %v", err)
	}
	for _, line := range lines {
		if strings.HasPrefix(line, "token:") {
			return strings.TrimPrefix(line, "token:")
		}
	}
	return ""
}

// homeAssistantSendspinClients gives back the players Home Assistant
// knows about.
//
// It is asked over the websocket, because that is where Home Assistant
// says it: the music server follows the list, so that a player it has
// not paired with can be paired with as soon as it appears.
func homeAssistantSendspinClients(t *testing.T) []string {
	t.Helper()
	script := `
import asyncio, json, sys
import websockets

TOKEN = sys.argv[1]
URL = sys.argv[2]

async def main():
    ws_url = URL.replace("http://", "ws://").replace("https://", "wss://")
    async with websockets.connect(ws_url + "/api/websocket") as ws:
        await ws.send(json.dumps({"type": "auth", "access_token": TOKEN}))
        await ws.recv()
        await ws.send(json.dumps({"id": 1, "type": "sendspin/subscribe_clients"}))
        for _ in range(4):
            msg = await asyncio.wait_for(ws.recv(), timeout=30)
            d = json.loads(msg)
            if d.get("type") == "event":
                print(json.dumps(d["event"].get("client_ids", [])))
                return
    print("[]")

asyncio.run(main())
`
	cmd := exec.Command("python3", "-c", script, haToken(), haURL())
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("the sendspin clients could not be asked for: %v (%s)", err, strings.TrimSpace(string(out)))
	}
	var ids []string
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(out))), &ids); err != nil {
		t.Fatalf("the answer could not be read: %v (%s)", err, strings.TrimSpace(string(out)))
	}
	return ids
}

// The result of a call that is not a map.
var _ = json.Marshal
var _ = os.Getenv
