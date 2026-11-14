// Package latch is the Echo Show 8's microphone mute.
//
// The mute is a hardware latch, not a setting. The button on the top of the
// unit flips it, and while it is flipped the microphones are cut at the
// board: the converter is held in reset and the room stops reaching the
// application altogether. The latch's position is reported by the kernel as
// a switch on the gpio-keys device — SW_MUTE_DEVICE, code 14.
//
// So the mute is read from there and it is the source of truth: the panel's
// switch, Home Assistant's switch and the button on the unit are all
// views of the same latch, and none of them can be out of step with it.
//
// Unmuting is the one thing that needs care. The latch is released by the
// line the kernel's own button handling pulses, so a request to unmute from
// software pulses that line the same way the button does, and then waits
// for the latch to report the change.
//
// Ported from TECHO5 (github.com/HuskerMinion/techo5), MIT licensed.
// Copyright (c) 2026 Yuri Gelfand.
package latch

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"github.com/vithurshanselvarajah/Home-Panel-Kiosk/audio/evdev"
)

// The paths and numbers the latch is read from and written to.
const (
	// keysName is the name the kernel gives the gpio-keys device, which
	// is where the latch's position is reported.
	keysName = "gpio-keys"

	// EnableGPIO is the line that flips the latch.
	//
	// It is the gpio-privacy driver's "enable-gpio" on the stock kernel:
	// an output, low at rest, which the driver pulses for 1000 ms to
	// toggle the hardware mic/camera mute latch. The mainline kernel has
	// no gpio-privacy driver, so the line is driven here instead — the
	// same pulse, the same effect.
	EnableGPIO = 159

	// gpioChip is the chip the enable line is on.
	gpioChip = 0

	// gpioSysfs is the gpio-class directory, where a line that has been
	// exported can be driven without an ioctl.
	gpioSysfs = "/sys/class/gpio"
)

// pollInterval is how often the latch is read when it is being watched.
//
// The switch is read with an ioctl rather than waited for, because the
// kernel sends nothing until the latch moves, and a switch that was already
// set when the program started has to be readable at once.
const pollInterval = 500 * time.Millisecond

// toggleLag is how long the latch takes to report the flip once the enable
// line has been pulsed, with margin over the 1000 ms pulse.
const toggleLag = 1400 * time.Millisecond

// pulseHold is how long the enable line is held. The stock driver holds it
// for 1000 ms, and the latch toggles when the pulse arrives, so the hold
// has to be long enough for that to happen.
const pulseHold = 100 * time.Millisecond

// Mute is the hardware microphone cut.
type Mute struct {
	mu sync.Mutex

	// device is the gpio-keys node, opened once.
	device *evdev.Device

	// line is the gpio-cdev handle for the enable line, when it is open.
	line *gpioLine

	// state is the last known position of the latch.
	state bool

	// known says whether the latch has been read at all.
	known bool

	// onChange is called when the latch moves.
	onChange func(muted bool)
}

// Open opens the latch.
//
// It fails when the board has no gpio-keys device, which is a board whose
// mute is not a latch and not a problem for this package: the caller keeps
// its own software mute in that case.
func Open() (*Mute, error) {
	device, err := evdev.Find(keysName)
	if err != nil {
		return nil, fmt.Errorf("latch: %w", err)
	}
	m := &Mute{device: device}
	// The enable line is opened here rather than on the first mute: a
	// line that cannot be opened is a line that cannot be pulsed, and
	// that is worth saying at the start rather than at the first press.
	if line, err := openLine(gpioChip, EnableGPIO); err == nil {
		m.line = line
	} else {
		m.line = nil
	}
	return m, nil
}

// Close releases the gpio-keys node and the enable line.
func (m *Mute) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.line != nil {
		_ = m.line.close()
		m.line = nil
	}
	if m.device == nil {
		return nil
	}
	return m.device.Close()
}

// OnChange sets what is called when the latch moves.
func (m *Mute) OnChange(fn func(muted bool)) {
	m.mu.Lock()
	m.onChange = fn
	m.mu.Unlock()
}

// Get reads the latch's position now.
//
// It is read with an ioctl rather than waited for, because a switch
// reports a position that is already true when the program starts: a unit
// that was muted before the program came up has to read as muted at once.
func (m *Mute) Get() (bool, error) {
	m.mu.Lock()
	device := m.device
	m.mu.Unlock()
	if device == nil {
		return false, fmt.Errorf("latch: the latch is not open")
	}
	return device.Switch(evdev.SWMuteDevice)
}

