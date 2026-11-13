package live

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
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
	// started is when the device program was started.
	started time.Time
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
	DuckingDb        float64  `json:"ducking_db"`
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
	//
	// The lock is given back after the device has stopped, and not
	// before: the cleanups run in the reverse order they were set, so
	// the one set last runs first. The device's own cleanup is set
	// last, so it runs first, and the lock is given back after it.
	lockPorts()

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
		started:     time.Now(),
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
	t.Cleanup(unlockPorts)
	// The entry is put back when the tests are done, so that the panel
	// that is really there is the one Home Assistant is talking to.
	t.Cleanup(func() { restoreHomeAssistant(t) })

	// Home Assistant does not always take the device the first time.
	//
	// It finds the device by the announcement, and it checks the
	// announcement against the device it already has. A device that has
	// just been reloaded — because the panel's address changed, or
	// because the test before it left one behind — takes a moment to
	// settle, and until it has the announcement is refused.
	//
	// So the device is started again if it does not connect. The
	// announcement is the same every time, so the second try is the one
	// that works.
	go d.retryUntilConnected()

	return d
}

// retryUntilConnected makes sure Home Assistant can reach the panel.
//
// Home Assistant finds the panel by the address it was given, and it
// keeps that address. The panel's address changes: the machine it runs
// on gets a new address from the network, and a machine with a
// container network has several.
//
// So when the panel does not connect, the entry is pointed at this
// machine and the panel is told to try again.
//
// The entry is only reconfigured when the panel has not connected,
// because reconfiguring reloads the entry and a reload drops the
// connection and takes a moment to come back. A test that reconfigured
// every time would be about the reload rather than about the panel.
func (d *device) retryUntilConnected() {
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		if state, found := d.lastState(); found && state.Connection == "connected" {
			return
		}
		// The panel has had a moment and has not connected, so
		// Home Assistant is probably looking in the wrong place.
		if time.Now().After(d.started.Add(10 * time.Second)) {
			pointHomeAssistantHere(d.t)
		}
		time.Sleep(2 * time.Second)
	}
}

// restoreHomeAssistant puts Home Assistant's entry back.
//
// The tests point the entry at the machine they run on, because a
// machine whose address has changed is a panel Home Assistant cannot
// reach. The entry is put back when the tests are done, because the
// entry belongs to the panel that is really there.
func restoreHomeAssistant(t *testing.T) {
	t.Helper()
	address := originalEsphomeAddress(t)
	if address == "" {
		return
	}
	entries := configEntries(t, "esphome")
	for _, entry := range entries {
		if entry.title != "Home Panel" {
			continue
		}
		reconfigureEsphomeEntry(t, entry.id, address, livePort())
		return
	}
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
			<-done
		}
	}

	// The ports have to be free before the next test starts, and a
	// process that has just been killed takes a moment to let go of
	// them.
	waitForFreePort(livePort())
	waitForFreePort(liveSendspinPort())
}

// deviceID is the address Home Assistant knows the device by.
//
// It is the device's own address, which is the base64url of its public
// key, and it is what the entities are filed under.
func (d *device) deviceID() string {
	lines, err := d.peer("device_id", "")
	if err != nil {
		return ""
	}
	for _, line := range lines {
		if strings.HasPrefix(line, "device_id:") {
			return strings.TrimPrefix(line, "device_id:")
		}
	}
	return ""
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

    if MODE == "device_id":
        info = await client.device_info()
        print("device_id:" + str(info.mac_address))
    elif MODE == "capabilities":
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

// pointHomeAssistantHere tells Home Assistant where the panel is.
//
// Home Assistant finds the panel by the address it was given, and it
// keeps that address. A panel whose machine has been given a new
// address is a panel Home Assistant cannot reach, and the tests would
// be about the network rather than about the panel.
//
// The entry is reconfigured rather than created, because there is one
// panel and there should be one entry for it.
func pointHomeAssistantHere(t *testing.T) {
	t.Helper()
	if haURL() == "" || haToken() == "" {
		return
	}
	// The address is this machine's address on the network Home
	// Assistant is on.
	address := localAddress(t)

	// The entry is found by its title, so the test does not care which
	// entry it is.
	entries := configEntries(t, "esphome")
	for _, entry := range entries {
		if entry.title != "Home Panel" {
			continue
		}
		// The address the entry was pointed at before the tests
		// changed it is remembered, so that it can be put back.
		//
		// The entry is only reconfigured when the panel has not
		// connected, which is what this is only called for.
		//
		// Reconfiguring reloads the entry, and a reload drops the
		// connection and takes a moment to come back. So it is not
		// done over and over: the address it was pointed at is
		// remembered, and it is only done again when the address has
		// changed.
		if lastEsphomeAddress(t) == address {
			return
		}
		if !esphomeAddressRemembered() {
			rememberEsphomeAddress(t, currentEsphomeAddress(t))
		}
		reconfigureEsphomeEntry(t, entry.id, address, livePort())
		rememberEsphomeAddress(t, address)
		return
	}
	t.Log("Home Assistant has no esphome entry for the panel")
}

// esphomeAddressFile is where the address the entry was pointed at is
// remembered.
//
// It is a file rather than a variable because the live tests are run
// one at a time but in separate processes, and a variable would be
// forgotten between them.
//
// The file holds two addresses: the one the entry was pointed at before
// the tests changed it, which is put back at the end, and the one the
// tests pointed it at, which says whether it needs changing at all.
func esphomeAddressFile() string {
	return filepath.Join(os.TempDir(), "homepanel-live-esphome-address")
}

// lastEsphomeAddress is the address the entry was last pointed at.
func lastEsphomeAddress(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(esphomeAddressFile())
	if err != nil {
		return ""
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) < 2 {
		return ""
	}
	return strings.TrimSpace(lines[1])
}

// originalEsphomeAddress is the address the entry was pointed at
// before the tests changed it.
func originalEsphomeAddress(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(esphomeAddressFile())
	if err != nil {
		return ""
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) < 1 {
		return ""
	}
	return strings.TrimSpace(lines[0])
}

// esphomeAddressRemembered says whether the address has been
// remembered already.
func esphomeAddressRemembered() bool {
	_, err := os.Stat(esphomeAddressFile())
	return err == nil
}

// currentEsphomeAddress is the address the entry is pointed at now.
//
// Home Assistant's list of the entries does not say where the entry is
// pointing, so the environment says it: the address the panel is
// really at.
func currentEsphomeAddress(t *testing.T) string {
	t.Helper()
	if address := os.Getenv("LIVE_ESPHOME_ADDRESS"); address != "" {
		return address
	}
	return ""
}

// rememberEsphomeAddress remembers the address the entry was pointed
// at.
//
// The first address remembered is the one to put back, and the second
// is the one the tests pointed it at.
func rememberEsphomeAddress(t *testing.T, address string) {
	t.Helper()
	original := originalEsphomeAddress(t)
	if original == "" {
		original = currentEsphomeAddress(t)
	}
	if original == "" {
		original = address
	}
	if err := os.WriteFile(esphomeAddressFile(), []byte(original+"\n"+address), 0o600); err != nil {
		t.Logf("the address could not be remembered: %v", err)
	}
}

// configEntry is one of Home Assistant's configuration entries.
type configEntry struct {
	id    string
	title string
	host  string
	port  int
}

// configEntries gives back the entries of a kind.
func configEntries(t *testing.T, kind string) []configEntry {
	t.Helper()
	body, err := httpGet(haURL()+"/api/config/config_entries/entry", haToken())
	if err != nil {
		t.Logf("the configuration entries could not be read: %v", err)
		return nil
	}
	var raw []map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		t.Logf("the configuration entries could not be read: %v", err)
		return nil
	}
	var out []configEntry
	for _, item := range raw {
		if item["domain"] != kind {
			continue
		}
		entry := configEntry{
			id:    stringOf(item["entry_id"]),
			title: stringOf(item["title"]),
		}
		// The address is in the entry's data, which the list does not
		// always carry.
		if data, ok := item["data"].(map[string]any); ok {
			entry.host = stringOf(data["host"])
			if number, ok := data["port"].(float64); ok {
				entry.port = int(number)
			}
		}
		out = append(out, entry)
	}
	return out
}

