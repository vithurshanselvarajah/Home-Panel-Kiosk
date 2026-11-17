package scorer

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// testdataDir is where the model's own reference audio is.
func testdataDir() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..", "..", "..", "audio", "third_party", "microwakeword", "testdata")
}

// loadWAV reads a wav file's samples: the header is 44 bytes and the rest is
// signed 16-bit little-endian mono.
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

// The engine must hear the wake word in the model's own reference audio.
//
// This is the model the engine was trained against, and the audio that goes
// with it, so a score that does not reach the cutoff here means the engine
// is not running the model it was given.
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