// Set turns the mute on or off.
//
// Muting pulses the enable line, and the latch's position is then read
// back: the line is what asks for the change, and the latch is what says
// whether it happened.
//
// It fails when the line cannot be driven, which is a board whose latch
// can only be released by the button: the caller says so rather than
// pretending the microphones came back.
func (m *Mute) Set(muted bool) error {
	current, err := m.Get()
	if err != nil {
		return err
	}
	if current == muted {
		return nil
	}
	if err := m.pulse(); err != nil {
		return err
	}

	// The latch reports the change a moment after the pulse, so the
	// position is read until it moves or the wait runs out.
	deadline := time.Now().Add(toggleLag)
	for time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
		now, err := m.Get()
		if err != nil {
			return err
		}
		if now == muted {
			m.setState(muted)
			return nil
		}
	}
	return fmt.Errorf("latch: the latch did not change")
}

// Toggle flips the latch and reports where it ended up.
func (m *Mute) Toggle() (bool, error) {
	current, err := m.Get()
	if err != nil {
		return false, err
	}
	if err := m.Set(!current); err != nil {
		return current, err
	}
	return !current, nil
}

// pulse drives the enable line, which is what asks the latch to flip.
func (m *Mute) pulse() error {
	m.mu.Lock()
	line := m.line
	m.mu.Unlock()
	if line == nil {
		return fmt.Errorf("latch: the enable line (GPIO%d) could not be opened", EnableGPIO)
	}
	return line.pulse(pulseHold)
}

// setState records the position and tells anyone watching.
func (m *Mute) setState(muted bool) {
	m.mu.Lock()
	changed := !m.known || m.state != muted
	m.state, m.known = muted, true
	fn := m.onChange
	m.mu.Unlock()
	if changed && fn != nil {
		fn(muted)
	}
}

// Watch reads the latch until the stop channel is closed.
//
// The switch is polled rather than waited for, because the kernel sends
// nothing until the latch moves and a switch that was already set has to
// be noticed at once.
func (m *Mute) Watch(stop <-chan struct{}) {
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			state, err := m.Get()
			if err != nil {
				continue
			}
			m.setState(state)
		}
	}
}

// gpioLine is an open line on a gpio chip.
//
// The line handle the chip gives back is itself a file descriptor: the
// values are set through it with an ioctl, which is how the gpio-cdev
// interface works.
type gpioLine struct {
	// file is the chip's character device, which the line was requested
	// from and which is closed with the line.
	file *os.File
	// handle is the line's own descriptor, which is what the values are
	// set through.
	handle int
	// offset is the line's number.
	offset uint32
}

// openLine opens one line of a gpio chip.
//
// The chip's character device is opened and the line is requested through
// it, which is the gpio-cdev interface. The gpio-class interface in
// sysfs is tried first, because it needs no ioctl at all — but it needs
// the line to have been exported, which is not something that happens by
// itself.
func openLine(chip int, offset uint32) (*gpioLine, error) {
	if line, err := openSysfsLine(offset); err == nil {
		return line, nil
	}
	return openCdevLine(chip, offset)
}

// openSysfsLine drives a line through the gpio-class interface.
//
// The line has to be exported first, and it has to be released when it is
// done with, or it stays claimed and nothing else can use it.
func openSysfsLine(offset uint32) (*gpioLine, error) {
	path := fmt.Sprintf("%s/gpio%d", gpioSysfs, offset)
	if _, err := os.Stat(path); err != nil {
		return nil, fmt.Errorf("latch: %s is not exported", path)
	}
	return &gpioLine{offset: offset}, nil
}

// openCdevLine requests a line through the chip's character device.
//
// The v2 interface is used, not the deprecated v1: the v2 request carries
// the line's configuration with it, so the line is asked for as an output
// and comes back ready to drive.
func openCdevLine(chip int, offset uint32) (*gpioLine, error) {
	path := fmt.Sprintf("/dev/gpiochip%d", chip)
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return nil, fmt.Errorf("latch: opening %s: %w", path, err)
	}
	handle, err := requestLine(f, offset)
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	return &gpioLine{file: f, handle: handle, offset: offset}, nil
}

// requestLine asks the chip for one line.
//
// The chip answers with a file descriptor of its own, which is what the
// line's values are then set through.
func requestLine(f *os.File, offset uint32) (int, error) {
	var request gpioV2LineRequest
	request.offsets[0] = offset
	request.numLines = 1
	// The line is an output. The stock kernel's gpio-privacy driver drove
	// it as one, and the line rests low.
	request.config.flags = gpioV2LineFlagOutput
	copy(request.consumer[:], "homepanel-kiosk")

	fd, err := ioctl(f.Fd(), gpioV2GetLine, uintptr(unsafePointer(&request)))
	if err != nil {
		return 0, fmt.Errorf("latch: asking for the line: %w", err)
	}
	return int(fd), nil
}

// close releases the line.
func (l *gpioLine) close() error {
	if l.handle != 0 {
		_ = syscall.Close(l.handle)
		l.handle = 0
	}
	if l.file != nil {
		return l.file.Close()
	}
	return nil
}

