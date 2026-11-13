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

// The live voice turn.
//
// This is the whole of the voice path, end to end: a recording is
// played into the panel's microphone, the panel sends it to Home
// Assistant, Home Assistant's speech recognition hears it, Home
// Assistant's agent answers, the answer comes back as audio, and the
// panel plays it.
//
// Nothing is stubbed. The device program is the real one, the servers
// are the real ones, and the recording is a real recording.

// TestTheVoiceTurnAgainstLiveHomeAssistant speaks a recording to the
// panel and checks what Home Assistant answers.
//
// The answer is checked against what Home Assistant says to the same
// question asked as text, so the check is against Home Assistant's own
// answer and not against something written down here.
func TestTheVoiceTurnAgainstLiveHomeAssistant(t *testing.T) {
	skipUnlessLive(t, "Home Assistant", haURL(), haToken())

	recording := liveConvertedRecording(t, liveRecording(t))
	question := liveQuestion(t)

	// What Home Assistant answers to the same question, asked as text.
	answered := post(t, haURL()+"/api/conversation/process", haToken(), map[string]any{
		"text":     question,
		"language": "en",
	})
	expected := speechOf(answered)
	if expected == "" {
		t.Fatalf("Home Assistant did not answer %q: %v", question, answered)
	}
	t.Logf("asked as text, Home Assistant answers %q", expected)

	// The panel, with the recording as its microphone.
	d := startDevice(t, recording)

	// The link has to come up before the turn, because the turn goes
	// over it.
	d.waitFor("connected to Home Assistant", 90*time.Second, func(s deviceState) bool {
		return s.Connection == "connected"
	})

	// The turn starts. The panel is asked, which is what an automation
	// or a button on the screen would do.
	d.askToListen()

	// What Home Assistant heard.
	state := d.waitFor("heard the recording", 60*time.Second, func(s deviceState) bool {
		return s.Transcript != ""
	})
	t.Logf("Home Assistant heard %q", state.Transcript)

	// What Home Assistant said.
	state = d.waitFor("gave an answer", 60*time.Second, func(s deviceState) bool {
		return s.Spoken != ""
	})
	t.Logf("Home Assistant said %q", state.Spoken)

	// The answer is the answer it gave as text.
	//
	// The time is the exception: asked as text it says the time when it
	// was asked, and asked by voice it says the time when it was
	// spoken. So the answer is compared with the same words, not with
	// the same time.
	if question == "what is the time" {
		if !sameWords(state.Spoken, expected) {
			t.Errorf("the answer was %q, asked as text it was %q", state.Spoken, expected)
		}
	} else if state.Spoken != expected {
		t.Errorf("the answer was %q, asked as text it was %q", state.Spoken, expected)
	}

	// The reply is played. The speaker's audio is in the file.
	played := d.waitFor("played the reply", 60*time.Second, func(s deviceState) bool {
		return s.Speaking
	})
	t.Logf("the panel is speaking: %s", played.Phase)
}

// liveRecording is the recording spoken to the panel.
//
// It is a wav file at sixteen kilohertz, mono, sixteen bit, which is
// what the microphone produces.
//
// The recording is converted here rather than being asked for ready
// made, so a test does not depend on a file somebody made by hand and
// then deleted.
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

// liveConvertedRecording converts a recording to what the microphone
// produces.
//
// The microphone is sixteen kilohertz, mono, sixteen bit, and a
// recording in any other shape has to be converted before it is
// played into it.
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

// liveQuestion is what the recording is asking.
//
// It is taken from the recording's own name when it is not given
// outright, because a recording that says one thing and a question that
// asks another make the test about something nobody meant to test.
//
// The recording is named after what it says, so the name is the
// question.
func liveQuestion(t *testing.T) string {
	t.Helper()
	if question := os.Getenv("LIVE_QUESTION"); question != "" {
		return question
	}
	path := liveRecording(t)
	name := filepath.Base(path)
	// The name is "test-recording - What is the time.mp3".
	if dash := strings.Index(name, " - "); dash >= 0 {
		name = name[dash+3:]
	}
	name = strings.TrimSuffix(name, filepath.Ext(name))
	if name == "" {
		return "what is the time"
	}
	return strings.ToLower(name)
}

// speechOf reads the answer out of a conversation reply.
func speechOf(reply map[string]any) string {
	response, _ := reply["response"].(map[string]any)
	speech, _ := response["speech"].(map[string]any)
	plain, _ := speech["plain"].(map[string]any)
	text, _ := plain["speech"].(string)
	return text
}

// sameWords says whether two answers are the same words.
//
// The time is the same words whatever time it is.
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

// hasDigit says whether a word has a digit in it.
func hasDigit(word string) bool {
	for _, r := range word {
		if r >= '0' && r <= '9' {
			return true
		}
	}
	return false
}

// splitFields splits a sentence into words.
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

// joinWords puts words back together.
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

// askToListen asks the device program to start a turn.
//
// It is the same thing the wake word does, and it goes over the control
// socket that the panel uses.
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
