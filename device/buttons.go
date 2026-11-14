package main

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"github.com/vithurshanselvarajah/Home-Panel-Kiosk/audio/evdev"
)

// The buttons on top of the device.
//
// The Echo Show 8 has three: volume up, volume down and the mute. They
// are read from the input nodes the kernel gives, and they are read here
// rather than by a script on the side, because the level is the device
// program's and the mute is the hardware latch's: both are the device
// program's business, and a button that went through a shell script and a
// file on the way would be a button whose meaning was spread across three
// programs.
//
// The volume buttons move the level on the curve, so a press is a step
// on the curve rather than a number of decibels: the same press is the
// same size of step wherever the level is.
//
// Ported from TECHO5 (github.com/HuskerMinion/techo5), MIT licensed.
// Copyright (c) 2026 Yuri Gelfand.

// The key codes the buttons report.
const (
	keyVolumeUp   = 115
	keyVolumeDown = 114
	keyMicMute    = 248
)

// notButtons are input devices that are not buttons, by name, though they
// declare key codes.
//
// The Echo Show 8's FocalTech touchscreen reports KEY_POWER (116) among its
// gesture keys, and on the second generation that is the mute button's
// code. A gesture read as a button would toggle the mute, so the
// touchscreen is left alone.
//
// The MT6358's own key driver reports its power key as KEY_POWER too. The
// mute button here is KEY_MICMUTE in gpio-keys, so the PMIC's 116 is never
// the mute, and is not read.
var notButtons = map[string]bool{"fts_ts": true, "mtk-pmic-keys": true}

// buttons reads the device's own buttons.
type buttons struct {
	log *slog.Logger

	mu      sync.Mutex
	devices []*evdev.Device

	// onVolume is called when a volume button is pressed.
	onVolume func(delta int)
	// onMute is called when the mute button is pressed.
	onMute func()
}

// newButtons makes the button reader.
func newButtons(log *slog.Logger) *buttons {
	return &buttons{log: log}
}

// Start opens the input nodes.
//
// The devices that are not buttons are closed and left out: the
// touchscreen declares the mute button's code among its gestures, and a
// gesture read as a button would toggle the mute.
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

// Close releases the nodes.
//
// Closing them is also what unblocks the readers: a read on an input node
// waits for a key, so there is no stopping one except by closing it
// underneath.
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

// Run reads the buttons until the context is done.
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

// watch reads one node until the context is done.
//
// The kernel autorepeats a key that is held down, so the repeat is the
// ramp: the level moves again and again while the button is down, which
// is what a person holding it down expects.
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
			// The press. The key autorepeats while it is held, so the
			// repeat is the ramp and nothing else is needed.
			if event.Value != 1 {
				continue
			}
			delta := 1
			if event.Code == keyVolumeDown {
				delta = -1
			}
			b.press(delta)
		case keyMicMute:
			// The mute button.
			//
			// The latch is the hardware's and the kernel flips it when
			// the button is pressed, so this is only a report: the latch
			// is read and it says where it ended up.
			if event.Value == 1 && b.onMute != nil {
				b.onMute()
			}
		}
	}
}

// press moves the level by one step.
func (b *buttons) press(delta int) {
	if b.onVolume != nil {
		b.onVolume(delta)
	}
}
