// Package ambient reads the board's ambient light sensor.
//
// The Echo Show 8 (2nd gen) has an AMS TCS3400 on the I²C bus. The
// kernel's tcs3472 driver is taught about it (the TCS3400 has no
// command-type field and its own gains), and it hands out raw clear,
// red, green and blue counts rather than a lux.
//
// So the lux is worked out here, the way Amazon's own tcs3400 driver
// does it:
//
//	IR    = (R + G + B - C + 1) / 2, not below zero
//	CPL   = integration time in ms * gain / DGF
//	lux   = (Rc*(R-IR) + Gc*(G-IR) + Bc*(B-IR)) / CPL
//
// The gain is chosen so the counts are in range: the clear channel
// saturates near the top of the scale, and the sensor is moved up or
// down a step to keep it off both ends.
package ambient

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// iioRoot is where the IIO devices are listed.
//
// It is a variable so a test can point it at a directory of its own.
var iioRoot = "/sys/bus/iio/devices"

// tcs3400Name is the name the driver gives a TCS3400.
const tcs3400Name = "tcs3400"

// tcs3400IntTime is the integration time asked for: 100 cycles of
// 2.78 ms.
//
// It fits inside the half-second poll, and at the highest gain one
// count is about 0.03 lux, while at the lowest the clear channel
// saturates near 80,000 lux.
const tcs3400IntTime = "0.278000"

// atimeStep is the time one integration cycle takes.
//
// The driver's own table has the steps in microseconds, and the cycle is
// 2.78 ms of it.
const atimeStep = 0.00278

// Lux coefficients: the stock tcs3400 driver's built-in defaults.
//
// The device's glass factor is 446, the red coefficient 0.090, the
// green 1.000 and the blue -0.460. This follows AMS's design note DN40
// for the TCS3400 family.
//
// The device's own device tree gives none of these, so its real numbers
// may come from a per-unit calibration Amazon's userspace writes. These
// are not calibrated for the window.
const (
	luxDGF   = 446.0
	luxRCoef = 0.090
	luxGCoef = 1.000
	luxBCoef = -0.460
)

// Sensor is the board's ambient light sensor.
//
// It is read on demand: the reading is a poll, and the poll is the
// thing that decides how bright the screen is.
type Sensor struct {
	// dir is the IIO device's directory.
	dir string
	// gains are the gain steps the driver offers, low to high.
	gains []int
	// gain is the index into gains of the step in use.
	gain int
	// itime is the integration time in seconds.
	itime float64
	// settle is how many readings to drop after the gain moves.
	//
	// The cycle that was running when the gain changed finishes on the
	// old one, and the next poll may still see it.
	settle int
}

// Open finds the light sensor and puts it in a state that can be read.
//
// It is not an error for the board to have no light sensor: a panel
// without one is a panel whose screen is set by hand, so the caller is
// told there is none rather than being given an error to carry.
func Open() (*Sensor, error) {
	dir, err := findIIO(tcs3400Name)
	if err != nil {
		return nil, err
	}
	s := &Sensor{dir: dir}
	if err := s.write("in_intensity_integration_time", tcs3400IntTime); err != nil {
		return nil, err
	}
	s.itime, err = s.float("in_intensity_integration_time")
	if err != nil {
		return nil, fmt.Errorf("ambient: reading the integration time back: %w", err)
	}
	if s.itime <= 0 {
		return nil, fmt.Errorf("ambient: the integration time is %v", s.itime)
	}
	gains, err := s.availableGains()
	if err != nil {
		return nil, err
	}
	s.gains = gains
	// Start in the middle: the first readings move it where the room
	// needs it.
	if err := s.setGain(len(s.gains) / 2); err != nil {
		return nil, err
	}
	return s, nil
}

// findIIO is the directory of the IIO device with this name.
func findIIO(name string) (string, error) {
	names, _ := filepath.Glob(filepath.Join(iioRoot, "*", "name"))
	for _, n := range names {
		b, err := os.ReadFile(n)
		if err == nil && strings.TrimSpace(string(b)) == name {
			return filepath.Dir(n), nil
		}
	}
	return "", fmt.Errorf("ambient: there is no IIO device named %s under %s", name, iioRoot)
}

