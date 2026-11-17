package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestTheRuntimePathIsInMemory(t *testing.T) {
	path := runtimePath("test-device", "state.json", "/tmp/fallback.json")
	if path == "/tmp/fallback.json" {
		t.Skip("this machine has no memory filesystem to use")
	}
	if !isMemory(filepath.Dir(path)) {
		t.Errorf("the runtime path %q is not on a memory filesystem", path)
	}
}

func TestTheRuntimePathFallsBack(t *testing.T) {
	const fallback = "/tmp/fallback.json"
	path := runtimePath("test-device", "state.json", fallback)
	if path == fallback {
		return
	}

	if !isMemory(filepath.Dir(path)) {
		t.Errorf("the runtime path %q is not on a memory filesystem", path)
	}
}

func TestTheRuntimePathIsPerDevice(t *testing.T) {
	first := runtimePath("one", "state.json", "/tmp/a.json")
	second := runtimePath("two", "state.json", "/tmp/b.json")
	if first == second {
		t.Errorf("two devices share the runtime path %q", first)
	}
}

func TestTheRuntimeDirectoryIsRemoved(t *testing.T) {
	path := runtimePath("test-device", "state.json", "/tmp/fallback.json")
	if path == "/tmp/fallback.json" {
		t.Skip("this machine has no memory filesystem to use")
	}
	if err := os.WriteFile(path, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	clearRuntime("test-device")
	if _, err := os.Stat(filepath.Dir(path)); !os.IsNotExist(err) {
		t.Errorf("the runtime directory %q was not removed", filepath.Dir(path))
	}
}

func TestTheSafeNameIsSafe(t *testing.T) {
	for _, name := range []string{"../../etc", "a/b", "", "normal-name_1"} {
		safe := safeName(name)
		if safe == "" {
			t.Errorf("the name %q became nothing", name)
		}
		if filepath.Base(safe) != safe {
			t.Errorf("the name %q became %q, which is not a plain name", name, safe)
		}
	}
}

func TestTheRuntimeBaseFollowsTheService(t *testing.T) {
	t.Setenv("RUNTIME_DIRECTORY", "/run/user/1000/homepanel")
	if got := runtimeBase(); got != "/run/user/1000/homepanel" {
		t.Errorf("the runtime base is %q, not the one the service named", got)
	}
}

func TestTheRuntimeBaseFallsBackToSharedMemory(t *testing.T) {
	t.Setenv("RUNTIME_DIRECTORY", "")
	if got := runtimeBase(); got != runtimeRoot {
		t.Errorf("the runtime base is %q, not the shared memory directory", got)
	}
}
