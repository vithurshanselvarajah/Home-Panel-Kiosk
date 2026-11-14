package main

import (
	"context"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/vithurshanselvarajah/Home-Panel-Kiosk/audio/latch"
)

// The microphone mute.
//
// On the Echo Show 8 the mute is a hardware latch: the button on the top
// of the unit flips it, and while it is flipped the microphones are cut
// at the board. The latch's position is read from the gpio-keys device as
// a switch, and it is the source of truth — the panel's switch, Home
// Assistant's switch and the button are all views of the same latch.
//
// So the mute is owned here, and everything else follows it.
//
// On a board whose mute is not a latch there is nothing to read, and the
// software mute stands on its own: the microphone's frames are silenced
// before anything sees them, which is what the latch would have done.

// muteController owns the microphone mute.
type muteController struct {
	log *slog.Logger

	mu sync.Mutex
	// latch is the hardware latch, when the board has one.
	latch *latch.Mute
	// muted is the state the application is using.
	muted bool
	// known says whether the state has been read from the hardware.
	known bool

	// onChange is called when the state changes.
	onChange func(muted bool)
}

// newMuteController makes the controller.
//
// It opens the hardware latch when the board has one. A board without one
// is not a failure: the software mute stands on its own.
func newMuteController(log *slog.Logger) *muteController {
	m := &muteController{log: log}

	hw, err := latch.Open()
	if err != nil {
		log.Info("the microphone mute is a software switch on this board", "reason", err)
		return m
	}
	m.latch = hw

	// The latch is read once at the start, so a unit that was muted
	// before the program came up starts muted rather than unmuting
	// itself by accident.
	if state, err := hw.Get(); err == nil {
		m.muted, m.known = state, true
		log.Info("the microphone mute is the hardware latch", "muted", state)
	} else {
		log.Warn("the microphone latch could not be read", "error", err)
	}
	return m
}

// Set turns the mute on or off.
//
// The hardware latch is asked first, and what it says afterwards is what
// the application uses: the latch is the source of truth, so the state is
// never what the application wanted but the hardware did not do.
//
// A board with no latch is a software mute, and it is said so.
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
	// The latch has already reported its new position, and setState has
	// been called by the watch. The state is read here as well so that a
	// mute asked for from Home Assistant is reflected at once rather
	// than at the next poll.
	state, err := hw.Get()
	if err == nil {
		m.setState(state)
	}
	return nil
}

// Get says whether the microphone is muted.
func (m *muteController) Get() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.muted
}

// Known says whether the state has been read from the hardware.
func (m *muteController) Known() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.known
}

// OnChange sets what is called when the mute changes.
func (m *muteController) OnChange(fn func(muted bool)) {
	m.mu.Lock()
	m.onChange = fn
	m.mu.Unlock()
}

// setState records the state and tells anyone watching.
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

// Watch follows the latch until the context is done.
//
// The button on the unit is the latch's, and this is how the application
// finds out that it moved.
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

// Close releases the latch.
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

// The mute's LED.
//
// The button's light follows the latch in hardware on the stock kernel,
// but the mainline wiring drives it from the LED subsystem, so it is set
// here from the latch's state.
//
// The LED is found by its function, which the device tree gives as
// LED_FUNCTION_MICMUTE.
const micMuteLED = "micmute"

// setMicMuteLED lights the mute button's light.
func setMicMuteLED(lit bool) error {
	root := "/sys/class/leds"
	entries, err := os.ReadDir(root)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		name := entry.Name()
		function, err := os.ReadFile(root + "/" + name + "/function")
		if err != nil {
			continue
		}
		if strings.TrimSpace(string(function)) != micMuteLED {
			continue
		}
		brightness := "0"
		if lit {
			max, err := os.ReadFile(root + "/" + name + "/max_brightness")
			if err != nil || strings.TrimSpace(string(max)) == "0" {
				brightness = "1"
			} else {
				brightness = strings.TrimSpace(string(max))
			}
		}
		return os.WriteFile(root+"/"+name+"/brightness", []byte(brightness), 0o644)
	}
	return nil
}

// The latch's own timing, for the wait after a change.
//
// The latch reports its new position within a moment of the pulse, and
// the watch polls for it. This is how long the watch waits between
// readings, and it is short enough that a button press is reflected
// while the finger is still on it.
const latchPollInterval = 200 * time.Millisecond
