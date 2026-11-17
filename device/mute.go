package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"

	"github.com/vithurshanselvarajah/Home-Panel-Kiosk/audio/latch"
)

type muteController struct {
	log *slog.Logger

	mu sync.Mutex

	latch *latch.Mute

	muted bool

	known bool

	onChange func(muted bool)
}

func newMuteController(log *slog.Logger) *muteController {
	m := &muteController{log: log}

	hw, err := latch.Open()
	if err != nil {
		log.Info("the microphone mute is a software switch on this board", "reason", err)
		return m
	}
	m.latch = hw

	if state, err := hw.Get(); err == nil {
		m.muted, m.known = state, true
		log.Info("the microphone mute is the hardware latch", "muted", state)
	} else {
		log.Warn("the microphone latch could not be read", "error", err)
	}
	return m
}

func (m *muteController) Set(muted bool) error {
	m.mu.Lock()
	hw := m.latch
	current := m.muted
	m.mu.Unlock()

	if hw == nil {
		m.setState(muted)
		return nil
	}

	if current == muted && m.known {
		return nil
	}
	if err := hw.Set(muted); err != nil {
		return err
	}

	state, err := hw.Get()
	if err == nil {
		m.setState(state)
	}
	return nil
}

func (m *muteController) Get() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.muted
}

func (m *muteController) Known() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.known
}

func (m *muteController) OnChange(fn func(muted bool)) {
	m.mu.Lock()
	m.onChange = fn
	m.mu.Unlock()
}

func (m *muteController) setState(muted bool) {
	m.mu.Lock()
	changed := !m.known || m.muted != muted
	m.muted, m.known = muted, true
	fn := m.onChange
	m.mu.Unlock()
	if changed && fn != nil {
		fn(muted)
	}
}

func (m *muteController) Watch(ctx context.Context) {
	m.mu.Lock()
	hw := m.latch
	m.mu.Unlock()
	if hw == nil {
		return
	}

	hw.OnChange(func(muted bool) {
		m.log.Info("the microphone was muted by the hardware", "muted", muted)
		m.setState(muted)
	})

	stop := make(chan struct{})
	go func() {
		<-ctx.Done()
		close(stop)
	}()
	hw.Watch(stop)
}

func (m *muteController) Close() error {
	m.mu.Lock()
	hw := m.latch
	m.latch = nil
	m.mu.Unlock()
	if hw == nil {
		return nil
	}
	return hw.Close()
}

const (
	micMuteLED = "micmute"
)

func setMicMuteLED(lit bool) error {
	root := "/sys/class/leds"
	entries, err := os.ReadDir(root)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		name := entry.Name()
		if !isMicMuteLED(name) {
			continue
		}
		brightness := "0"
		if lit {
			brightness = ledBrightness(root + "/" + name)
		}
		return os.WriteFile(root+"/"+name+"/brightness", []byte(brightness), 0o644)
	}
	return fmt.Errorf("the mute light %s was not found", micMuteLED)
}

func isMicMuteLED(name string) bool {
	_, label, found := strings.Cut(name, ":")
	if !found {
		label = name
	}
	return strings.EqualFold(label, micMuteLED)
}

func ledBrightness(path string) string {
	max, err := os.ReadFile(path + "/max_brightness")
	if err != nil {
		return "1"
	}
	level := strings.TrimSpace(string(max))
	if level == "" || level == "0" {
		return "1"
	}
	return level
}
