package main

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"github.com/vithurshanselvarajah/Home-Panel-Kiosk/audio/evdev"
)

// Ported from TECHO5 (github.com/HuskerMinion/techo5), MIT licensed.
// Copyright (c) 2026 Yuri Gelfand.

const (
	keyVolumeUp   = 115
	keyVolumeDown = 114
	keyMicMute    = 248
)

var notButtons = map[string]bool{"fts_ts": true, "mtk-pmic-keys": true}

type buttons struct {
	log *slog.Logger

	mu      sync.Mutex
	devices []*evdev.Device

	onVolume func(delta int)

	onMute func()
}

func newButtons(log *slog.Logger) *buttons {
	return &buttons{log: log}
}

func (b *buttons) Start(context.Context) error {
	devices, err := evdev.List()
	if err != nil {
		return fmt.Errorf("buttons: listing the input devices: %w", err)
	}

	kept := make([]*evdev.Device, 0, len(devices))
	for _, d := range devices {
		if notButtons[d.Name] {
			_ = d.Close()
			continue
		}
		kept = append(kept, d)
	}
	if len(kept) == 0 {
		return fmt.Errorf("buttons: there are no input devices")
	}

	b.mu.Lock()
	b.devices = kept
	b.mu.Unlock()

	b.log.Debug("the buttons are ready", "devices", len(kept))
	return nil
}

func (b *buttons) Close() error {
	b.mu.Lock()
	devices := b.devices
	b.devices = nil
	b.mu.Unlock()

	for _, d := range devices {
		_ = d.Close()
	}
	return nil
}

func (b *buttons) Run(ctx context.Context) error {
	b.mu.Lock()
	devices := b.devices
	b.mu.Unlock()

	failed := make(chan error, len(devices))
	for _, d := range devices {
		go func(d *evdev.Device) { failed <- b.watch(ctx, d) }(d)
	}

	select {
	case <-ctx.Done():
		return nil
	case err := <-failed:
		return err
	}
}

func (b *buttons) watch(ctx context.Context, d *evdev.Device) error {
	for {
		event, err := d.Read()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("buttons: reading %s: %w", d.Name, err)
		}
		if event.Type != evdev.EvKey {
			continue
		}

		switch event.Code {
		case keyVolumeUp, keyVolumeDown:

			if event.Value != 1 {
				continue
			}
			delta := 1
			if event.Code == keyVolumeDown {
				delta = -1
			}
			b.press(delta)
		case keyMicMute:

			if event.Value == 1 && b.onMute != nil {
				b.onMute()
			}
		}
	}
}

func (b *buttons) press(delta int) {
	if b.onVolume != nil {
		b.onVolume(delta)
	}
}
