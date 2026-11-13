package sendspin

import (
	"encoding/json"
	"path/filepath"
	"testing"
)

// TestTheMetadataIsRead checks that the music is read out of the server's
// state.
//
// The server sends what is playing in its state message, and it is the
// only thing that says what is playing: the audio itself says nothing
// about it.
func TestTheMetadataIsRead(t *testing.T) {
	payload := `{
		"metadata": {
			"timestamp": 1234567890,
			"title": "The Bird",
			"artist": "The Sleepy Jackson",
			"album_artist": "The Sleepy Jackson",
			"album": "The Bird",
			"artwork_url": "http://192.168.1.10/art.jpg",
			"year": 2003,
			"track": 7,
			"progress": {
				"track_progress": 45000,
				"track_duration": 210000,
				"playback_speed": 1000
			}
		}
	}`

	var st serverState
	if err := json.Unmarshal([]byte(payload), &st); err != nil {
		t.Fatal(err)
	}
	if st.Metadata == nil {
		t.Fatal("the metadata was not read")
	}

	got := metadataOf(*st.Metadata)
	if got.Title != "The Bird" {
		t.Errorf("the title was %q, want %q", got.Title, "The Bird")
	}
	if got.Artist != "The Sleepy Jackson" {
		t.Errorf("the artist was %q, want %q", got.Artist, "The Sleepy Jackson")
	}
	if got.Album != "The Bird" {
		t.Errorf("the album was %q, want %q", got.Album, "The Bird")
	}
	if got.AlbumArtist != "The Sleepy Jackson" {
		t.Errorf("the album artist was %q, want %q", got.AlbumArtist, "The Sleepy Jackson")
	}
	if got.ArtworkURL != "http://192.168.1.10/art.jpg" {
		t.Errorf("the artwork was %q, want %q", got.ArtworkURL, "http://192.168.1.10/art.jpg")
	}
	if got.Year != 2003 {
		t.Errorf("the year was %d, want 2003", got.Year)
	}
	if got.Track != 7 {
		t.Errorf("the track was %d, want 7", got.Track)
	}
	if got.ProgressMs != 45000 {
		t.Errorf("the position was %d, want 45000", got.ProgressMs)
	}
	if got.DurationMs != 210000 {
		t.Errorf("the length was %d, want 210000", got.DurationMs)
	}
	if got.Speed != 1000 {
		t.Errorf("the speed was %d, want 1000", got.Speed)
	}
	if got.Empty() {
		t.Error("the metadata was said to be empty, but it has a title")
	}
}

// TestTheMusicStoppingIsRead checks that the music stopping is told
// apart from the state not changing.
//
// The server clears the metadata by sending it as null, and null and
// absent mean different things: one says the music has stopped and the
// other says nothing has changed.
func TestTheMusicStoppingIsRead(t *testing.T) {
	var st serverState
	if err := json.Unmarshal([]byte(`{"metadata": null}`), &st); err != nil {
		t.Fatal(err)
	}
	if !st.MetadataNull {
		t.Error("a null metadata was not seen as the music stopping")
	}
	if st.Metadata != nil {
		t.Error("a null metadata was read as an object")
	}

	var other serverState
	if err := json.Unmarshal([]byte(`{"controller": {"volume": 50}}`), &other); err != nil {
		t.Fatal(err)
	}
	if other.MetadataNull {
		t.Error("an absent metadata was seen as the music stopping")
	}
}

// TestTheTransportIsRead checks that what the transport can do is read.
//
// The server says which commands it will take, and the panel only
// offers those, because a button that does nothing is worse than no
// button.
func TestTheTransportIsRead(t *testing.T) {
	payload := `{
		"controller": {
			"supported_commands": ["play", "pause", "next", "previous", "seek"],
			"volume": 60,
			"muted": false,
			"repeat": "off",
			"shuffle": false,
			"seek_max_ms": 210000
		}
	}`

	var st serverState
	if err := json.Unmarshal([]byte(payload), &st); err != nil {
		t.Fatal(err)
	}
	if st.Controller == nil {
		t.Fatal("the controller was not read")
	}
	if len(st.Controller.SupportedCommands) != 5 {
		t.Errorf("there were %d commands, want 5", len(st.Controller.SupportedCommands))
	}
	if st.Controller.Volume != 60 {
		t.Errorf("the volume was %d, want 60", st.Controller.Volume)
	}
	if st.Controller.SeekMaxMs == nil || *st.Controller.SeekMaxMs != 210000 {
		t.Errorf("the furthest seek was %v, want 210000", st.Controller.SeekMaxMs)
	}
}

