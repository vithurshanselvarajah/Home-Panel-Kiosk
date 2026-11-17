package live

import (
	"strings"
	"testing"
	"time"
)

func TestTheVoiceSettingsRoundTrip(t *testing.T) {
	skipUnlessLive(t, "Home Assistant", haURL(), haToken())

	d := startDevice(t, "")
	d.waitFor("connected to Home Assistant", 90*time.Second, func(s deviceState) bool {
		return s.Connection == "connected"
	})

	entities := haEntities(t, "home_panel")
	ducking := ""
	for name, state := range entities {
		if strings.HasPrefix(name, "number.home_panel_music_ducking") && state != "unavailable" {
			ducking = name
		}
	}
	if ducking == "" {
		t.Fatalf("Home Assistant has no music ducking entity that is available: %v", entities)
	}
	t.Logf("the ducking entity is %s", ducking)

	const want = -20.0
	setEntity(t, ducking, want)

	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if got := haEntities(t, "home_panel")[ducking]; got == "-20.0" {
			t.Logf("the ducking is %v decibels", got)
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Errorf("the ducking was not changed: %v", haEntities(t, "home_panel")[ducking])
}

func setEntity(t *testing.T, entity string, value any) {
	t.Helper()

	domain := "switch"
	if strings.HasPrefix(entity, "number.") {
		domain = "number"
	}
	post(t, haURL()+"/api/services/"+domain+"/set_value", haToken(), map[string]any{
		"entity_id": entity,
		"value":     value,
	})
}
