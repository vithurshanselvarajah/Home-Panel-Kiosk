package live

import (
	"os"
	"strings"
	"testing"
	"time"
)

// The live media player.
//
// The panel is a speaker to Home Assistant: Home Assistant plays
// something on it and the panel plays it. This is the whole of that,
// end to end.
//
// The thing played is a radio station, so the audio really is a stream
// and never ends: a track that was a file would finish, and the
// problems that matter are the ones that only show up on a stream that
// does not end.

// TestThePanelPlaysRadioFromHomeAssistant plays a radio station on the
// panel and checks that the panel plays it.
//
// The station is Capital, through Home Assistant's radio browser
// integration. The audio is checked by its length: a station that was
// not played would leave the speaker's file short, and a station that
// was played leaves it long.
//
// The panel is told to play it the way Home Assistant tells it to: a
// media command over the Home Assistant connection.
func TestThePanelPlaysRadioFromHomeAssistant(t *testing.T) {
	skipUnlessLive(t, "Home Assistant", haURL(), haToken())

	station := liveStation(t)
	// The station is a radio browser entry, so the content is a media
	// source address rather than the stream itself.
	//
	// Home Assistant plays the radio browser's own stream, which it has
	// proxied and knows the format of. A raw address would be played as
	// it came, and the panel would have to know every format a radio
	// station uses.
	//
	// The station is named by its identifier in the radio browser, not
	// by its address: Home Assistant looks the station up to find out
	// what it is and where it plays from.
	content := "media-source://radio_browser/" + station

	d := startDevice(t, "")
	d.waitFor("connected to Home Assistant", 90*time.Second, func(s deviceState) bool {
		return s.Connection == "connected"
	})

	// The player has to be there before it can be played on.
	//
	// Home Assistant notices the device a moment after it connects, so
	// the player is not there the instant the link is up.
	player := livePlayer(t)
	t.Logf("the player is %s", player)

	// Home Assistant is told to play the station.
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

	// The panel says it is playing.
	state := d.waitFor("playing the station", 60*time.Second, func(s deviceState) bool {
		return s.MediaState == "playing"
	})
	t.Logf("the panel is %s", state.MediaState)

	// The audio is really being played.
	//
	// A station never ends, so the speaker's file keeps growing. The
	// first of it takes a moment: the proxy transcodes the station, and
	// the speaker opens once there is enough music queued to play
	// without a gap.
	grown := d.waitFor("playing the station's audio", 90*time.Second, func(s deviceState) bool {
		return d.speakerBytes() > 100000
	})
	_ = grown
	t.Logf("the panel has played %d bytes", d.speakerBytes())
}

// liveStation is the radio station that is played.
//
// It is a stream, so it never ends and the panel keeps playing it for
// as long as it is told to.
func liveStation(t *testing.T) string {
	t.Helper()
	if station := os.Getenv("LIVE_STATION"); station != "" {
		return station
	}
	t.Skip("no station was named")
	return ""
}

// livePlayer is the panel's media player in Home Assistant.
//
// There may be more than one panel in Home Assistant — a device that
// was here before and one that is here now — so the one that is
// available is the one to play on.
//
// Home Assistant notices the device a moment after it connects, so the
// player is waited for rather than looked for.
func livePlayer(t *testing.T) string {
	t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		entities := haEntities(t, "media_player")
		for id, state := range entities {
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

// TestThePanelSurvivesTheMediaPlaying checks that the panel stays
// connected to Home Assistant while the music plays.
//
// The music goes through the panel's own proxy, and a panel that drops
// its connection when the music starts is a panel that cannot be
// controlled while the music is playing.
func TestThePanelSurvivesTheMediaPlaying(t *testing.T) {
	skipUnlessLive(t, "Home Assistant", haURL(), haToken())

	d := startDevice(t, "")
	d.waitFor("connected to Home Assistant", 90*time.Second, func(s deviceState) bool {
		return s.Connection == "connected"
	})

	player := livePlayer(t)
	post(t, haURL()+"/api/services/media_player/play_media", haToken(), map[string]any{
		"entity_id":          player,
		"media_content_id":   "media-source://radio_browser/" + liveStation(t),
		"media_content_type": "music",
	})

	// The music plays for a while, and the panel stays connected.
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
