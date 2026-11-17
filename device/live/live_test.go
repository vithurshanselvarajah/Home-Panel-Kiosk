package live

import (
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

func haURL() string   { return os.Getenv("LIVE_HA_URL") }
func haToken() string { return os.Getenv("LIVE_HA_TOKEN") }
func maURL() string   { return os.Getenv("LIVE_MA_URL") }
func maToken() string { return os.Getenv("LIVE_MA_TOKEN") }

func skipUnlessLive(t *testing.T, what string, url, token string) {
	t.Helper()
	if url == "" || token == "" {
		t.Skipf("no %s was named", what)
	}
	if !reachable(url) {
		t.Skipf("%s is not reachable at %s", what, url)
	}
}

func reachable(url string) bool {
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(url + "/")
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode < 500
}

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

func post(t *testing.T, url, token string, body any) map[string]any {
	t.Helper()
	out := map[string]any{}
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
	req.Header.Set("Accept", "application/json")
	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	answer, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode >= 400 {
		t.Fatalf("%s said %s: %s", url, resp.Status, strings.TrimSpace(string(answer)))
	}

	var list []any
	if err := json.Unmarshal(answer, &list); err == nil {
		out["list"] = list
		return out
	}
	if err := json.Unmarshal(answer, &out); err != nil {
		t.Fatalf("%s: %v", url, err)
	}
	return out
}

func waitForFreePort(port int) {

	for attempt := 0; attempt < 100; attempt++ {
		listener, err := net.Listen("tcp", ":"+strconv.Itoa(port))
		if err == nil {
			_ = listener.Close()
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func liveSendspinPort() int {
	if port := os.Getenv("LIVE_SENDSPIN_PORT"); port != "" {
		if number, err := strconv.Atoi(port); err == nil {
			return number
		}
	}
	return 8928
}

func livePort() int {
	if port := os.Getenv("LIVE_ESPHOME_PORT"); port != "" {
		if number, err := strconv.Atoi(port); err == nil {
			return number
		}
	}
	return 6053
}

func liveConfigFile() string {
	if path := os.Getenv("LIVE_CONFIG_FILE"); path != "" {
		return path
	}
	return ""
}

func liveName() string {
	if name := os.Getenv("LIVE_ESPHOME_NAME"); name != "" {
		return name
	}
	return "home-panel"
}

func liveDeviceBinary() string {
	if binary := os.Getenv("LIVE_DEVICE_BINARY"); binary != "" {
		return binary
	}
	return ""
}

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
