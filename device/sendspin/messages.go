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
	// The commands the player takes are in client/state, not here.
	//
	// They were in the hello in an earlier version of the protocol, and
	// a server that speaks that version still accepts them there — but a
	// server that speaks the current one says the client is not
	// conforming, and treats it as a client from before the change,
	// which changes how the handshake is done.
	SupportedCommands []string `json:"supported_commands,omitempty"`
}

// pairMethods are the pairing methods the panel offers.
//
// It is an object keyed by the method, which is what the current
// protocol has. It used to be a list of {method, ...}, and a server
// that speaks the current one says a client that sends the list is not
// conforming.
type pairMethods struct {
	// PairingPSK is the pre-shared key the server pairs with.
	PairingPSK *pairMethod `json:"pairing_psk,omitempty"`
}

// pairMethod describes one pairing method.
type pairMethod struct {
	// Locations is where the operator finds the secret.
	//
	// It is a hint for the person setting the device up, so it is
	// optional.
	Locations []string `json:"locations,omitempty"`
}

type unpairedAccess struct {
	Enabled bool `json:"enabled"`
}

type clientHello struct {
	Name                 string         `json:"name"`
	SupportedRoles       []string       `json:"supported_roles"`
	DeviceInfo           *deviceInfo    `json:"device_info,omitempty"`
	PlayerSupport        *playerSupport `json:"player@v1_support,omitempty"`
	SupportedPairMethods pairMethods    `json:"supported_pair_methods"`
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
	// State says whether the player is in step with the server's clock:
	// "synchronized", or "error" when it is not.
	//
	// The spec makes the timing fields below required, and a server that
	// sees a player without them treats it as a legacy one and can be set
	// to refuse it.
	State  string `json:"state,omitempty"`
	Volume int    `json:"volume"`
	Muted  bool   `json:"muted"`
	// StaticDelayMs is how far behind the server's clock the panel's audio
	// is played. It was called output_delay_ms before the spec renamed it,
	// and it is sent under both names: a server ignores a field it does
	// not know.
	//
	// It is zero, and the command to set it is not offered. It is for a
	// chain beyond the panel's own port, which it does not have; the
	// panel's own latency is compensated where the audio is placed.
	StaticDelayMs int `json:"static_delay_ms"`
	// OutputDelayMs is how far behind the server's clock the panel's
	// audio is played.
	OutputDelayMs int `json:"output_delay_ms"`
	// RequiredLeadMs is how far ahead of the clock the server sends.
	RequiredLeadMs int `json:"required_lead_time_ms"`
	// MinBufferMs is how much audio the panel keeps queued.
	MinBufferMs int `json:"min_buffer_ms"`
	// SupportedCommands are the commands the player takes.
	//
	// set_static_delay was renamed to set_output_delay, and a server
	// that speaks the current protocol says a client that declares the
	// old name is not conforming.
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
