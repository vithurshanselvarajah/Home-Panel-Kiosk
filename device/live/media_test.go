package live

import (
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestThePanelPlaysRadioFromHomeAssistant(t *testing.T) {
	skipUnlessLive(t, "Home Assistant", haURL(), haToken())

	content := liveStationContent(t)

	d := startDevice(t, "")
	d.waitFor("connected to Home Assistant", 90*time.Second, func(s deviceState) bool {
		return s.Connection == "connected"
	})

	player := livePlayer(t, d)
	t.Logf("the player is %s", player)

	played := post(t, haURL()+"/api/services/media_player/play_media", haToken(), map[string]any{
		"entity_id":          player,
		"media_content_id":   content,
		"media_content_type": "music",
	})
	if list, ok := played["list"].([]any); ok && len(list) > 0 {
		if failure, ok := list[0].(map[string]any); ok {
			if _, bad := failure["error"]; bad {
				t.Errorf("Home Assistant said no: %v", failure)
			}
		}
	}

	state := d.waitFor("playing the station", 60*time.Second, func(s deviceState) bool {
		return s.MediaState == "playing"
	})
	t.Logf("the panel is %s", state.MediaState)

	grown := d.waitFor("playing the station's audio", 90*time.Second, func(s deviceState) bool {
		return d.speakerBytes() > 100000
	})
	_ = grown
	t.Logf("the panel has played %d bytes", d.speakerBytes())
}

func liveStation(t *testing.T) string {
	t.Helper()
	if station := os.Getenv("LIVE_STATION"); station != "" {

		if strings.Contains(station, "-") && strings.Contains(station, "radio_browser") {
			return station
		}
	}
	name := os.Getenv("LIVE_STATION")
	if name == "" {
		name = "Capital FM London"
	}
	station := findRadioStation(t, name)
	if station == "" {
		t.Skipf("the radio browser does not have a station called %q", name)
	}
	return station
}

func liveStationContent(t *testing.T) string {
	t.Helper()
	content := liveStation(t)
	if !strings.HasPrefix(content, "media-source://") {
		content = "media-source://radio_browser/" + content
	}
	return content
}

func findRadioStation(t *testing.T, name string) string {
	t.Helper()
	player := livePlayerEntity(t)
	script := `
import asyncio, json, sys
import websockets

TOKEN = sys.argv[1]
URL = sys.argv[2]
PLAYER = sys.argv[3]
WANTED = sys.argv[4]

async def main():
    ws_url = URL.replace("http://", "ws://").replace("https://", "wss://")
    async with websockets.connect(ws_url + "/api/websocket") as ws:
        await ws.send(json.dumps({"type": "auth", "access_token": TOKEN}))
        await ws.recv()
        await ws.send(json.dumps({
            "id": 1,
            "type": "media_player/browse_media",
            "entity_id": PLAYER,
            "media_content_type": "music",
            "media_content_id": "media-source://radio_browser/country/GB",
        }))
        for _ in range(20):
            msg = await asyncio.wait_for(ws.recv(), timeout=60)
            data = json.loads(msg)
            if data.get("id") != 1:
                continue
            result = data.get("result") or {}
            for child in result.get("children", []):
                if (child.get("title") or "").lower() == WANTED.lower():
                    print(child.get("media_content_id", ""))
                    return
    print("")

asyncio.run(main())
`
	cmd := exec.Command("python3", "-c", script, haToken(), haURL(), player, name)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Logf("the radio browser could not be searched: %v (%s)", err, strings.TrimSpace(string(out)))
		return ""
	}

	answer := strings.TrimSpace(string(out))
	if answer == "" {
		return ""
	}
	return answer
}

func livePlayerEntity(t *testing.T) string {
	t.Helper()
	for id := range haEntities(t, "media_player") {
		return id
	}
	t.Skip("Home Assistant has no media player at all")
	return ""
}

func livePlayer(t *testing.T, d *device) string {
	t.Helper()

	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		for id, state := range haEntities(t, "media_player") {
			if !strings.Contains(id, "home_panel") {
				continue
			}
			if state != "unavailable" {
				return id
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatalf("Home Assistant has no panel media player that is available: %v", haEntities(t, "media_player"))
	return ""
}

func TestThePanelSurvivesTheMediaPlaying(t *testing.T) {
	skipUnlessLive(t, "Home Assistant", haURL(), haToken())

	d := startDevice(t, "")
	d.waitFor("connected to Home Assistant", 90*time.Second, func(s deviceState) bool {
		return s.Connection == "connected"
	})

	player := livePlayer(t, d)

	post(t, haURL()+"/api/services/media_player/media_stop", haToken(), map[string]any{
		"entity_id": player,
	})

	post(t, haURL()+"/api/services/media_player/play_media", haToken(), map[string]any{
		"entity_id":          player,
		"media_content_id":   liveStationContent(t),
		"media_content_type": "music",
	})

	d.waitFor("playing the station", 90*time.Second, func(s deviceState) bool {
		return s.MediaState == "playing"
	})

	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		state, _ := d.lastState()
		if state.Connection != "connected" {
			t.Fatalf("the panel dropped its connection while the music was playing: %s", state.Connection)
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Log("the panel stayed connected while the music played")
}
