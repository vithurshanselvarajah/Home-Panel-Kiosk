package sendspin

import "encoding/json"

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

	SupportedCommands []string `json:"supported_commands,omitempty"`
}

type pairMethods struct {
	PairingPSK *pairMethod `json:"pairing_psk,omitempty"`
}

type pairMethod struct {
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

type serverState struct {
	Metadata *streamMetadata `json:"metadata,omitempty"`

	Controller *controllerState `json:"controller,omitempty"`

	MetadataNull bool `json:"-"`
}

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

type streamProgress struct {
	TrackProgress int `json:"track_progress"`
	TrackDuration int `json:"track_duration"`
	PlaybackSpeed int `json:"playback_speed"`
}

type playerState struct {
	State  string `json:"state,omitempty"`
	Volume int    `json:"volume"`
	Muted  bool   `json:"muted"`

	StaticDelayMs int `json:"static_delay_ms"`

	OutputDelayMs int `json:"output_delay_ms"`

	RequiredLeadMs int `json:"required_lead_time_ms"`

	MinBufferMs int `json:"min_buffer_ms"`

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

	StaticDelayMs *int `json:"static_delay_ms"`
	OutputDelayMs *int `json:"output_delay_ms"`
}

type serverCommand struct {
	Player *playerCommand `json:"player"`
}

type controllerState struct {
	SupportedCommands []string `json:"supported_commands"`
	Volume            int      `json:"volume"`
	Muted             bool     `json:"muted"`
	Repeat            string   `json:"repeat"`
	Shuffle           bool     `json:"shuffle"`

	SeekMaxMs *int64 `json:"seek_max_ms,omitempty"`
}

type controllerCommand struct {
	Command    string `json:"command"`
	Volume     *int   `json:"volume,omitempty"`
	Mute       *bool  `json:"mute,omitempty"`
	PositionMs *int64 `json:"position_ms,omitempty"`
	OffsetMs   *int64 `json:"offset_ms,omitempty"`
}

type clientCommand struct {
	Controller *controllerCommand `json:"controller,omitempty"`
}
