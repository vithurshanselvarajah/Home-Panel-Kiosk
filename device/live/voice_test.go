package live

import (
	"encoding/json"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestTheVoiceTurnAgainstLiveHomeAssistant(t *testing.T) {
	skipUnlessLive(t, "Home Assistant", haURL(), haToken())

	recording := liveConvertedRecording(t, liveRecording(t))
	question := liveQuestion(t)

	answered := post(t, haURL()+"/api/conversation/process", haToken(), map[string]any{
		"text":     question,
		"language": "en",
	})
	expected := speechOf(answered)
	if expected == "" {
		t.Fatalf("Home Assistant did not answer %q: %v", question, answered)
	}
	t.Logf("asked as text, Home Assistant answers %q", expected)

	d := startDevice(t, recording)

	d.waitFor("connected to Home Assistant", 90*time.Second, func(s deviceState) bool {
		return s.Connection == "connected"
	})

	d.askToListen()

	state := d.waitFor("heard the recording", 60*time.Second, func(s deviceState) bool {
		return s.Transcript != ""
	})
	t.Logf("Home Assistant heard %q", state.Transcript)

	state = d.waitFor("gave an answer", 60*time.Second, func(s deviceState) bool {
		return s.Spoken != ""
	})
	t.Logf("Home Assistant said %q", state.Spoken)

	if question == "what is the time" {
		if !sameWords(state.Spoken, expected) {
			t.Errorf("the answer was %q, asked as text it was %q", state.Spoken, expected)
		}
	} else if state.Spoken != expected {
		t.Errorf("the answer was %q, asked as text it was %q", state.Spoken, expected)
	}

	played := d.waitFor("played the reply", 60*time.Second, func(s deviceState) bool {
		return s.Speaking
	})
	t.Logf("the panel is speaking: %s", played.Phase)
}

func liveRecording(t *testing.T) string {
	t.Helper()
	path := os.Getenv("LIVE_RECORDING")
	if path == "" {
		t.Skip("no recording was named")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("the recording is not there: %v", err)
	}
	return path
}

func liveConvertedRecording(t *testing.T, source string) string {
	t.Helper()
	if strings.HasSuffix(source, ".wav") {
		return source
	}
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skipf("ffmpeg is not there, so the recording cannot be converted: %v", err)
	}
	out := filepath.Join(t.TempDir(), "recording.wav")
	cmd := exec.Command(ffmpeg, "-y", "-loglevel", "error",
		"-i", source, "-ar", "16000", "-ac", "1", "-acodec", "pcm_s16le", out)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("the recording could not be converted: %v (%s)", err, strings.TrimSpace(string(output)))
	}
	return out
}

func liveQuestion(t *testing.T) string {
	t.Helper()
	if question := os.Getenv("LIVE_QUESTION"); question != "" {
		return question
	}
	path := liveRecording(t)
	name := filepath.Base(path)

	if dash := strings.Index(name, " - "); dash >= 0 {
		name = name[dash+3:]
	}
	name = strings.TrimSuffix(name, filepath.Ext(name))
	if name == "" {
		return "what is the time"
	}
	return strings.ToLower(name)
}

func speechOf(reply map[string]any) string {
	response, _ := reply["response"].(map[string]any)
	speech, _ := response["speech"].(map[string]any)
	plain, _ := speech["plain"].(map[string]any)
	text, _ := plain["speech"].(string)
	return text
}

func sameWords(a, b string) bool {
	strip := func(s string) string {
		var out []string
		for _, f := range splitFields(s) {
			if hasDigit(f) {
				f = "N"
			}
			out = append(out, f)
		}
		return joinWords(out)
	}
	return strip(a) == strip(b)
}

func hasDigit(word string) bool {
	for _, r := range word {
		if r >= '0' && r <= '9' {
			return true
		}
	}
	return false
}

func splitFields(s string) []string {
	var out []string
	word := ""
	for _, r := range s {
		if r == ' ' || r == '\t' || r == '\n' {
			if word != "" {
				out = append(out, word)
				word = ""
			}
			continue
		}
		word += string(r)
	}
	if word != "" {
		out = append(out, word)
	}
	return out
}

func joinWords(words []string) string {
	out := ""
	for i, word := range words {
		if i > 0 {
			out += " "
		}
		out += word
	}
	return out
}

func (d *device) askToListen() {
	d.t.Helper()
	socket := d.controlSocket()
	if socket == "" {
		d.t.Fatal("the device program has no control socket")
	}
	conn, err := net.Dial("unix", socket)
	if err != nil {
		d.t.Fatalf("the control socket could not be opened: %v", err)
	}
	defer conn.Close()

	payload, err := json.Marshal(map[string]any{
		"action": "listen",
		"phrase": "hey jarvis",
	})
	if err != nil {
		d.t.Fatal(err)
	}
	if _, err := conn.Write(append(payload, '\n')); err != nil {
		d.t.Fatalf("the request could not be sent: %v", err)
	}
}
