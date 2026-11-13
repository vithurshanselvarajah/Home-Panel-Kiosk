package live

import (
	"bufio"
	"strings"
	"testing"
	"time"
)

// The live arbitration.
//
// The panel has two kinds of music: the music Home Assistant is
// playing, and the music a music server is playing. Both are music, and
// playing both at once is neither of them, so when one starts the other
// stops.
//
// The microphone and the reply are never stopped: they are the point of
// the panel.

// TestTheMusicServerStopsHomeAssistantsMusic checks that a music
// server playing to the panel stops the music Home Assistant is playing.
//
// Home Assistant's music is the audio the device holds, so it can be
// stopped. The music server's music is the server's, and the server says
// so.
func TestTheMusicServerStopsHomeAssistantsMusic(t *testing.T) {
	skipUnlessLive(t, "Home Assistant", haURL(), haToken())

	d := startDevice(t, "")
	d.waitFor("connected to Home Assistant", 90*time.Second, func(s deviceState) bool {
		return s.Connection == "connected"
	})

	// Home Assistant plays the radio.
	player := livePlayer(t)
	post(t, haURL()+"/api/services/media_player/play_media", haToken(), map[string]any{
		"entity_id":          player,
		"media_content_id":   "media-source://radio_browser/" + liveStation(t),
		"media_content_type": "music",
	})

	// The panel is playing it.
	d.waitFor("playing Home Assistant's music", 90*time.Second, func(s deviceState) bool {
		return s.MediaState == "playing"
	})
	t.Log("the panel is playing Home Assistant's music")

	// Now a music server plays to the panel.
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

	// The panel plays the server's music.
	d.waitFor("playing the server's music", 90*time.Second, func(s deviceState) bool {
		return s.MusicPlaying || s.MusicState == "playing"
	})
	t.Log("the panel is playing the music server's music")

	// And Home Assistant's music has stopped.
	d.waitFor("Home Assistant's music stopped", 60*time.Second, func(s deviceState) bool {
		return s.MediaState != "playing"
	})
	t.Log("Home Assistant's music stopped")
}

// trimLine takes the spaces off a line.
func trimLine(s string) string {
	return strings.TrimSpace(s)
}

// TestHomeAssistantsMusicPausesTheMusicServer checks that the music
// Home Assistant plays pauses the music a music server is playing.
//
// The server's music is paused rather than stopped: the server is still
// playing it, and it will say so again.
func TestHomeAssistantsMusicPausesTheMusicServer(t *testing.T) {
	skipUnlessLive(t, "Home Assistant", haURL(), haToken())

	d := startDevice(t, "")
	d.waitFor("connected to Home Assistant", 90*time.Second, func(s deviceState) bool {
		return s.Connection == "connected"
	})

	// A music server plays to the panel.
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

	// The panel is playing the server's music.
	d.waitFor("playing the server's music", 90*time.Second, func(s deviceState) bool {
		return s.MusicPlaying || s.MusicState == "playing"
	})
	t.Log("the panel is playing the music server's music")

	// Now Home Assistant plays the radio.
	player := livePlayer(t)
	post(t, haURL()+"/api/services/media_player/play_media", haToken(), map[string]any{
		"entity_id":          player,
		"media_content_id":   "media-source://radio_browser/" + liveStation(t),
		"media_content_type": "music",
	})

	// The panel is playing Home Assistant's music.
	d.waitFor("playing Home Assistant's music", 90*time.Second, func(s deviceState) bool {
		return s.MediaState == "playing"
	})
	t.Log("the panel is playing Home Assistant's music")

	// And the server's music has stopped.
	d.waitFor("the server's music stopped", 60*time.Second, func(s deviceState) bool {
		return !s.MusicPlaying && s.MusicState != "playing"
	})
	t.Log("the music server's music stopped")
}
