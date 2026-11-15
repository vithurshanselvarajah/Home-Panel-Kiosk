package esphome

import (
	"log/slog"
	"sync"
)

// The media player entity.
//
// It is the panel's speaker as Home Assistant sees it. Two things use it:
//
//   - the reply to a voice turn comes back as audio on this entity, and
//   - Music Assistant plays music through it.
//
// The name is empty on purpose. Home Assistant composes the device name and
// the entity name itself, so a name here is rendered twice — "Home Panel
// Voice Home Panel Voice". An empty name is its own convention for the
// device's primary entity.

// The media player's feature flags.
//
// They say what the speaker can do, and they are what Home Assistant's
// player card shows. The set is the one a voice satellite needs: play and
// pause, stop, the volume, the mute, and announcements.
const (
	// mediaPlayerPause says the player can be paused.
	mediaPlayerPause uint32 = 1 << 0
	// mediaPlayerVolumeSet says the volume can be set.
	mediaPlayerVolumeSet uint32 = 1 << 2
	// mediaPlayerPlayMedia says a URL can be played.
	mediaPlayerPlayMedia uint32 = 1 << 9
	// mediaPlayerStop says the player can be stopped.
	mediaPlayerStop uint32 = 1 << 12
	// mediaPlayerPlay says playback can be resumed.
	mediaPlayerPlay uint32 = 1 << 14
	// mediaPlayerAnnounce says the player can make an announcement, which
	// is what interrupts the music and speaks over it.
	mediaPlayerAnnounce uint32 = 1 << 20
	// mediaPlayerBrowseMedia says the player can be sent something to
	// play from Home Assistant's own media browser.
	//
	// Without it, the player shows up with no way to choose anything to
	// play on it: the service works, but there is nothing to pick from.
	mediaPlayerBrowseMedia uint32 = 1 << 17
)

// The formats the speaker takes.
//
// Home Assistant runs the audio through its own proxy and hands it over
// already at the wire format, so the device does not have to decode
// anything.
//
// The rate is the speaker's own: the panel plays at 48 kHz, and the
// capture is at 16 kHz. The reply to a voice turn arrives at 16 kHz
// because that is the rate the conversation runs at, and the panel
// resamples it on the way to the speaker.
const (
	// mediaPlayerSampleRate is the rate the speaker plays at.
	mediaPlayerSampleRate = 48000
	// mediaPlayerChannels is how many channels the speaker takes.
	mediaPlayerChannels = 1
	// mediaPlayerSampleBytes is how many bytes each sample takes.
	mediaPlayerSampleBytes = 2
)

// MediaPlayer is the panel's speaker.
type MediaPlayer struct {
	// log is where the messages go.
	log *slog.Logger

	mu sync.Mutex
	// state is what the player is doing.
	state MediaPlayerState
	// muted is whether the player is muted.
	muted bool
	// volume is the level, 0.0 to 1.0.
	//
	// The player owns the volume. The panel's own slider, the music
	// server and the system mixer all follow it, because it is one
	// setting: the volume of the room.
	volume float32

	// onPlay is called with a URL to play.
	onPlay func(url string, announcement bool)
	// onCommand is called with a command: play, pause, stop, mute.
	onCommand func(command MediaPlayerCommand)
	// onVolume is called with a level, 0.0 to 1.0.
	onVolume func(volume float32)
	// onMute is called with whether the player is muted.
	onMute func(muted bool)
	// onState is called when the player changes what it is doing.
	onState func(state MediaPlayerState)
}

// NewMediaPlayer makes a media player.
//
// The player starts at full volume rather than at zero.
//
// It has to: the volume is pushed to Home Assistant as the speaker's
// state, and Home Assistant sends the volume back as a command. A player
// that started at zero would tell Home Assistant the speaker is silent,
// and Home Assistant would then set it to silent for real.
func NewMediaPlayer(log *slog.Logger) *MediaPlayer {
	if log == nil {
		log = slog.Default()
	}
	return &MediaPlayer{
		log:    log,
		state:  MediaPlayerState_MEDIA_PLAYER_STATE_IDLE,
		volume: 1.0,
	}
}

// Entity describes the speaker to Home Assistant.
func (m *MediaPlayer) Entity() *Entity {
	return &Entity{
		Kind:     KindMediaPlayer,
		Name:     "",
		ObjectID: "media_player",
		Icon:     "mdi:account-box",
	}
}

