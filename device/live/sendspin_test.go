package live

import (
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestTheDeviceIsASendspinPlayer(t *testing.T) {
	skipUnlessLive(t, "Home Assistant", haURL(), haToken())

	d := startDevice(t, "")
	d.waitFor("connected to Home Assistant", 90*time.Second, func(s deviceState) bool {
		return s.Connection == "connected"
	})

	token := devicePairingToken(t, d)
	if token == "" {
		t.Error("the device does not hand out a pairing token")
	}
	t.Logf("the device hands out a pairing token")
}

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

	ids := homeAssistantSendspinClients(t)
	t.Logf("Home Assistant knows %d sendspin clients: %v", len(ids), ids)
	for _, id := range ids {
		if id == clientID {
			return
		}
	}
	t.Errorf("Home Assistant does not know the panel's player %q", clientID)
}

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

var _ = json.Marshal
var _ = os.Getenv
