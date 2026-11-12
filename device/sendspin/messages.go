package sendspin

import "encoding/json"

// envelope is every JSON message: a type and a payload.
type envelope struct {
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload"`
}

func marshalMsg(typ string, payload any) ([]byte, error) {
	p, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	return json.Marshal(envelope{Type: typ, Payload: p})
}

// ── cleartext handshake ─────────────────────────────────────────────────────

type clientInit struct {
	ClientID string `json:"client_id"`
	Version  int    `json:"version"`
	Suite    string `json:"suite"`
}

type serverInit struct {
	ServerID string `json:"server_id"`
	Version  int    `json:"version"`
}

type serverError struct {
	Reason string `json:"reason"`
}

type noiseHandshake struct {
	Data string `json:"data"`
}

// ── client → server ─────────────────────────────────────────────────────────

type deviceInfo struct {
	ProductName     string `json:"product_name,omitempty"`
	Manufacturer    string `json:"manufacturer,omitempty"`
	SoftwareVersion string `json:"software_version,omitempty"`
}

type audioFormat struct {
	Codec      string `json:"codec"`
	Channels   int    `json:"channels"`
	SampleRate int    `json:"sample_rate"`
	BitDepth   int    `json:"bit_depth"`
}

type playerSupport struct {
	SupportedFormats []audioFormat `json:"supported_formats"`
	BufferCapacity   int           `json:"buffer_capacity"`
	// 9.1.1: required here, and only "volume"/"mute" are valid. The current
	// spec moved it into client/state.
	SupportedCommands []string `json:"supported_commands"`
}

// pairMethod is one entry of supported_pair_methods. 9.1.1: a LIST of
// {method, ...}; the current spec has an object keyed by method. 9.1.1
// rejects the object form, so the list is what gets sent.
type pairMethod struct {
	Method string `json:"method"`
}

type unpairedAccess struct {
	Enabled bool `json:"enabled"`
}

type clientHello struct {
	Name                 string         `json:"name"`
	SupportedRoles       []string       `json:"supported_roles"`
	DeviceInfo           *deviceInfo    `json:"device_info,omitempty"`
	PlayerSupport        *playerSupport `json:"player@v1_support,omitempty"`
	SupportedPairMethods []pairMethod   `json:"supported_pair_methods"`
	UnpairedAccess       unpairedAccess `json:"unpaired_access"`
}

// serverState is the state the server sends.
//
// It carries the metadata role's object when the server has one, and the
// metadata is what says what is playing: the title, the artist, the
// album, the artwork and where the track has got to.
//
// The metadata is a pointer because the server sends it as null when
// nothing is playing, and null is a different answer from an object
// with nothing in it.
type serverState struct {
	Metadata *streamMetadata `json:"metadata,omitempty"`
	// Controller is what the transport can do, when the server sends it.
	Controller *controllerState `json:"controller,omitempty"`
	// MetadataNull says the metadata role's object came as null.
	//
	// A JSON null and an absent key both leave the pointer nil, and
	// they mean different things: null says the music has stopped, and
	// absent says nothing changed. The raw payload is kept so the two
	// can be told apart.
	MetadataNull bool `json:"-"`
}

// UnmarshalJSON reads a server/state message.
//
// The metadata role's object is cleared by the server sending it as
// null, and that has to be told apart from the object simply not
// being there, because one means the music has stopped and the other
// means nothing changed.
func (s *serverState) UnmarshalJSON(data []byte) error {
	type plain serverState
	var p plain
	if err := json.Unmarshal(data, &p); err != nil {
		return err
	}
	*s = serverState(p)

	var probe map[string]json.RawMessage
	if err := json.Unmarshal(data, &probe); err != nil {
		return err
	}
	if raw, ok := probe["metadata"]; ok {
		s.MetadataNull = string(raw) == "null"
	}
	return nil
}

// streamMetadata is what is playing.
//
// The progress is in milliseconds, and the speed is a multiplier of a
// thousand, so a thousand is normal and zero is paused.
type streamMetadata struct {
	Timestamp   int64           `json:"timestamp,omitempty"`
	Title       string          `json:"title,omitempty"`
	Artist      string          `json:"artist,omitempty"`
	Album       string          `json:"album,omitempty"`
	AlbumArtist string          `json:"album_artist,omitempty"`
	ArtworkURL  string          `json:"artwork_url,omitempty"`
	Year        int             `json:"year,omitempty"`
	Track       int             `json:"track,omitempty"`
	Progress    *streamProgress `json:"progress,omitempty"`
}