// Message builds the listing message for the speaker.
func (m *MediaPlayer) Message(key uint32) (entityMessage, error) {
	return entityMessage{
		Type: TypeListEntitiesMediaPlayerResponse,
		Body: &ListEntitiesMediaPlayerResponse{
			Key:           key,
			Name:          "",
			ObjectId:      "media_player",
			Icon:          "mdi:account-box",
			SupportsPause: true,
			// The mute is not offered. There is one mute on this
			// device and it is the microphone's, and it is the
			// hardware latch's: only the button on top of the unit
			// moves it. The speaker has a level rather than a mute.
			FeatureFlags: mediaPlayerPause | mediaPlayerVolumeSet |
				mediaPlayerPlayMedia | mediaPlayerStop | mediaPlayerPlay |
				mediaPlayerAnnounce | mediaPlayerBrowseMedia,
			SupportedFormats: []*MediaPlayerSupportedFormat{
				{
					Purpose:     MediaPlayerFormatPurpose_MEDIA_PLAYER_FORMAT_PURPOSE_DEFAULT,
					Format:      "wav",
					SampleRate:  mediaPlayerSampleRate,
					NumChannels: mediaPlayerChannels,
					SampleBytes: mediaPlayerSampleBytes,
				},
				{
					Purpose:     MediaPlayerFormatPurpose_MEDIA_PLAYER_FORMAT_PURPOSE_ANNOUNCEMENT,
					Format:      "wav",
					SampleRate:  mediaPlayerSampleRate,
					NumChannels: mediaPlayerChannels,
					SampleBytes: mediaPlayerSampleBytes,
				},
			},
		},
	}, nil
}

// StateMessage builds the message that reports what the player is doing.
func (m *MediaPlayer) StateMessage(key uint32) *MediaPlayerStateResponse {
	m.mu.Lock()
	defer m.mu.Unlock()
	return &MediaPlayerStateResponse{
		Key:    key,
		State:  m.state,
		Volume: m.volume,
		Muted:  m.muted,
	}
}

// HandleCommand applies a command from Home Assistant.
//
// The volume and the mute are handled here, because they are the
// player's own state. The playing and the pausing are handed on, because
// the panel is what actually holds the sound card.
func (m *MediaPlayer) HandleCommand(key uint32, request *MediaPlayerCommandRequest) {
	if request.GetHasVolume() {
		volume := request.GetVolume()
		m.mu.Lock()
		m.volume = volume
		m.mu.Unlock()
		m.log.Info("the volume was set", "volume", volume)
		if m.onVolume != nil {
			m.onVolume(volume)
		}
	}

	if request.GetHasCommand() {
		command := request.GetCommand()
		switch command {
		case MediaPlayerCommand_MEDIA_PLAYER_COMMAND_MUTE:
			m.setMuted(true)
		case MediaPlayerCommand_MEDIA_PLAYER_COMMAND_UNMUTE:
			m.setMuted(false)
		default:
			m.log.Info("a media command", "command", command)
			if m.onCommand != nil {
				m.onCommand(command)
			}
		}
	}

	if request.GetHasMediaUrl() {
		announcement := request.GetAnnouncement()
		m.log.Info("a URL to play", "announcement", announcement)
		if announcement {
			m.SetState(MediaPlayerState_MEDIA_PLAYER_STATE_ANNOUNCING)
		} else {
			m.SetState(MediaPlayerState_MEDIA_PLAYER_STATE_PLAYING)
		}
		if m.onPlay != nil {
			m.onPlay(request.GetMediaUrl(), announcement)
		}
	}
}

// setMuted records the mute state.
func (m *MediaPlayer) setMuted(muted bool) {
	m.mu.Lock()
	m.muted = muted
	m.mu.Unlock()
	m.log.Info("the player was muted", "muted", muted)
	if m.onMute != nil {
		m.onMute(muted)
	}
}

// SetState records what the player is doing and tells Home Assistant.
//
// The state is pushed to every client that is connected, so the player
// card in Home Assistant is right without anybody asking.
func (m *MediaPlayer) SetState(state MediaPlayerState) {
	m.mu.Lock()
	if m.state == state {
		m.mu.Unlock()
		return
	}
	m.state = state
	m.mu.Unlock()

	m.log.Info("the player is now", "state", state)
	if m.onState != nil {
		m.onState(state)
	}
}

// SetVolume records the volume level.
//
// The player owns it. The panel's own slider and the system mixer follow
// it, and the music server is told about it, because it is one setting:
// the volume of the room.
func (m *MediaPlayer) SetVolume(volume float32) {
	m.mu.Lock()
	m.volume = volume
	m.mu.Unlock()
}

// Volume returns the level, 0.0 to 1.0.
func (m *MediaPlayer) Volume() float32 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.volume
}

// SetMuted records the mute state.
func (m *MediaPlayer) SetMuted(muted bool) {
	m.setMuted(muted)
}

// Muted reports whether the player is muted.
func (m *MediaPlayer) Muted() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.muted
}

// OnPlay sets the callback for a URL to play.
func (m *MediaPlayer) OnPlay(fn func(url string, announcement bool)) { m.onPlay = fn }

// OnCommand sets the callback for a command.
func (m *MediaPlayer) OnCommand(fn func(command MediaPlayerCommand)) { m.onCommand = fn }

// OnVolume sets the callback for the volume.
func (m *MediaPlayer) OnVolume(fn func(volume float32)) { m.onVolume = fn }

// OnMute sets the callback for the mute.
func (m *MediaPlayer) OnMute(fn func(muted bool)) { m.onMute = fn }

// OnState sets the callback for the state changing.
func (m *MediaPlayer) OnState(fn func(state MediaPlayerState)) { m.onState = fn }
