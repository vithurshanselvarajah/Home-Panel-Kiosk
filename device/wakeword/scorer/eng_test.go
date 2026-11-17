package scorer

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func testdataDir() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..", "..", "..", "audio", "third_party", "microwakeword", "testdata")
}

func loadWAV(path string) ([]int16, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	pcm := data[44:]
	n := len(pcm) / 2
	samples := make([]int16, n)
	for i := 0; i < n; i++ {
		samples[i] = int16(binary.LittleEndian.Uint16(pcm[i*2:]))
	}
	return samples, nil
}

func TestTheEngineHearsTheWakeWord(t *testing.T) {
	eng, err := newMicroEngine(filepath.Join(testdataDir(), "hey_jarvis.tflite"))
	if err != nil {
		t.Fatalf("the engine could not load the model: %v", err)
	}
	samples, err := loadWAV(filepath.Join(testdataDir(), "hey_jarvis.wav"))
	if err != nil {
		t.Skipf("no reference audio: %v", err)
	}

	best := float32(0)
	for i := 0; i+1600 <= len(samples); i += 1600 {
		score, ready, err := eng.Push(samples[i : i+1600])
		if err != nil {
			t.Fatalf("the engine failed: %v", err)
		}
		if ready && score > best {
			best = score
		}
	}
	t.Logf("the best score was %v", best)
	if best < 0.5 {
		t.Fatalf("the engine scored %v on the model's own reference audio, want at least 0.5", best)
	}
}
