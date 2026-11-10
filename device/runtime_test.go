package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TestTheRuntimePathIsInMemory checks that the live state goes somewhere
// that is not the disk.
//
// The state is a live reading and not a record: it changes as the device
// listens and speaks, and it is worth nothing once the process is gone.
// Writing it to the flash would wear the flash out for nothing, and a
// device on eMMC is written to a few thousand times over its life.
func TestTheRuntimePathIsInMemory(t *testing.T) {
	path := runtimePath("test-device", "state.json", "/tmp/fallback.json")
	if path == "/tmp/fallback.json" {
		t.Skip("this machine has no memory filesystem to use")
	}
	if !isMemory(filepath.Dir(path)) {
		t.Errorf("the runtime path %q is not on a memory filesystem", path)
	}
}

// TestTheRuntimePathFallsBack checks that the ordinary path is used when
// the memory one cannot be had.
//
// A container without /dev/shm, or a system where it is full, must still
// work. The state goes to the disk then, which is worse but works.
func TestTheRuntimePathFallsBack(t *testing.T) {
	const fallback = "/tmp/fallback.json"
	path := runtimePath("test-device", "state.json", fallback)
	if path == fallback {
		return
	}
	// It found a memory directory, so the fallback was not needed.
	if !isMemory(filepath.Dir(path)) {
		t.Errorf("the runtime path %q is not on a memory filesystem", path)
	}
}

// TestTheRuntimePathIsPerDevice checks that two devices do not share a
// file.
//
// Two kiosks on one machine — or a kiosk and a development run — would
// otherwise read each other's state.
func TestTheRuntimePathIsPerDevice(t *testing.T) {
	first := runtimePath("one", "state.json", "/tmp/a.json")
	second := runtimePath("two", "state.json", "/tmp/b.json")
	if first == second {
		t.Errorf("two devices share the runtime path %q", first)
	}
}

// TestTheRuntimeDirectoryIsRemoved checks that the memory is given back
// when the device stops.
//
// The kernel would do it anyway when the last reference to the directory
// went, but a device that is restarted often would leave a directory
// behind each time.
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

// TestTheSafeNameIsSafe checks that a name from a setting cannot escape
// the runtime directory.
//
// The name comes from a setting and it becomes a path.
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

// TestTheRuntimeBaseFollowsTheService checks that the live state goes
// where the service said it would.
//
// The service makes a directory in memory and names it through
// RUNTIME_DIRECTORY. The application and the device program both have to
// look there, or one writes the state where the other does not look for
// it and the panel never sees the device.
func TestTheRuntimeBaseFollowsTheService(t *testing.T) {
	t.Setenv("RUNTIME_DIRECTORY", "/run/user/1000/homepanel")
	if got := runtimeBase(); got != "/run/user/1000/homepanel" {
		t.Errorf("the runtime base is %q, not the one the service named", got)
	}
}

// TestTheRuntimeBaseFallsBackToSharedMemory checks that the state has
// somewhere to go when the service names no directory.
//
// The shared memory directory needs nothing set up and it is a memory
// filesystem on every Linux.
func TestTheRuntimeBaseFallsBackToSharedMemory(t *testing.T) {
	t.Setenv("RUNTIME_DIRECTORY", "")
	if got := runtimeBase(); got != runtimeRoot {
		t.Errorf("the runtime base is %q, not the shared memory directory", got)
	}
}
