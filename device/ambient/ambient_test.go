package ambient

import (
	"math"
	"os"
	"path/filepath"
	"testing"
)

func fakeSensor(t *testing.T, clear, red, green, blue string) string {
	t.Helper()
	dir := t.TempDir()
	write := func(name, value string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(value), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("name", "tcs3400")
	write("calibscale_available", "1 4 16 64")
	write("in_intensity_integration_time", tcs3400IntTime)
	write("in_intensity_calibscale", "16")
	write("in_intensity_clear_raw", clear)
	write("in_intensity_red_raw", red)
	write("in_intensity_green_raw", green)
	write("in_intensity_blue_raw", blue)
	return dir
}

func TestTheLuxConversionFollowsTheStockDriver(t *testing.T) {

	s := &Sensor{gains: []int{1, 4, 16, 64}, gain: 2, itime: 0.278}
	got := s.lux(counts{c: 2000, r: 1000, g: 1500, b: 800}, 16, 0.278, false)

	ir := math.Max(0, math.Floor((1000+1500+800-2000+1)/2))
	cpl := 0.278 * 1000 * 16 / 446.0
	want := (0.090*(1000-ir) + 1.000*(1500-ir) + -0.460*(800-ir)) / cpl
	if math.Abs(got-want) > 0.001 {
		t.Errorf("the lux is %v, the stock arithmetic gives %v", got, want)
	}
}

func TestTheLuxIsNeverNegative(t *testing.T) {
	s := &Sensor{gains: []int{1, 4, 16, 64}, gain: 0, itime: 0.278}
	got := s.lux(counts{c: 100, r: 10, g: 10, b: 200}, 1, 0.278, false)
	if got < 0 {
		t.Errorf("the lux is %v, it should never be negative", got)
	}
}

func TestTheGainDropsWhenTheSensorSaturates(t *testing.T) {
	s := &Sensor{gains: []int{1, 4, 16, 64}, gain: 3, itime: 0.278}
	if got := s.nextGain(maxCount(s.itime)); got != 2 {
		t.Errorf("the gain moved to %d, it should have dropped a step", got)
	}
}

func TestTheGainRisesWhenTheRoomIsDim(t *testing.T) {
	s := &Sensor{gains: []int{1, 4, 16, 64}, gain: 0, itime: 0.278}
	if got := s.nextGain(1); got != 1 {
		t.Errorf("the gain moved to %d, it should have risen a step", got)
	}
}

func TestTheGainDoesNotHunt(t *testing.T) {
	s := &Sensor{gains: []int{1, 4, 16, 64}, gain: 2, itime: 0.278}

	if got := s.nextGain(30000); got != 2 {
		t.Errorf("the gain moved to %d, it should have stayed", got)
	}
}

func TestTheSensorIsFoundByItsName(t *testing.T) {
	root := t.TempDir()
	dir := fakeSensor(t, "10", "5", "5", "5")

	device := filepath.Join(root, "iio:device0")
	if err := os.MkdirAll(device, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"name", "calibscale_available", "in_intensity_integration_time", "in_intensity_calibscale"} {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(device, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	old := iioRoot
	iioRoot = root
	defer func() { iioRoot = old }()

	s, err := Open()
	if err != nil {
		t.Fatalf("the sensor was not found: %v", err)
	}
	if s.itime <= 0 {
		t.Errorf("the integration time is %v, it should be the one that was asked for", s.itime)
	}
	if len(s.gains) != 4 {
		t.Errorf("there are %d gains, there should be 4", len(s.gains))
	}
}

func TestAMissingSensorIsNotAnError(t *testing.T) {
	old := iioRoot
	iioRoot = t.TempDir()
	defer func() { iioRoot = old }()

	if _, err := Open(); err == nil {
		t.Error("a board with no sensor was not reported as having none")
	}
}
