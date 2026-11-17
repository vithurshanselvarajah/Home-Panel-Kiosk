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
// The LED is found by its name rather than by its function. The device
// tree gives the function as LED_FUNCTION_MICMUTE, but the LED class only
// exposes a "function" file when the kernel has the attribute, and this
// one does not: the light is simply named "red:micmute", and a lookup by
// function finds nothing and leaves the mute silent.
const (
	// micMuteLED is the name of the mute button's light, as the LED
	// class names it: the colour, then a colon, then the label from
	// the device tree.
	micMuteLED = "micmute"
)

// setMicMuteLED lights the mute button's light.
//
// The light is the red bar along the top of the unit, and it is what
// says at a glance that the microphones are cut.
//
// Writing the brightness needs to be permitted for the program's user,
// which the udev rule in the kernel's install does; without it the write
// is refused and the error says so rather than the mute being silently
// dark.
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

// isMicMuteLED says whether a name in the LED class is the mute light.
//
// The name is the colour and the label, joined by a colon: "red:micmute".
// The label is matched on its own, so a light under a different colour
// is still found.
func isMicMuteLED(name string) bool {
	_, label, found := strings.Cut(name, ":")
	if !found {
		label = name
	}
	return strings.EqualFold(label, micMuteLED)
}

// ledBrightness is the level that lights an LED fully.
//
// It is the LED's own maximum, which is what a light with a range wants
// rather than a number that happens to be bright.
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
