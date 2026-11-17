package live

import (
	"bufio"
	"strings"
	"testing"
	"time"
)

func TestTheMusicServerStopsHomeAssistantsMusic(t *testing.T) {
	skipUnlessLive(t, "Home Assistant", haURL(), haToken())

	d := startDevice(t, "")
	d.waitFor("connected to Home Assistant", 90*time.Second, func(s deviceState) bool {
		return s.Connection == "connected"
	})

	player := livePlayer(t, d)
	post(t, haURL()+"/api/services/media_player/play_media", haToken(), map[string]any{
		"entity_id":          player,
		"media_content_id":   liveStationContent(t),
		"media_content_type": "music",
	})

	d.waitFor("playing Home Assistant's music", 90*time.Second, func(s deviceState) bool {
		return s.MediaState == "playing"
	})
	t.Log("the panel is playing Home Assistant's music")

	peer, peerOutput := startSendspinPeer(t, "play", d)
	defer func() {
		if peer != nil && peer.Process != nil {
			_ = peer.Process.Kill()
		}
	}()
	go func() {
		scanner := bufio.NewScanner(peerOutput)
		for scanner.Scan() {
			if line := trimLine(scanner.Text()); line != "" {
				t.Logf("the server: %s", line)
			}
		}
	}()

	d.waitFor("playing the server's music", 90*time.Second, func(s deviceState) bool {
		return s.MusicPlaying || s.MusicState == "playing"
	})
	t.Log("the panel is playing the music server's music")

	d.waitFor("Home Assistant's music stopped", 60*time.Second, func(s deviceState) bool {
		return s.MediaState != "playing"
	})
	t.Log("Home Assistant's music stopped")
}

func trimLine(s string) string {
	return strings.TrimSpace(s)
}

func TestHomeAssistantsMusicPausesTheMusicServer(t *testing.T) {
	skipUnlessLive(t, "Home Assistant", haURL(), haToken())

	d := startDevice(t, "")
	d.waitFor("connected to Home Assistant", 90*time.Second, func(s deviceState) bool {
		return s.Connection == "connected"
	})

	peer, peerOutput := startSendspinPeer(t, "play", d)
	defer func() {
		if peer != nil && peer.Process != nil {
			_ = peer.Process.Kill()
		}
	}()
	go func() {
		scanner := bufio.NewScanner(peerOutput)
		for scanner.Scan() {
			if line := trimLine(scanner.Text()); line != "" {
				t.Logf("the server: %s", line)
			}
		}
	}()

	d.waitFor("playing the server's music", 90*time.Second, func(s deviceState) bool {
		return s.MusicPlaying || s.MusicState == "playing"
	})
	t.Log("the panel is playing the music server's music")

	player := livePlayer(t, d)
	post(t, haURL()+"/api/services/media_player/play_media", haToken(), map[string]any{
		"entity_id":          player,
		"media_content_id":   liveStationContent(t),
		"media_content_type": "music",
	})

	d.waitFor("playing Home Assistant's music", 90*time.Second, func(s deviceState) bool {
		return s.MediaState == "playing"
	})
	t.Log("the panel is playing Home Assistant's music")

	d.waitFor("the server's music stopped", 60*time.Second, func(s deviceState) bool {
		return !s.MusicPlaying && s.MusicState != "playing"
	})
	t.Log("the music server's music stopped")
}

func TestTheTransportGoesToTheMusicThatIsPlaying(t *testing.T) {
	skipUnlessLive(t, "Home Assistant", haURL(), haToken())

	d := startDevice(t, "")
	d.waitFor("connected to Home Assistant", 90*time.Second, func(s deviceState) bool {
		return s.Connection == "connected"
	})

	player := livePlayer(t, d)
	post(t, haURL()+"/api/services/media_player/play_media", haToken(), map[string]any{
		"entity_id":          player,
		"media_content_id":   liveStationContent(t),
		"media_content_type": "music",
	})
	d.waitFor("playing Home Assistant's music", 90*time.Second, func(s deviceState) bool {
		return s.MediaState == "playing"
	})
	t.Log("the panel is playing Home Assistant's music")

	d.sendTransport("pause", "home")

	d.waitFor("Home Assistant's music paused", 30*time.Second, func(s deviceState) bool {
		return s.MediaState == "paused"
	})
	t.Log("Home Assistant's music was paused")
}
