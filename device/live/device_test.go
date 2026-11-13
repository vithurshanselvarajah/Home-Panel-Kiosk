package live

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// The live device.
//
// The live tests run the real device program — the binary that ships
// — against the real Home Assistant and the real Music Assistant.
//
// That is the point of them. Every other test in this repository runs
// the panel against a peer that was written for the test, which proves
// the panel agrees with itself. These prove it agrees with the thing
// it is for.
//
// The device program is run as a process, not called as a function,
// because the process is what ships: it has its own flags, its own
// announcement and its own connection. A test that called the code
// directly would not notice that the binary could not start.

// device is the running device program.
type device struct {
	t           *testing.T
	cmd         *exec.Cmd
	statePath   string
	controlPath string
	speakerPath string
	address     string
	mu          sync.Mutex
	states      []deviceState
	lines       []string
	stopped     bool
}

// deviceState is the state the device program publishes.
//
// It is the same shape as the panel's state file, so the live tests
// read the panel's own view of the device.
type deviceState struct {
	Connection       string   `json:"connection"`
	ConnectionReason string   `json:"connection_reason"`
	Phase            string   `json:"phase"`
	Listen           string   `json:"listen"`
	Listening        bool     `json:"listening"`
	Speaking         bool     `json:"speaking"`
	Thinking         bool     `json:"thinking"`
	Transcript       string   `json:"transcript"`
	Spoken           string   `json:"spoken"`
	SpokenProgress   float64  `json:"spoken_progress"`
	Muted            bool     `json:"muted"`
	Volume           int      `json:"volume"`
	Level            int      `json:"level"`
	MediaState       string   `json:"media_state"`
	MusicState       string   `json:"music_state"`
	MusicPaired      int      `json:"music_paired"`
	MusicSynced      bool     `json:"music_synced"`
	MusicBuffered    int      `json:"music_buffered"`
	MusicTitle       string   `json:"music_title"`
	MusicArtist      string   `json:"music_artist"`
	MusicAlbum       string   `json:"music_album"`
	MusicArtwork     string   `json:"music_artwork"`
	MusicPosition    int      `json:"music_position"`
	MusicDuration    int      `json:"music_duration"`
	MusicPlaying     bool     `json:"music_playing"`
	MusicTransport   []string `json:"music_transport"`
	LastError        string   `json:"last_error"`
}

