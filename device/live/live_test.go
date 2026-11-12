// The live tests.
//
// These tests talk to a real Home Assistant and a real Music Assistant.
//
// Everything else in this repository is tested against a peer that was
// written for the test, which proves the code agrees with itself. These
// prove it agrees with the thing it is actually for.
//
// They are skipped unless the servers are named, because they need
// them:
//
//	LIVE_HA_URL=http://192.168.1.132:8123
//	LIVE_HA_TOKEN=...
//	LIVE_MA_URL=http://192.168.1.132:8095
//	LIVE_MA_TOKEN=...
//
// They are also skipped when the servers are not there, so a machine
// without them still runs the suite.
package live

import (
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

// The servers, if they are there.
func haURL() string   { return os.Getenv("LIVE_HA_URL") }
func haToken() string { return os.Getenv("LIVE_HA_TOKEN") }
func maURL() string   { return os.Getenv("LIVE_MA_URL") }
func maToken() string { return os.Getenv("LIVE_MA_TOKEN") }

// skipUnlessLive skips the test when a server is not named.
func skipUnlessLive(t *testing.T, what string, url, token string) {
	t.Helper()
	if url == "" || token == "" {
		t.Skipf("no %s was named", what)
	}
	if !reachable(url) {
		t.Skipf("%s is not reachable at %s", what, url)
	}
}

// reachable says whether the server answers.
func reachable(url string) bool {
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(url + "/")
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode < 500
}

// get asks the server for something and gives back the JSON.
func get(t *testing.T, url, token string) map[string]any {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	client := &http.Client{Timeout: 20 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("%s: %v", url, err)
	}
	return out
}

// post asks the server to do something and gives back the JSON.
func post(t *testing.T, url, token string, body any) map[string]any {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPost, url, strings.NewReader(string(raw)))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("%s: %v", url, err)
	}
	return out
}

// liveName is the name the device is announced under.
//
// Home Assistant connects to the device it knows by name, so a device
// that announces itself under a new name is one nobody connects to.
// The name is the one the panel uses in production, so the test
// exercises the same device Home Assistant already has.
func liveName() string {
	if name := os.Getenv("LIVE_ESPHOME_NAME"); name != "" {
		return name
	}
	return "home-panel"
}

// liveDeviceBinary is the device program the tests run.
//
// It is the binary that ships, because a test that ran the code
// directly would not notice that the binary could not start.
func liveDeviceBinary() string {
	if binary := os.Getenv("LIVE_DEVICE_BINARY"); binary != "" {
		return binary
	}
	return ""
}

// TestTheLiveServersAreThere is the first test: it checks the servers
// answer, so a failure later is about the panel and not about the
// network.
func TestTheLiveServersAreThere(t *testing.T) {
	skipUnlessLive(t, "Home Assistant", haURL(), haToken())
	info := get(t, haURL()+"/api/config", haToken())
	version, _ := info["version"].(string)
	if version == "" {
		t.Fatal("Home Assistant did not say what version it is")
	}
	t.Logf("Home Assistant %s is at %s", version, haURL())

	if maURL() == "" || maToken() == "" {
		t.Log("no Music Assistant was named")
		return
	}
	skipUnlessLive(t, "Music Assistant", maURL(), maToken())
	ma := get(t, maURL()+"/info", maToken())
	maVersion, _ := ma["server_version"].(string)
	if maVersion == "" {
		t.Fatal("Music Assistant did not say what version it is")
	}
	t.Logf("Music Assistant %s is at %s", maVersion, maURL())
}