// TestTheEmptyMetadataIsTold checks that a metadata with nothing in it is
// said to be empty.
//
// It is what the panel uses to decide whether to show the music at all.
func TestTheEmptyMetadataIsTold(t *testing.T) {
	if !(Metadata{}).Empty() {
		t.Error("a metadata with nothing in it was not said to be empty")
	}
	if (Metadata{Title: "A"}).Empty() {
		t.Error("a metadata with a title was said to be empty")
	}
}

// TestTheServerStateIsHandled checks that the server's state reaches the
// client.
//
// The session is driven with the message the server would send, and the
// client is asked what it thinks is playing. This is the whole path:
// the server says, the session reads, and the client reports.
func TestTheServerStateIsHandled(t *testing.T) {
	client, err := New(Config{
		StorePath: filepath.Join(t.TempDir(), "sendspin.json"),
		Name:      "Test",
		Instance:  "test",
		Port:      0,
	})
	if err != nil {
		t.Fatal(err)
	}
	session := &session{c: client, filter: newTimeFilter()}
	// A server may not send anything before it has activated the
	// session, so the session is activated before it is driven.
	session.activated = true

	// The server sends what is playing.
	state, err := json.Marshal(map[string]any{
		"metadata": map[string]any{
			"title":        "Suck the Blood",
			"artist":       "The Sleepy Jackson",
			"album":        "Fumbling Towards Fitness",
			"artwork_url":  "http://192.168.1.20/cover.jpg",
			"track":        3,
			"year":         2001,
			"album_artist": "The Sleepy Jackson",
			"progress": map[string]any{
				"track_progress": 30000,
				"track_duration": 195000,
				"playback_speed": 1000,
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := session.dispatch(envelope{Type: "server/state", Payload: state}); err != nil {
		t.Fatalf("the state was not handled: %v", err)
	}

	got := metadataOf(session.metadata)
	if got.Title != "Suck the Blood" {
		t.Errorf("the title was %q, want %q", got.Title, "Suck the Blood")
	}
	if got.DurationMs != 195000 {
		t.Errorf("the length was %d, want 195000", got.DurationMs)
	}
	if got.Speed != 1000 {
		t.Errorf("the speed was %d, want 1000", got.Speed)
	}

	// The server says the transport can seek.
	controller, err := json.Marshal(map[string]any{
		"controller": map[string]any{
			"supported_commands": []string{"play", "pause", "next", "previous", "seek"},
			"volume":             60,
			"muted":              false,
			"repeat":             "off",
			"shuffle":            false,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := session.dispatch(envelope{Type: "server/state", Payload: controller}); err != nil {
		t.Fatalf("the state was not handled: %v", err)
	}
	commands := session.Controller()
	if commands == nil {
		t.Fatal("the transport was not read")
	}
	if len(commands.SupportedCommands) != 5 {
		t.Errorf("there were %d commands, want 5", len(commands.SupportedCommands))
	}

	// The music stops, and the server says so with a null.
	stopped, err := json.Marshal(map[string]any{"metadata": nil})
	if err != nil {
		t.Fatal(err)
	}
	if err := session.dispatch(envelope{Type: "server/state", Payload: stopped}); err != nil {
		t.Fatalf("the state was not handled: %v", err)
	}
	if got := metadataOf(session.metadata); !got.Empty() {
		t.Errorf("the metadata was %+v after the music stopped, want nothing", got)
	}
}

// TestTheBufferIsSmallEnoughToHearTheMusic checks that the buffer is
// small.
//
// The server holds the audio back by the buffer before it sends it, and
// the player plays the audio when its own time says it is due. So a big
// buffer is a long silence before the music is heard — and a buffer of
// two seconds meant the panel played nothing for two seconds and then
// waited for the buffer to pass.
//
// The buffer only has to be big enough that the network cannot outrun
// the speaker.
func TestTheBufferIsSmallEnoughToHearTheMusic(t *testing.T) {
	dir := t.TempDir()
	client, err := New(Config{StorePath: filepath.Join(dir, "sendspin.json"), Name: "Test Panel"})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Stop("the test is over")

	if got := client.bufferMs; got > 500 {
		t.Errorf("the buffer is %dms, the music would be heard that late", got)
	}
	if got := client.requiredLeadMs; got > 500 {
		t.Errorf("the lead is %dms, the music would be heard that late", got)
	}
}
