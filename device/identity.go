package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const identityFile = "identity"

func readIdentity(dir string) string {
	if dir == "" {
		return ""
	}
	data, err := os.ReadFile(filepath.Join(dir, identityFile))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

func writeIdentity(dir, id string) error {
	if dir == "" || id == "" {
		return nil
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("echomuse-device: creating %s: %w", dir, err)
	}
	path := filepath.Join(dir, identityFile)

	return os.WriteFile(path, []byte(id+"\n"), 0o600)
}

func resolveIdentity(dir, asked string) string {
	if dir == "" {
		return asked
	}
	stored := readIdentity(dir)
	if stored != "" {
		if asked != "" && asked != stored {
			return stored
		}
		return stored
	}
	if asked == "" {
		return ""
	}

	if hasCredentials(dir) {
		_ = writeIdentity(dir, asked)
	}
	return asked
}

func hasCredentials(dir string) bool {
	if dir == "" {
		return false
	}
	_, err := os.Stat(filepath.Join(dir, "ca.pem"))
	return err == nil
}