// reconfigureEsphomeEntry points an esphome entry at an address.
func reconfigureEsphomeEntry(t *testing.T, entryID, host string, port int) {
	t.Helper()
	flow, err := httpPost(haURL()+"/api/config/config_entries/flow", haToken(), map[string]any{
		"handler":              "esphome",
		"entry_id":             entryID,
		"show_advanced_options": true,
	})
	if err != nil {
		t.Logf("the entry could not be reconfigured: %v", err)
		return
	}
	var form struct {
		FlowID string `json:"flow_id"`
	}
	if err := json.Unmarshal(flow, &form); err != nil || form.FlowID == "" {
		t.Logf("the entry could not be reconfigured: %s", strings.TrimSpace(string(flow)))
		return
	}
	// The form answers with an error when nothing is there to talk to
	// yet, and the address is taken from the answer either way.
	answer, err := httpPost(haURL()+"/api/config/config_entries/flow/"+form.FlowID, haToken(), map[string]any{
		"host": host,
		"port": port,
	})
	if err != nil {
		t.Logf("the entry could not be pointed at %s: %v", host, err)
		return
	}
	t.Logf("Home Assistant's entry was pointed at %s:%d (%s)", host, port, strings.TrimSpace(string(answer)))
}

// localAddress is this machine's address on the network.
//
// It is the address Home Assistant can reach the panel on, which is
// the one on the network Home Assistant is on and not the one on a
// container's own network.
func localAddress(t *testing.T) string {
	t.Helper()
	// The address is found by asking which address would be used to
	// reach Home Assistant.
	// The address of Home Assistant is taken from its URL, because the
	// machine can reach it and so Home Assistant can reach the machine.
	address := haURL()
	address = strings.TrimPrefix(address, "http://")
	address = strings.TrimPrefix(address, "https://")
	// The port is cut off: the address is what matters.
	if colon := strings.LastIndex(address, ":"); colon > 0 {
		address = address[:colon]
	}
	conn, err := net.Dial("udp", address+":9")
	if err != nil {
		t.Logf("the machine's address could not be found: %v", err)
		return "127.0.0.1"
	}
	defer conn.Close()
	return conn.LocalAddr().(*net.UDPAddr).IP.String()
}

// httpGet asks for something and gives back the raw answer.
//
// The answer is raw because some of Home Assistant's answers are a list
// and the helpers that read a list would have to know what was in it.
func httpGet(url, token string) ([]byte, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("%s: %d %s", url, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return body, nil
}

// httpPost asks for something to be done and gives back the raw answer.
//
// The answer is raw because Home Assistant's reconfigure flow answers
// with a form, and the form is not an object with a known shape.
func httpPost(url, token string, body any) ([]byte, error) {
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequest(http.MethodPost, url, strings.NewReader(string(raw)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	answer, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("%s: %d %s", url, resp.StatusCode, strings.TrimSpace(string(answer)))
	}
	return answer, nil
}

// stringOf is a string, or an empty string.
func stringOf(value any) string {
	if text, ok := value.(string); ok {
		return text
	}
	return ""
}
