package esphome

import (
	"log/slog"
	"sync"
)

const (
	mediaPlayerPause uint32 = 1 << 0

	mediaPlayerVolumeSet uint32 = 1 << 2

	mediaPlayerPlayMedia uint32 = 1 << 9

	mediaPlayerStop uint32 = 1 << 12

	mediaPlayerPlay uint32 = 1 << 14

	mediaPlayerAnnounce uint32 = 1 << 20

	mediaPlayerBrowseMedia uint32 = 1 << 17
)

const (
	mediaPlayerSampleRate = 48000

	mediaPlayerChannels = 1

	mediaPlayerSampleBytes = 2
)

type MediaPlayer struct {
	log *slog.Logger

	mu sync.Mutex

	state MediaPlayerState

	muted bool

	volume float32

	onPlay func(url string, announcement bool)

	onCommand func(command MediaPlayerCommand)

	onVolume func(volume float32)

	onMute func(muted bool)

	onState func(state MediaPlayerState)
}

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

func (m *MediaPlayer) Entity() *Entity {
	return &Entity{
		Kind:     KindMediaPlayer,
		Name:     "",
		ObjectID: "media_player",
		Icon:     "mdi:account-box",
	}
}

func (m *MediaPlayer) Message(key uint32) (entityMessage, error) {
	return entityMessage{
		Type: TypeListEntitiesMediaPlayerResponse,
		Body: &ListEntitiesMediaPlayerResponse{
			Key:           key,
			Name:          "",
			ObjectId:      "media_player",
			Icon:          "mdi:account-box",
			SupportsPause: true,

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

func (m *MediaPlayer) setMuted(muted bool) {
	m.log.Info("the speaker's mute was ignored", "muted", muted)
	if m.onMute != nil {
		m.onMute(muted)
	}
}

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

func (m *MediaPlayer) SetVolume(volume float32) {
	m.mu.Lock()
	m.volume = volume
	m.mu.Unlock()
}

func (m *MediaPlayer) Volume() float32 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.volume
}

func (m *MediaPlayer) SetMuted(muted bool) {
	m.setMuted(muted)
}

func (m *MediaPlayer) Muted() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.muted
}

func (m *MediaPlayer) OnPlay(fn func(url string, announcement bool)) { m.onPlay = fn }

func (m *MediaPlayer) OnCommand(fn func(command MediaPlayerCommand)) { m.onCommand = fn }

func (m *MediaPlayer) OnVolume(fn func(volume float32)) { m.onVolume = fn }

func (m *MediaPlayer) OnMute(fn func(muted bool)) { m.onMute = fn }

func (m *MediaPlayer) OnState(fn func(state MediaPlayerState)) { m.onState = fn }
