package live

import (
	"strings"
	"testing"
	"time"
)

// The live voice settings.
//
// The voice settings are entities, so Home Assistant can read them and
// change them. The panel's settings are the entities' values, and the
// entities' values are what the panel is really using.
//
// The round trip is the whole of it: a value is changed in Home
// Assistant, and the device says it is using the new one.

// TestTheVoiceSettingsRoundTrip checks that the voice settings can be
// changed from Home Assistant and are read back.
//
// The ducking is the one that matters most: it was set to minus twenty
// decibels and the music did not get any quieter.
func TestTheVoiceSettingsRoundTrip(t *testing.T) {
	skipUnlessLive(t, "Home Assistant", haURL(), haToken())

	d := startDevice(t, "")
	d.waitFor("connected to Home Assistant", 90*time.Second, func(s deviceState) bool {
		return s.Connection == "connected"
	})

	// The entities are there, and they are the panel's.
	//
	// There may be more than one panel in Home Assistant — a device that
	// was here before and one that is here now — so the entities that
	// are available are the ones to use.
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

	// The ducking is changed, and the device says it is using the new
	// value.
	//
	// The value is read back from Home Assistant, because that is where
	// it was set: the entity is the panel's setting, and the panel
	// reports what it is using by putting it there.
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

// setEntity changes an entity's value from Home Assistant.
func setEntity(t *testing.T, entity string, value any) {
	t.Helper()
	// The domain is in the entity's name.
	domain := "switch"
	if strings.HasPrefix(entity, "number.") {
		domain = "number"
	}
	post(t, haURL()+"/api/services/"+domain+"/set_value", haToken(), map[string]any{
		"entity_id": entity,
		"value":     value,
	})
}
