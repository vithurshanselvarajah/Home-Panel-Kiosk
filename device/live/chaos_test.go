package live

import (
	"bufio"
	"encoding/json"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

type chaosStep struct {
	Action string

	Source string

	PositionMs int64
}

type chaosPlan []chaosStep

var chaosPlans = map[string]chaosPlan{
	"the double tap": {
		{Action: "pause"},
		{Action: "pause"},
		{Action: "play"},
		{Action: "play"},
	},
	"the press before the music is ready": {
		{Action: "play"},
		{Action: "pause"},
		{Action: "play"},
	},
	"the seek during the pause": {
		{Action: "pause"},
		{Action: "seek", PositionMs: 30000},
		{Action: "play"},
	},
	"the seek during the play": {
		{Action: "seek", PositionMs: 10000},
		{Action: "seek", PositionMs: 45000},
		{Action: "play"},
	},
	"the seek backwards": {
		{Action: "seek", PositionMs: 60000},
		{Action: "seek", PositionMs: 5000},
		{Action: "pause"},
		{Action: "play"},
	},
	"the stop and the play": {
		{Action: "stop"},
		{Action: "play"},
		{Action: "stop"},
		{Action: "play"},
	},
	"the skip and the back": {
		{Action: "next"},
		{Action: "previous"},
		{Action: "next"},
		{Action: "play"},
	},
	"the pause after the stop": {
		{Action: "stop"},
		{Action: "pause"},
		{Action: "play"},
	},
}

func (d *device) pressTransport(t *testing.T, step chaosStep) {
	t.Helper()
	socket := d.controlSocket()
	if socket == "" {
		t.Fatal("the device program has no control socket")
	}
	conn, err := net.DialTimeout("unix", socket, 5*time.Second)
	if err != nil {
		t.Fatalf("the control socket could not be reached: %v", err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))

	command := map[string]any{"action": step.Action}
	if step.Source != "" {
		command["source"] = step.Source
	}
	if step.PositionMs > 0 {
		command["positionMs"] = step.PositionMs
	}
	payload, err := json.Marshal(map[string]any{
		"action":  "transport",
		"command": command,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Write(append(payload, '\n')); err != nil {
		t.Fatalf("the transport command could not be sent: %v", err)
	}
}

type mediaView struct {
	MediaState   string
	MusicState   string
	MusicPlaying bool
	MusicTitle   string
}

func (d *device) view(t *testing.T) mediaView {
	t.Helper()
	state, found := d.lastState()
	if !found {
		t.Fatal("the panel has said nothing yet")
	}
	return mediaView{
		MediaState:   state.MediaState,
		MusicState:   state.MusicState,
		MusicPlaying: state.MusicPlaying,
		MusicTitle:   state.MusicTitle,
	}
}

func (v mediaView) musicIsSettled() bool {
	switch v.MusicState {
	case "playing", "paused", "idle", "buffering", "stopped", "connecting":
		return true
	default:
		return false
	}
}

func TestThePanelStaysSaneWhenTheTransportIsPressed(t *testing.T) {
	skipUnlessLive(t, "Home Assistant", haURL(), haToken())

	d := startDevice(t, "")
	d.waitFor("connected to Home Assistant", 90*time.Second, func(s deviceState) bool {
		return s.Connection == "connected"
	})

	peer, peerOutput := startSendspinPeer(t, "chaos", d)
	defer func() {
		if peer != nil && peer.Process != nil {
			_ = peer.Process.Kill()
		}
	}()
	go logLines(t, "the server", peerOutput)

	d.waitFor("the server's music is playing", 60*time.Second, func(s deviceState) bool {
		return s.MusicState == "playing" && s.MusicTitle != ""
	})
	t.Logf("the panel says the music is %q", d.view(t).MusicTitle)

	names := make([]string, 0, len(chaosPlans))
	for name := range chaosPlans {
		names = append(names, name)
	}

	sortStrings(names)

	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			runChaosPlan(t, d, chaosPlans[name])
		})
	}
}

func runChaosPlan(t *testing.T, d *device, plan chaosPlan) {
	t.Helper()

	for _, step := range plan {
		d.pressTransport(t, step)

		time.Sleep(150 * time.Millisecond)
	}

	time.Sleep(3 * time.Second)

	view := d.view(t)
	t.Logf("the panel says the server's music is %q, playing=%v, the panel's own music is %q",
		view.MusicState, view.MusicPlaying, view.MediaState)

	if !view.musicIsSettled() {
		t.Errorf("the panel is in a state no player is in: %q", view.MusicState)
	}

	_, connected := d.waitForConnected(20 * time.Second)
	if !connected {
		t.Errorf("the panel dropped its connection")
		for _, line := range d.logs() {
			t.Logf("the device said: %s", line)
		}
	}

	for _, line := range d.logs() {
		if strings.Contains(line, "panic:") || strings.Contains(line, "fatal error:") {
			t.Errorf("the device program fell over: %s", line)
		}
	}

	d.pressTransport(t, chaosStep{Action: "play"})
	d.waitFor("the music playing again", 30*time.Second, func(s deviceState) bool {
		return s.MusicState == "playing"
	})
}

func logLines(t *testing.T, who string, reader io.Reader) {
	t.Helper()
	scanner := bufio.NewScanner(reader)
	for scanner.Scan() {
		if line := strings.TrimSpace(scanner.Text()); line != "" {
			t.Logf("%s: %s", who, line)
		}
	}
}

func sortStrings(names []string) {
	for i := 1; i < len(names); i++ {
		for j := i; j > 0 && names[j] < names[j-1]; j-- {
			names[j], names[j-1] = names[j-1], names[j]
		}
	}
}

func TestTheTransportCommandsAreAllUnderstood(t *testing.T) {
	skipUnlessLive(t, "Home Assistant", haURL(), haToken())

	d := startDevice(t, "")
	d.waitFor("connected to Home Assistant", 90*time.Second, func(s deviceState) bool {
		return s.Connection == "connected"
	})

	peer, peerOutput := startSendspinPeer(t, "chaos", d)
	defer func() {
		if peer != nil && peer.Process != nil {
			_ = peer.Process.Kill()
		}
	}()
	go logLines(t, "the server", peerOutput)

	d.waitFor("the server's music is playing", 60*time.Second, func(s deviceState) bool {
		return s.MusicState == "playing"
	})

	before := len(d.logs())
	for _, step := range []chaosStep{
		{Action: "play"},
		{Action: "pause"},
		{Action: "next"},
		{Action: "previous"},
		{Action: "seek", PositionMs: 1000},
		{Action: "stop"},
	} {
		d.pressTransport(t, step)
		time.Sleep(400 * time.Millisecond)
	}

	logs := d.logs()[before:]
	for _, line := range logs {
		if strings.Contains(line, "unknown transport") || strings.Contains(line, "was not understood") {
			t.Errorf("the device program did not understand a command: %s", line)
		}
	}
	t.Logf("the device program said %d things about the transport", len(logs))
}

func (d *device) waitForConnected(within time.Duration) (deviceState, bool) {
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		state, found := d.lastState()
		if found && state.Connection == "connected" {
			return state, true
		}
		time.Sleep(200 * time.Millisecond)
	}
	state, _ := d.lastState()
	return state, false
}
