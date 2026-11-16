package speaker

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/vithurshanselvarajah/Home-Panel-Kiosk/audio/alsa"
)

const (
	acquireRetry    = 250 * time.Millisecond
	acquireAttempts = 8
)

// Start takes the playback device.
//
// The device program owns the card through its own backend, so the open
// here is the external playback when one is set and the card otherwise.
//
// A card that is busy is taken after a few tries: something else may be
// holding it, and a card that is open twice is a card that ALSA asserts
// on, and an assertion takes the process down.
func (p *Player) Start(context.Context) error {
	err := p.open()
	if err == nil || !errors.Is(err, alsa.ErrBusy) {
		return err
	}

	slog.Warn("playback device busy, taking it after a few tries", "err", err)
	for range acquireAttempts {
		time.Sleep(acquireRetry)

		err = p.open()
		if err == nil {
			slog.Info("playback device acquired")
			return nil
		}
		if !errors.Is(err, alsa.ErrBusy) {
			return err
		}
	}
	return err
}

// Acquire is New and Start together, for a tool that wants the speaker for
// the length of one command.
func Acquire() (*Player, error) {
	p := New()
	if err := p.Start(context.Background()); err != nil {
		return nil, err
	}
	return p, nil
}