// streamProgress is where the track has got to.
type streamProgress struct {
	TrackProgress int `json:"track_progress"`
	TrackDuration int `json:"track_duration"`
	PlaybackSpeed int `json:"playback_speed"`
}

type playerState struct {
	Volume            int      `json:"volume"`
	Muted             bool     `json:"muted"`
	StaticDelayMs     int      `json:"static_delay_ms"`
	RequiredLeadMs    int      `json:"required_lead_time_ms"`
	MinBufferMs       int      `json:"min_buffer_ms"`
	SupportedCommands []string `json:"supported_commands"`
}

type clientState struct {
	Available bool         `json:"available"`
	Player    *playerState `json:"player,omitempty"`
}

type clientTime struct {
	ClientTransmitted int64 `json:"client_transmitted"`
}

type clientGoodbye struct {
	Reason string `json:"reason"`
}

type pairFinalize struct {
	LongTermPSK string `json:"long_term_psk"`
}

type pairAbort struct {
	Reason string `json:"reason"`
}

// ── server → client ─────────────────────────────────────────────────────────

type serverHello struct {
	Name string `json:"name"`
}

type activatePairing struct {
	Method string `json:"method"`
}

type serverActivate struct {
	Activities  []string         `json:"activities"`
	ActiveRoles *[]string        `json:"active_roles"`
	Pairing     *activatePairing `json:"pairing"`
}

type serverTime struct {
	ClientTransmitted int64 `json:"client_transmitted"`
	ServerReceived    int64 `json:"server_received"`
	ServerTransmitted int64 `json:"server_transmitted"`
}

type streamStart struct {
	Player *streamFormat `json:"player"`
}

type streamRoles struct {
	Roles []string `json:"roles"`
}

// covers reports whether a stream/clear or stream/end applies to the player.
// Omitted roles means every stream.
func (r streamRoles) covers(role string) bool {
	if len(r.Roles) == 0 {
		return true
	}
	for _, x := range r.Roles {
		if x == role {
			return true
		}
	}
	return false
}

type groupUpdate struct {
	PlaybackState string `json:"playback_state"`
	GroupID       string `json:"group_id"`
	GroupName     string `json:"group_name"`
}

type playerCommand struct {
	Command string `json:"command"`
	Volume  *int   `json:"volume"`
	Mute    *bool  `json:"mute"`
	// 9.1.1 names it static_delay_ms with command set_static_delay; the
	// current spec output_delay_ms with set_output_delay. Both are taken.
	StaticDelayMs *int `json:"static_delay_ms"`
	OutputDelayMs *int `json:"output_delay_ms"`
}

type serverCommand struct {
	Player *playerCommand `json:"player"`
}

// controllerState is what the server says the transport can do.
//
// The commands in it are the ones the server will accept, so the panel
// only offers what the server has said it can do.
type controllerState struct {
	SupportedCommands []string `json:"supported_commands"`
	Volume            int      `json:"volume"`
	Muted             bool     `json:"muted"`
	Repeat            string   `json:"repeat"`
	Shuffle           bool     `json:"shuffle"`
	// SeekMaxMs is the furthest a seek may go. It is only there when the
	// server knows how long the track is, and without it a seek to an
	// absolute position is refused.
	SeekMaxMs *int64 `json:"seek_max_ms,omitempty"`
}

// controllerCommand is a command for the transport.
//
// The fields are only used by the commands that take them, and the
// server checks that: a volume with a play command is refused.
type controllerCommand struct {
	Command    string `json:"command"`
	Volume     *int   `json:"volume,omitempty"`
	Mute       *bool  `json:"mute,omitempty"`
	PositionMs *int64 `json:"position_ms,omitempty"`
	OffsetMs   *int64 `json:"offset_ms,omitempty"`
}

// clientCommand is what the panel asks the transport to do.
//
// It is the controller role's message. The player role's message is the
// volume and the mute, which this device already handles, so only the
// controller's is here.
type clientCommand struct {
	Controller *controllerCommand `json:"controller,omitempty"`
}
