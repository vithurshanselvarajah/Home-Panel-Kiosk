package main

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// runtimeRoot is where the live state lives.
//
// The state is a live reading, not a record: it changes as the device
// listens and speaks, and it is worth nothing once the process is gone.
// Writing it to the flash would wear the flash out for nothing — a device
// on eMMC is written to a few thousand times over its life, and a kiosk
// that rewrites a file ten times a second spends that in a month.
//
// So it goes into memory. The directory is a tmpfs, which is RAM the
// kernel gives back when the last reference to it goes, so nothing is
// left behind and nothing is written to the disk.
//
// The path is under /dev/shm, which every Linux has and which is a
// memory filesystem by definition.
const runtimeRoot = "/dev/shm/homepanel"

// runtimeBase is the directory the live state lives in.
//
// The service gives the application a directory in memory and tells it
// where it is through RUNTIME_DIRECTORY. The device program has to look
// in the same place, or the two would not find the same state file.
//
// The shared memory directory is the fallback: it is where the state goes
// when the service does not name a directory, and it is a memory
// filesystem on every Linux.
func runtimeBase() string {
	if dir := os.Getenv("RUNTIME_DIRECTORY"); dir != "" {
		return dir
	}
	return runtimeRoot
}

// runtimePath is where a live file for a device lives.
//
// The device's own name is in it, so two devices on one machine — two
// kiosks, or a kiosk and a development run — do not share a file.
//
// The fallback is used when the memory directory cannot be had: a
// container without /dev/shm, or a system where it is full. The state is
// then written to disk, which is worse but works.
func runtimePath(device, name, fallback string) string {
	dir := filepath.Join(runtimeBase(), safeName(device))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fallback
	}
	// The directory has to be a memory one, or the fallback is the
	// ordinary directory and the state goes to the disk as it always did.
	if !isMemory(dir) {
		return fallback
	}
	return filepath.Join(dir, name)
}

// clearRuntime removes the runtime directory for a device.
//
// It is called when the device stops, so the memory is given back at once
// rather than when the last reference to it goes. The kernel would do it
// anyway, but a device that is restarted often would otherwise leave a
// directory behind each time.
func clearRuntime(device string) {
	base := runtimeBase()
	dir := filepath.Join(base, safeName(device))
	// The directory that is removed has to be inside the base it was
	// taken from, or a name with a path in it could remove something
	// else.
	if !strings.HasPrefix(dir, base) {
		return
	}
	_ = os.RemoveAll(dir)
}

// safeName makes a name safe to use as a directory name.
//
// It comes from a setting and it becomes a path, so anything that is not
// a letter, a digit, a dash or an underscore is a dash.
func safeName(name string) string {
	safe := strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' {
			return r
		}
		return '-'
	}, name)
	if safe == "" {
		return "device"
	}
	return safe
}

// isMemory says whether a path is on a memory filesystem.
//
// The device's own state is small — a few kilobytes — so the memory
// filesystem does not have to be big. What matters is that it is not the
// disk.
func isMemory(dir string) bool {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(dir, &stat); err != nil {
		return false
	}
	// TMPFS_MAGIC and RAMFS_MAGIC.
	const tmpfsMagic = 0x01021994
	const ramfsMagic = 0x858458f6
	return stat.Type == tmpfsMagic || stat.Type == ramfsMagic
}
