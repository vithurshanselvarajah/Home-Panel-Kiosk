package main

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

const runtimeRoot = "/dev/shm/homepanel"

func runtimeBase() string {
	if dir := os.Getenv("RUNTIME_DIRECTORY"); dir != "" {
		return dir
	}
	return runtimeRoot
}

func runtimePath(device, name, fallback string) string {
	dir := filepath.Join(runtimeBase(), safeName(device))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fallback
	}

	if !isMemory(dir) {
		return fallback
	}
	return filepath.Join(dir, name)
}

func clearRuntime(device string) {
	base := runtimeBase()
	dir := filepath.Join(base, safeName(device))

	if !strings.HasPrefix(dir, base) {
		return
	}
	_ = os.RemoveAll(dir)
}

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

func isMemory(dir string) bool {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(dir, &stat); err != nil {
		return false
	}

	const tmpfsMagic = 0x01021994
	const ramfsMagic = 0x858458f6
	return stat.Type == tmpfsMagic || stat.Type == ramfsMagic
}
