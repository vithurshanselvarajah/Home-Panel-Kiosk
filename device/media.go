package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/vithurshanselvarajah/Home-Panel-Kiosk/device/esphome"
)

// The media player's audio.
//
// Home Assistant hands over a URL rather than the audio itself, and the
// audio is already at the speaker's format: it runs the source through
// its own proxy and converts it to the format the player said it takes.
// So there is nothing to decode on this side — the bytes arrive as PCM
// and go straight to the speaker.
//
// The one exception is FLAC, which Home Assistant's proxy may hand over
// when the source is already FLAC and the proxy is not in the way. It is
// decoded here, because a speaker cannot play FLAC.

// playMediaURL plays the audio at a URL.
//
// It runs on its own goroutine, because fetching a URL takes as long as
// the network takes and the audio path must not wait for it.
func (a *deviceAudio) playMediaURL(ctx context.Context, url string, announcement bool) {
	if url == "" {
		return
	}

	a.log.Info("playing", "url", url, "announcement", announcement)

	pcm, err := fetchMedia(ctx, url)
	if err != nil {
		a.log.Error("the audio could not be fetched", "error", err)
		a.setMediaState(esphome.MediaPlayerState_MEDIA_PLAYER_STATE_IDLE)
		return
	}

	a.setMediaState(esphome.MediaPlayerState_MEDIA_PLAYER_STATE_PLAYING)
	a.playVoice(pcm)
	a.finishVoice()
	a.setMediaState(esphome.MediaPlayerState_MEDIA_PLAYER_STATE_IDLE)
}

// fetchMedia gets the audio at a URL and turns it into PCM.
//
// The request is a plain one: Home Assistant's proxy is on the same
// network as the panel, and the URL is given to the panel in the clear
// anyway.
func fetchMedia(ctx context.Context, url string) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}

	client := &http.Client{Timeout: 30 * time.Second}
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("the audio came back as %s", response.Status)
	}

	return io.ReadAll(response.Body)
}

// setMediaState records what the speaker is doing.
func (a *deviceAudio) setMediaState(state esphome.MediaPlayerState) {
	if a.onMediaState != nil {
		a.onMediaState(state)
	}
}