// availableGains are the gain steps the driver offers.
func (s *Sensor) availableGains() ([]int, error) {
	b, err := os.ReadFile(filepath.Join(s.dir, "calibscale_available"))
	if err != nil {
		return nil, fmt.Errorf("ambient: %w", err)
	}
	var gains []int
	for _, f := range strings.Fields(string(b)) {
		if g, err := strconv.Atoi(f); err == nil && g > 0 {
			gains = append(gains, g)
		}
	}
	if len(gains) == 0 {
		return nil, fmt.Errorf("ambient: there are no gains in %q", strings.TrimSpace(string(b)))
	}
	return gains, nil
}

// write sets one of the driver's attributes.
func (s *Sensor) write(attr, v string) error {
	if err := os.WriteFile(filepath.Join(s.dir, attr), []byte(v), 0o644); err != nil {
		return fmt.Errorf("ambient: setting %s: %w", attr, err)
	}
	return nil
}

// float reads one of the driver's attributes as a number.
func (s *Sensor) float(attr string) (float64, error) {
	b, err := os.ReadFile(filepath.Join(s.dir, attr))
	if err != nil {
		return 0, err
	}
	return strconv.ParseFloat(strings.TrimSpace(string(b)), 64)
}

// setGain moves to a gain step.
func (s *Sensor) setGain(i int) error {
	if i < 0 || i >= len(s.gains) {
		return fmt.Errorf("ambient: there is no gain step %d", i)
	}
	if err := s.write("in_intensity_calibscale", strconv.Itoa(s.gains[i])); err != nil {
		return err
	}
	s.gain = i
	// The cycle running when the gain changed finishes on the old one,
	// and the next poll may still see it. One poll later the data is
	// surely from the new gain.
	s.settle = 1
	return nil
}

// counts is one set of raw readings.
type counts struct{ c, r, g, b float64 }

// read reads the counts.
func (s *Sensor) read() (counts, error) {
	var v [4]float64
	for i, ch := range [4]string{"clear", "red", "green", "blue"} {
		n, err := s.float("in_intensity_" + ch + "_raw")
		if err != nil {
			return counts{}, err
		}
		v[i] = n
	}
	return counts{v[0], v[1], v[2], v[3]}, nil
}

// Lux reads the sensor and returns the light in lux.
//
// The reading is not one to report when the gain has just moved: the
// counts are from the old gain, and a lux worked out of them would be
// wrong. The caller is told so, and reads again.
func (s *Sensor) Lux() (float64, bool, error) {
	n, err := s.read()
	if err != nil {
		return 0, false, err
	}
	if s.settle > 0 {
		s.settle--
		return 0, false, nil
	}
	if next := s.nextGain(n.c); next != s.gain {
		if err := s.setGain(next); err != nil {
			return 0, false, err
		}
		return 0, false, nil
	}
	top := s.gain == len(s.gains)-1
	return s.lux(n, float64(s.gains[s.gain]), s.itime, top), true, nil
}

// maxCount is the most a channel can hold at this integration time.
//
// 1024 counts per 2.78 ms cycle, up to the 16-bit register. The
// datasheet's figures, as the stock driver uses them: the limit is
// cycles << 10, capped at 0xffff.
func maxCount(itime float64) float64 {
	cycles := math.Round(itime / atimeStep)
	return math.Min(cycles*1024, 65535)
}

// nextGain is the gain step for the next reading.
//
// Above 80% of full scale — the stock driver's saturation line — the
// gain drops a step. It rises a step only when the clear count would
// still be under half that line at the higher gain, so it does not hunt
// between two steps.
func (s *Sensor) nextGain(clear float64) int {
	high := 0.8 * maxCount(s.itime)
	switch {
	case clear >= high && s.gain > 0:
		return s.gain - 1
	case s.gain < len(s.gains)-1 && clear*float64(s.gains[s.gain+1])/float64(s.gains[s.gain]) < high/2:
		return s.gain + 1
	}
	return s.gain
}

// lux converts raw counts to lux as the stock driver does.
//
// A negative result — blue-heavy light at the low end — reads as zero.
func (s *Sensor) lux(n counts, gain, itime float64, topGain bool) float64 {
	ir := 0.0
	if !topGain {
		ir = math.Max(0, math.Floor((n.r+n.g+n.b-n.c+1)/2))
	}
	cpl := itime * 1000 * gain / luxDGF
	if cpl <= 0 {
		return 0
	}
	lux := (luxRCoef*(n.r-ir) + luxGCoef*(n.g-ir) + luxBCoef*(n.b-ir)) / cpl
	return math.Max(0, lux)
}
