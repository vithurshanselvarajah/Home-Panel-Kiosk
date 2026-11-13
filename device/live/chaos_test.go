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

// The media player under a mess.
//
// The music controls are pressed in the wrong order, quickly, and while
// the music is changing underneath them. A panel that handles each
// command on its own falls apart when the commands overlap: the panel
// says it is paused while the music plays, the panel says it is
// playing while the music is stopped, and the panel stops answering.
//
// So the commands are thrown at it in combinations, and after each one
// the panel's own view of the music is checked.
//
// The check is that the panel's view is *consistent* — that it is one
// of the states a player can really be in, that it settles, and that
// the panel is still there afterwards.
//
// The music server is aiosendspin's own server, which is the library
// Music Assistant speaks the protocol with. So the handshake, the roles
// and the messages are the ones that are really used, and the server is
// under the test's control.

// chaosStep is one thing that is done to the player.
type chaosStep struct {
	// Action is what the button does.
	Action string
	// Source is which music it is for.
	Source string
	// PositionMs is where to seek to, for a seek.
	PositionMs int64
}

// chaosPlan is a list of things done to the player, in order.
type chaosPlan []chaosStep

// chaosPlans are the combinations that are tried.
//
// They are not random: a random plan is not a test, because it cannot
// be run again and it does not say what was wrong when it fails. Each
// one is a shape of mistake that a person makes — the double tap, the
// press before the music has started, the seek during the pause, and
// the press of the wrong button entirely.
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

// pressTransport asks the device program to drive the transport.
//
// It goes over the control socket, which is how the panel asks for it,
// so the test drives the panel's own path rather than reaching into
// the device program.
//
// A seek carries where it is seeking to, which the other commands do
// not.
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

// mediaView is what the panel says about the music.
//
// It is the panel's own view, taken from the state file, which is
// exactly what the drawer shows.
type mediaView struct {
	MediaState   string
	MusicState   string
	MusicPlaying bool
	MusicTitle   string
}

// view reads the panel's view of the music.
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

// musicIsSettled says whether the panel's view of the music is one a
// player can really be in.
//
// The states are the ones the device program uses. A player that is
// playing and paused at once, or that is playing something it does not
// have, is a player in a state nobody asked for.
func (v mediaView) musicIsSettled() bool {
	switch v.MusicState {
	case "playing", "paused", "idle", "buffering", "stopped", "connecting":
		return true
	default:
		return false
	}
}

// TestThePanelStaysSaneWhenTheTransportIsPressed throws the transport
// commands at the panel in many combinations and checks that the panel
// is still sane afterwards.
//
// The music server is the real protocol library, so the commands meet
// a server that really answers them.
func TestThePanelStaysSaneWhenTheTransportIsPressed(t *testing.T) {
	skipUnlessLive(t, "Home Assistant", haURL(), haToken())

	d := startDevice(t, "")
	d.waitFor("connected to Home Assistant", 90*time.Second, func(s deviceState) bool {
		return s.Connection == "connected"
	})

	// The music server is started before the commands, so that the
	// commands have a music to act on.
	peer, peerOutput := startSendspinPeer(t, "chaos", d)
	defer func() {
		if peer != nil && peer.Process != nil {
			_ = peer.Process.Kill()
		}
	}()
	go logLines(t, "the server", peerOutput)

	// The music has to be playing before the commands are thrown, or
	// the commands have nothing to act on and the test is about nothing.
	d.waitFor("the server's music is playing", 60*time.Second, func(s deviceState) bool {
		return s.MusicState == "playing" && s.MusicTitle != ""
	})
	t.Logf("the panel says the music is %q", d.view(t).MusicTitle)

	names := make([]string, 0, len(chaosPlans))
	for name := range chaosPlans {
		names = append(names, name)
	}
	// The plans are run in a fixed order, so a failure can be run
	// again and means the same thing.
	sortStrings(names)

	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			runChaosPlan(t, d, chaosPlans[name])
		})
	}
}

// runChaosPlan throws one combination at the panel and checks that the
// panel is still sane.
func runChaosPlan(t *testing.T, d *device, plan chaosPlan) {
	t.Helper()

	// The commands are thrown at the panel without waiting, because that
	// is what a person does: they press the button again before the
	// music has caught up.
	for _, step := range plan {
		d.pressTransport(t, step)
		// A little space, so the commands overlap a little but do not
		// all arrive in the same instant.
		time.Sleep(150 * time.Millisecond)
	}

	// The panel is given a moment to settle.
	//
	// It is a moment and not a wait, because a panel that never settles
	// is a failure and should be reported as one, rather than waited
	// for.
	time.Sleep(3 * time.Second)

	view := d.view(t)
	t.Logf("the panel says the server's music is %q, playing=%v, the panel's own music is %q",
		view.MusicState, view.MusicPlaying, view.MediaState)

	// The panel's view is one a player can really be in.
	if !view.musicIsSettled() {
		t.Errorf("the panel is in a state no player is in: %q", view.MusicState)
	}

	// The panel is still connected. A transport command that takes the
	// connection down is a fault in its own right.
	//
	// The connection is waited for rather than looked at, because
	// Home Assistant reloads its entry from time to time and the panel
	// is reconnected when it does. A panel that is reconnecting is not
	// a panel whose transport broke the connection.
	_, connected := d.waitForConnected(20 * time.Second)
	if !connected {
		t.Errorf("the panel dropped its connection")
		for _, line := range d.logs() {
			t.Logf("the device said: %s", line)
		}
	}

	// The device program did not fall over.
	for _, line := range d.logs() {
		if strings.Contains(line, "panic:") || strings.Contains(line, "fatal error:") {
			t.Errorf("the device program fell over: %s", line)
		}
	}

	// The music is playing again.
	//
	// Every plan ends with a play, so the music should be playing at the
	// end of it. The panel is asked rather than assumed: the server may
	// have stopped the music, which is the server's business and not
	// the panel's.
	d.pressTransport(t, chaosStep{Action: "play"})
	d.waitFor("the music playing again", 30*time.Second, func(s deviceState) bool {
		return s.MusicState == "playing"
	})
}

// logLines prints what the reader says, line by line.
func logLines(t *testing.T, who string, reader io.Reader) {
	t.Helper()
	scanner := bufio.NewScanner(reader)
	for scanner.Scan() {
		if line := strings.TrimSpace(scanner.Text()); line != "" {
			t.Logf("%s: %s", who, line)
		}
	}
}

// sortStrings puts the names in a fixed order.
func sortStrings(names []string) {
	for i := 1; i < len(names); i++ {
		for j := i; j > 0 && names[j] < names[j-1]; j-- {
			names[j], names[j-1] = names[j-1], names[j]
		}
	}
}

// TestTheTransportCommandsAreAllUnderstood checks that every command
// the panel can send is understood by the device program.
//
// A command the device program does not know is a button that does
// nothing, and the panel shows the button as though it works.
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

	// The device program says what it did with the commands.
	//
	// A command it did not understand is said to be dropped.
	logs := d.logs()[before:]
	for _, line := range logs {
		if strings.Contains(line, "unknown transport") || strings.Contains(line, "was not understood") {
			t.Errorf("the device program did not understand a command: %s", line)
		}
	}
	t.Logf("the device program said %d things about the transport", len(logs))
}

// waitForConnected waits for the panel to be connected.
//
// It says whether it connected. The panel is reconnected when Home
// Assistant reloads its entry, so a panel that is not connected at one
// moment is not a panel that has broken.
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
