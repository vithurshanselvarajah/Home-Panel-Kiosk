package esphome

import (
	"testing"
)

func TestTheMediaPlayerIsListed(t *testing.T) {
	player := NewMediaPlayer(nil)
	message, err := player.Message(7)
	if err != nil {
		t.Fatal(err)
	}
	if message.Type != TypeListEntitiesMediaPlayerResponse {
		t.Errorf("the speaker was listed as message %d", message.Type)
	}

	body, ok := message.Body.(*ListEntitiesMediaPlayerResponse)
	if !ok {
		t.Fatalf("the speaker's listing was a %T", message.Body)
	}

	if body.GetName() != "" {
		t.Errorf("the speaker's name was %q, want empty", body.GetName())
	}
	if body.GetKey() != 7 {
		t.Errorf("the speaker's key was %d, want 7", body.GetKey())
	}

	if len(body.GetSupportedFormats()) != 2 {
		t.Fatalf("the speaker offered %d formats, want 2", len(body.GetSupportedFormats()))
	}
	for _, format := range body.GetSupportedFormats() {
		if format.GetFormat() != "wav" {
			t.Errorf("the speaker's format was %q, want wav", format.GetFormat())
		}
		if format.GetSampleRate() != 48000 {
			t.Errorf("the speaker's rate was %d, want 48000", format.GetSampleRate())
		}
	}
}

func TestTheMediaPlayerReportsItsState(t *testing.T) {
	player := NewMediaPlayer(nil)
	player.SetVolume(0.5)

	message := player.StateMessage(3)
	if message.GetKey() != 3 {
		t.Errorf("the state's key was %d, want 3", message.GetKey())
	}
	if message.GetVolume() != 0.5 {
		t.Errorf("the state's volume was %f, want 0.5", message.GetVolume())
	}
	if message.GetMuted() {
		t.Error("the player was muted before anything muted it")
	}
}

func TestAMediaPlayerCommandSetsTheVolume(t *testing.T) {
	player := NewMediaPlayer(nil)

	var got float32
	player.OnVolume(func(volume float32) { got = volume })

	player.HandleCommand(1, &MediaPlayerCommandRequest{
		Key:       1,
		HasVolume: true,
		Volume:    0.25,
	})

	if got != 0.25 {
		t.Errorf("the volume was %f, want 0.25", got)
	}
	if player.Volume() != 0.25 {
		t.Errorf("the player's volume was %f, want 0.25", player.Volume())
	}
}

func TestAMediaPlayerCommandSetsTheMute(t *testing.T) {
	player := NewMediaPlayer(nil)

	var got bool
	player.OnMute(func(muted bool) { got = muted })

	player.HandleCommand(1, &MediaPlayerCommandRequest{
		Key:        1,
		HasCommand: true,
		Command:    MediaPlayerCommand_MEDIA_PLAYER_COMMAND_MUTE,
	})

	if !got {
		t.Error("the player was not told it was muted")
	}
	if player.Muted() {
		t.Error("the player says it is muted, but the mute is not acted on")
	}
}

func TestAMediaPlayerCommandHandsOnTheURL(t *testing.T) {
	player := NewMediaPlayer(nil)

	var url string
	var announcement bool
	player.OnPlay(func(u string, a bool) {
		url = u
		announcement = a
	})

	player.HandleCommand(1, &MediaPlayerCommandRequest{
		Key:          1,
		HasMediaUrl:  true,
		MediaUrl:     "http://homeassistant/media.mp3",
		Announcement: true,
	})

	if url != "http://homeassistant/media.mp3" {
		t.Errorf("the URL was %q", url)
	}
	if !announcement {
		t.Error("the announcement was not marked as one")
	}
}

func TestTheSpeakerCanBeBrowsed(t *testing.T) {
	player := NewMediaPlayer(nil)
	message, err := player.Message(1)
	if err != nil {
		t.Fatal(err)
	}

	body, ok := message.Body.(*ListEntitiesMediaPlayerResponse)
	if !ok {
		t.Fatalf("the speaker's listing was a %T", message.Body)
	}

	if body.GetFeatureFlags()&(1<<17) == 0 {
		t.Errorf("the speaker's flags were %d, which do not include browsing",
			body.GetFeatureFlags())
	}
}

func TestTheSpeakerOffersTheFormatsHomeAssistantAsks(t *testing.T) {
	player := NewMediaPlayer(nil)
	message, err := player.Message(1)
	if err != nil {
		t.Fatal(err)
	}

	body, ok := message.Body.(*ListEntitiesMediaPlayerResponse)
	if !ok {
		t.Fatalf("the speaker's listing was a %T", message.Body)
	}

	var announcement bool
	for _, format := range body.GetSupportedFormats() {
		if format.GetPurpose() == MediaPlayerFormatPurpose_MEDIA_PLAYER_FORMAT_PURPOSE_ANNOUNCEMENT {
			announcement = true
		}
	}
	if !announcement {
		t.Error("the speaker offered no announcement format")
	}
}