// startDevice runs the device program.
//
// The name it is announced under is the one Home Assistant knows it by,
// so the test exercises the device that is really there.
func startDevice(t *testing.T, recording string) *device {
	t.Helper()

	// The tests cannot run at the same time, because they share the
	// panel's ports. So they take a lock, and the one that is waiting
	// says so.
	lockPorts()
	t.Cleanup(unlockPorts)

	binary := liveDeviceBinary()
	if binary == "" {
		t.Skip("no device binary was named")
	}
	if _, err := os.Stat(binary); err != nil {
		t.Fatalf("the device binary is not there: %v", err)
	}

	name := liveName()

	dir := t.TempDir()
	creds := filepath.Join(dir, "credentials")
	if err := os.MkdirAll(creds, 0o755); err != nil {
		t.Fatal(err)
	}
	// The control socket is how the panel talks to the device program,
	// and it is in memory because it carries what is happening right
	// now.
	controlSocket := filepath.Join("/dev/shm/homepanel", name, "control.sock")
	if err := os.MkdirAll(filepath.Dir(controlSocket), 0o700); err != nil {
		t.Fatal(err)
	}

	// The recording is the microphone, and the speaker's audio is
	// written to a file, so the whole audio path is exercised without a
	// sound card.
	speakerFile := filepath.Join(dir, "speaker.wav")

	// The state is written where the device program puts it.
	//
	// The device program writes the live state into memory when it can,
	// because it is a live reading and not a record: it changes as the
	// device listens and speaks, and it is worth nothing once the
	// process is gone. Writing it to the flash would wear the flash out
	// for nothing.
	statePath := os.Getenv("LIVE_STATE_FILE")
	if statePath == "" {
		statePath = filepath.Join("/dev/shm/homepanel", name, "state.json")
	}
	if dir := filepath.Dir(statePath); dir != "" {
		_ = os.MkdirAll(dir, 0o700)
	}
	// An old state file would be read as the new one's first state.
	_ = os.Remove(statePath)

	// The ports have to be free, or the device cannot start and the
	// test is about the wrong thing.
	//
	// They are the panel's own, so Home Assistant finds the device where
	// it expects to find it, and a music server finds the player.
	waitForFreePort(liveSendspinPort())
	waitForFreePort(livePort())

	// The port is the panel's own, so Home Assistant finds the device
	// where it expects to find it.
	//
	// The port has to be fixed rather than chosen by the operating
	// system: Home Assistant finds the device by the announcement, and
	// the announcement carries the port. A device on a port nobody
	// announced is a device nobody connects to.
	//
	// So the tests cannot run at the same time, and they are told which
	// port to use.
	port := livePort()

	args := []string{
		"--credentials-dir", creds,
		"--device-id", name,
		"--esphome-name", name,
		"--board", "Home Panel",
		"--state-file", statePath,
		"--control-socket", controlSocket,
		"--esphome-port", strconv.Itoa(port),
	}
	// The speaker's audio is always written to a file, so the tests can
	// see what the panel played. The recording is the microphone when
	// there is one.
	args = append(args, "--speaker-file", speakerFile)
	if recording != "" {
		args = append(args, "--mic-file", recording)
	}
	// The configuration is read, so the music player is started. It is
	// what the panel is a player to, and it has to be running for the
	// capabilities to say so.
	if config := liveConfigFile(); config != "" {
		args = append(args, "--config-file", config)
	}
	// The device says more when it is asked to.
	if os.Getenv("LIVE_VERBOSE") != "" {
		args = append(args, "--verbose")
	}
	if key := os.Getenv("LIVE_HA_ESPHOME_KEY"); key != "" {
		args = append(args, "--esphome-key", key)
	}

	cmd := exec.Command(binary, args...)
	cmd.Env = append(os.Environ(), "LD_LIBRARY_PATH="+runtimeDirectory(binary))
	stderr, err := cmd.StderrPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}

	d := &device{
		t:           t,
		cmd:         cmd,
		statePath:   statePath,
		controlPath: controlSocket,
		speakerPath: speakerFile,
		address:     "127.0.0.1:" + strconv.Itoa(port),
	}

	// The device's own words, which are how a failure is seen.
	go func() {
		scanner := bufio.NewScanner(stderr)
		for scanner.Scan() {
			d.mu.Lock()
			d.lines = append(d.lines, scanner.Text())
			d.mu.Unlock()
		}
	}()

	// The state, which is how the device is watched.
	go func() {
		for {
			state, err := readState(statePath)
			if err == nil {
				d.mu.Lock()
				d.states = append(d.states, state)
				d.mu.Unlock()
			}
			time.Sleep(100 * time.Millisecond)
		}
	}()

	t.Cleanup(d.stop)
	return d
}

// runtimeDirectory is where the onnx runtime is, next to the binary.
func runtimeDirectory(binary string) string {
	dir := filepath.Dir(binary)
	candidate := filepath.Join(dir, "amd64")
	if _, err := os.Stat(candidate); err == nil {
		return candidate
	}
	return ""
}

// stop shuts the device down.
func (d *device) stop() {
	d.mu.Lock()
	if d.stopped {
		d.mu.Unlock()
		return
	}
	d.stopped = true
	d.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if d.cmd.Process != nil {
		_ = d.cmd.Process.Signal(os.Interrupt)
		done := make(chan struct{})
		go func() { d.cmd.Wait(); close(done) }()
		select {
		case <-done:
		case <-ctx.Done():
			_ = d.cmd.Process.Kill()
		}
	}
}

// speakerBytes says how many bytes the speaker has played.
//
// The speaker's audio is written to a file, so the size of the file is
// how much has been played. A station never ends, so the file keeps
// growing for as long as the station is playing.
func (d *device) speakerBytes() int64 {
	if d.speakerPath == "" {
		return 0
	}
	info, err := os.Stat(d.speakerPath)
	if err != nil {
		return 0
	}
	return info.Size()
}

// peer asks the running device something, over the Home Assistant
// connection.
//
// It is the python peer that drives aioesphomeapi, the library Home
// Assistant speaks the protocol with, so the question is asked the way
// Home Assistant asks it.
func (d *device) peer(mode string, extra string) ([]string, error) {
	python, err := exec.LookPath("python3")
	if err != nil {
		return nil, fmt.Errorf("python3 is not there: %w", err)
	}
	if err := exec.Command(python, "-c", "import aioesphomeapi").Run(); err != nil {
		return nil, fmt.Errorf("aioesphomeapi is not there: %w", err)
	}
	out, err := exec.Command(python, "-c", esphomePeer, mode, d.esphomeAddress()).CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("%s: %s", err, strings.TrimSpace(string(out)))
	}
	var lines []string
	for _, line := range strings.Split(string(out), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			lines = append(lines, line)
		}
	}
	return lines, nil
}

