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

	started time.Time
}

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

func startDevice(t *testing.T, recording string) *device {
	t.Helper()

	pointHomeAssistantHere(t)

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

	creds := filepath.Join(os.TempDir(), "homepanel-live-credentials")
	if err := os.MkdirAll(creds, 0o755); err != nil {
		t.Fatal(err)
	}

	controlSocket := filepath.Join("/dev/shm/homepanel", name, "control.sock")
	if err := os.MkdirAll(filepath.Dir(controlSocket), 0o700); err != nil {
		t.Fatal(err)
	}

	speakerFile := filepath.Join(dir, "speaker.wav")

	statePath := os.Getenv("LIVE_STATE_FILE")
	if statePath == "" {
		statePath = filepath.Join("/dev/shm/homepanel", name, "state.json")
	}
	if dir := filepath.Dir(statePath); dir != "" {
		_ = os.MkdirAll(dir, 0o700)
	}

	_ = os.Remove(statePath)

	waitForFreePort(liveSendspinPort())
	waitForFreePort(livePort())

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

	args = append(args, "--speaker-file", speakerFile)
	if recording != "" {
		args = append(args, "--mic-file", recording)
	}

	if config := liveConfigFile(); config != "" {
		args = append(args, "--config-file", config)
	}

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

	go func() {
		scanner := bufio.NewScanner(stderr)
		for scanner.Scan() {
			d.mu.Lock()
			d.lines = append(d.lines, scanner.Text())
			d.mu.Unlock()
		}
	}()

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

	go d.retryUntilConnected()

	return d
}

func (d *device) retryUntilConnected() {
	deadline := time.Now().Add(90 * time.Second)

	pointed := false
	for time.Now().Before(deadline) {
		if state, found := d.lastState(); found && state.Connection == "connected" {
			return
		}

		if !pointed && time.Now().After(d.started.Add(10*time.Second)) {
			pointed = true
			pointHomeAssistantHere(d.t)
		}
		time.Sleep(2 * time.Second)
	}
}

func restoreHomeAssistantEntry(t *testing.T) {
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

func TestMain(m *testing.M) {
	code := m.Run()

	if haURL() != "" && haToken() != "" {
		restoreHomeAssistantEntry(&testing.T{})
	}
	os.Exit(code)
}

func runtimeDirectory(binary string) string {
	dir := filepath.Dir(binary)
	candidate := filepath.Join(dir, "amd64")
	if _, err := os.Stat(candidate); err == nil {
		return candidate
	}
	return ""
}

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

	waitForFreePort(livePort())
	waitForFreePort(liveSendspinPort())
}

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

func (d *device) esphomeAddress() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.address
}

var portsLock sync.Mutex

func lockPorts() {
	portsLock.Lock()
}

func unlockPorts() {
	portsLock.Unlock()
}

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

func (d *device) controlSocket() string {
	return d.controlPath
}

func (d *device) logs() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]string(nil), d.lines...)
}

func (d *device) lastState() (deviceState, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.states) == 0 {
		return deviceState{}, false
	}
	return d.states[len(d.states)-1], true
}

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

func TestTheDeviceConnectsToLiveHomeAssistant(t *testing.T) {
	skipUnlessLive(t, "Home Assistant", haURL(), haToken())

	d := startDevice(t, "")
	state := d.waitFor("connected to Home Assistant", 90*time.Second, func(s deviceState) bool {
		return s.Connection == "connected"
	})
	t.Logf("the device is %s: %s", state.Connection, state.ConnectionReason)

	entities := haEntities(t, "home_panel")
	if len(entities) == 0 {
		t.Error("Home Assistant has no entities for the panel")
	}
	t.Logf("Home Assistant has %d panel entities", len(entities))
}

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

var _ = context.Background
var _ = fmt.Sprintf
var _ = slog.Default

func pointHomeAssistantHere(t *testing.T) {
	t.Helper()
	if haURL() == "" || haToken() == "" {
		return
	}

	address := localAddress(t)

	entries := configEntries(t, "esphome")
	for _, entry := range entries {
		if entry.title != "Home Panel" {
			continue
		}

		if lastEsphomeAddress(t) == address {
			return
		}
		if !esphomeAddressRemembered() {
			rememberEsphomeAddress(t, currentEsphomeAddress(t))
		}
		reconfigureEsphomeEntry(t, entry.id, address, livePort())
		rememberEsphomeAddress(t, address)

		waitForEntryToLoad(t)
		return
	}
	t.Log("Home Assistant has no esphome entry for the panel")
}

func esphomeAddressFile() string {
	return filepath.Join(os.TempDir(), "homepanel-live-esphome-address")
}

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

func esphomeAddressRemembered() bool {
	_, err := os.Stat(esphomeAddressFile())
	return err == nil
}

func currentEsphomeAddress(t *testing.T) string {
	t.Helper()
	if address := os.Getenv("LIVE_ESPHOME_ADDRESS"); address != "" {
		return address
	}
	return ""
}

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

func waitForEntryToLoad(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		for _, entry := range configEntries(t, "esphome") {
			if entry.title == "Home Panel" && entry.state == "loaded" {
				return
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Log("the entry did not say it was loaded")
}

type configEntry struct {
	id    string
	title string
	state string
	host  string
	port  int
}

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
			state: stringOf(item["state"]),
		}

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

func reconfigureEsphomeEntry(t *testing.T, entryID, host string, port int) {
	t.Helper()
	flow, err := httpPost(haURL()+"/api/config/config_entries/flow", haToken(), map[string]any{
		"handler":               "esphome",
		"entry_id":              entryID,
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

func localAddress(t *testing.T) string {
	t.Helper()

	address := haURL()
	address = strings.TrimPrefix(address, "http://")
	address = strings.TrimPrefix(address, "https://")

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

func stringOf(value any) string {
	if text, ok := value.(string); ok {
		return text
	}
	return ""
}
