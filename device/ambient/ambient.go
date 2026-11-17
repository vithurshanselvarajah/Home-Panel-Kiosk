package ambient

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

var iioRoot = "/sys/bus/iio/devices"

const tcs3400Name = "tcs3400"

const tcs3400IntTime = "0.278000"

const atimeStep = 0.00278

const (
	luxDGF   = 446.0
	luxRCoef = 0.090
	luxGCoef = 1.000
	luxBCoef = -0.460
)

type Sensor struct {
	dir string

	gains []int

	gain int

	itime float64

	settle int
}

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

	if err := s.setGain(len(s.gains) / 2); err != nil {
		return nil, err
	}
	return s, nil
}

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

func (s *Sensor) write(attr, v string) error {
	if err := os.WriteFile(filepath.Join(s.dir, attr), []byte(v), 0o644); err != nil {
		return fmt.Errorf("ambient: setting %s: %w", attr, err)
	}
	return nil
}

func (s *Sensor) float(attr string) (float64, error) {
	b, err := os.ReadFile(filepath.Join(s.dir, attr))
	if err != nil {
		return 0, err
	}
	return strconv.ParseFloat(strings.TrimSpace(string(b)), 64)
}

func (s *Sensor) setGain(i int) error {
	if i < 0 || i >= len(s.gains) {
		return fmt.Errorf("ambient: there is no gain step %d", i)
	}
	if err := s.write("in_intensity_calibscale", strconv.Itoa(s.gains[i])); err != nil {
		return err
	}
	s.gain = i

	s.settle = 1
	return nil
}

type counts struct{ c, r, g, b float64 }

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

func maxCount(itime float64) float64 {
	cycles := math.Round(itime / atimeStep)
	return math.Min(cycles*1024, 65535)
}

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