// esphomeAddress is where the device is on the Home Assistant
// connection.
func (d *device) esphomeAddress() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.address
}

// portsLock is the lock the tests take, so they do not run at the same
// time.
var portsLock sync.Mutex

// lockPorts takes the lock.
func lockPorts() {
	portsLock.Lock()
}

// unlockPorts gives it back.
func unlockPorts() {
	portsLock.Unlock()
}

// esphomePeer is the peer that asks the device questions.
//
// It uses aioesphomeapi's own client, so the handshake, the framing and
// the messages are all the ones Home Assistant uses.
const esphomePeer = `
import asyncio, sys

from aioesphomeapi import APIClient

MODE = sys.argv[1]
ADDRESS = sys.argv[2]

async def main():
    host, port = ADDRESS.rsplit(":", 1)
    client = APIClient(host, int(port), noise_psk=None, password=None)
    await client.connect(login=True)

    if MODE == "capabilities":
        caps = await client.device_capabilities()
        print("sendspin_flags:" + str(caps.sendspin.feature_flags))
        print("sendspin_client:" + str(caps.sendspin.client_id))
    elif MODE == "pairing_token":
        resp = await client.sendspin_pairing_token()
        print("status:" + str(resp.status))
        print("token:" + str(resp.token))

    await client.disconnect()

asyncio.run(main())
`

// controlSocket is where the panel talks to the device program.
func (d *device) controlSocket() string {
	return d.controlPath
}

// logs gives back what the device said.
func (d *device) logs() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]string(nil), d.lines...)
}

// lastState gives back the most recent state.
func (d *device) lastState() (deviceState, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.states) == 0 {
		return deviceState{}, false
	}
	return d.states[len(d.states)-1], true
}

// waitFor waits for the state to satisfy something.
//
// It is how the tests wait for the device to do something: the state
// file is the panel's own view of the device, so waiting on it is
// waiting on the thing the person would see.
func (d *device) waitFor(what string, within time.Duration, ok func(deviceState) bool) deviceState {
	d.t.Helper()
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if state, found := d.lastState(); found && ok(state) {
			return state
		}
		time.Sleep(100 * time.Millisecond)
	}
	d.t.Fatalf("the device never %s. It said:\n%s", what, strings.Join(d.logs(), "\n"))
	return deviceState{}
}

// readState reads the state file.
func readState(path string) (deviceState, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return deviceState{}, err
	}
	var state deviceState
	if err := json.Unmarshal(raw, &state); err != nil {
		return deviceState{}, err
	}
	return state, nil
}

// TestTheDeviceConnectsToLiveHomeAssistant checks that the device
// program, the real binary, connects to the real Home Assistant.
//
// This is the first thing that has to work, and everything else is
// built on it.
func TestTheDeviceConnectsToLiveHomeAssistant(t *testing.T) {
	skipUnlessLive(t, "Home Assistant", haURL(), haToken())

	d := startDevice(t, "")
	state := d.waitFor("connected to Home Assistant", 90*time.Second, func(s deviceState) bool {
		return s.Connection == "connected"
	})
	t.Logf("the device is %s: %s", state.Connection, state.ConnectionReason)

	// The entities are there too: a device that connects with no
	// entities is a device Home Assistant cannot use.
	entities := haEntities(t, "home_panel")
	if len(entities) == 0 {
		t.Error("Home Assistant has no entities for the panel")
	}
	t.Logf("Home Assistant has %d panel entities", len(entities))
}

// haEntities gives back the entities whose id has a piece in it.
func haEntities(t *testing.T, piece string) map[string]string {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, haURL()+"/api/states", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+haToken())
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	var states []struct {
		EntityID string `json:"entity_id"`
		State    string `json:"state"`
	}
	if err := json.Unmarshal(raw, &states); err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, s := range states {
		if strings.Contains(s.EntityID, piece) {
			out[s.EntityID] = s.State
		}
	}
	return out
}

// contextUnused keeps the import honest.
var _ = context.Background
var _ = fmt.Sprintf
var _ = slog.Default