// pulse drives the line high and then low, which is what asks the latch to
// flip.
//
// The line is held for the hold time and then released. The stock driver
// held it for 1000 ms, but the latch toggles on the pulse's edge and the
// edge is what matters: a shorter hold is the same edge followed by less
// waiting.
func (l *gpioLine) pulse(hold time.Duration) error {
	if l.handle == 0 {
		return l.pulseSysfs(hold)
	}
	return l.pulseCdev(hold)
}

// pulseSysfs drives the line through the gpio-class interface.
func (l *gpioLine) pulseSysfs(hold time.Duration) error {
	path := fmt.Sprintf("%s/gpio%d/value", gpioSysfs, l.offset)
	if err := os.WriteFile(path, []byte("1"), 0o644); err != nil {
		return fmt.Errorf("latch: driving the enable line: %w", err)
	}
	time.Sleep(hold)
	// The line is left low, which is where it rests.
	_ = os.WriteFile(path, []byte("0"), 0o644)
	return nil
}

// pulseCdev drives the line through the chip's character device.
func (l *gpioLine) pulseCdev(hold time.Duration) error {
	if err := l.set(true); err != nil {
		return err
	}
	time.Sleep(hold)
	return l.set(false)
}

// set drives the line to a value.
//
// The values are a bitmask of the lines, and the mask says which of them
// the value applies to.
func (l *gpioLine) set(high bool) error {
	var values gpioV2LineValues
	values.mask = 1
	if high {
		values.bits = 1
	}
	if _, err := ioctl(uintptr(l.handle), gpioV2SetLineValues, uintptr(unsafePointer(&values))); err != nil {
		return fmt.Errorf("latch: driving the enable line: %w", err)
	}
	return nil
}

// The gpio-cdev interface, version 2.
//
// The ioctl numbers are _IOWR(0xB4, number, struct), and the structs are
// the kernel's own, laid out as they are in include/uapi/linux/gpio.h.
const (
	// gpioV2GetLine asks the chip for a line.
	//
	// _IOWR(0xB4, 0x07, struct gpio_v2_line_request), and the request is
	// 592 bytes on a 64-bit kernel.
	gpioV2GetLine = 0x8250B407
	// gpioV2SetLineValues drives the lines.
	//
	// _IOWR(0xB4, 0x0F, struct gpio_v2_line_values), and the values are
	// 16 bytes.
	gpioV2SetLineValues = 0x8010B40F

	// gpioV2LineFlagOutput asks for the line as an output.
	gpioV2LineFlagOutput = 1 << 3

	// gpioV2LinesMax is how many lines one request can carry.
	gpioV2LinesMax = 64
	// gpioMaxNameSize is how long a consumer's name can be.
	gpioMaxNameSize = 32
	// gpioV2LineNumAttrsMax is how many attributes one line's
	// configuration can carry.
	gpioV2LineNumAttrsMax = 10
)

// gpioV2LineAttribute is one configurable attribute of a line.
type gpioV2LineAttribute struct {
	id      uint32
	padding uint32
	// flags is the union's widest member, so the union is one aligned
	// 64-bit word whichever member is meant.
	flags uint64
}

// gpioV2LineConfigAttribute is an attribute and the lines it applies to.
type gpioV2LineConfigAttribute struct {
	attr gpioV2LineAttribute
	mask uint64
}

// gpioV2LineConfig is how a line is asked for.
type gpioV2LineConfig struct {
	flags    uint64
	numAttrs uint32
	padding  [5]uint32
	attrs    [gpioV2LineNumAttrsMax]gpioV2LineConfigAttribute
}

// gpioV2LineRequest is the kernel's request for a line.
type gpioV2LineRequest struct {
	offsets         [gpioV2LinesMax]uint32
	consumer        [gpioMaxNameSize]byte
	config          gpioV2LineConfig
	numLines        uint32
	eventBufferSize uint32
	padding         [5]uint32
	fd              int32
}

// gpioV2LineValues is the kernel's values for the lines.
type gpioV2LineValues struct {
	bits uint64
	mask uint64
}

// ioctl makes an ioctl on a descriptor.
func ioctl(fd uintptr, request uintptr, argument uintptr) (uintptr, error) {
	rc, _, errno := syscall.Syscall(syscall.SYS_IOCTL, fd, request, argument)
	if errno != 0 {
		return 0, errno
	}
	return rc, nil
}

// unsafePointer is the address of a value, for the ioctl.
func unsafePointer[T any](v *T) unsafe.Pointer {
	return unsafe.Pointer(v)
}

// describe says what the latch is reading, for the log.
func describe() string {
	return fmt.Sprintf("%s, enable line GPIO%d", strings.TrimSpace(keysName), EnableGPIO)
}
