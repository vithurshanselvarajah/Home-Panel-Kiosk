package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestTheIdentityFollowsTheCredentials(t *testing.T) {
	dir := t.TempDir()

	if err := os.WriteFile(filepath.Join(dir, "ca.pem"), []byte("ca"), 0o600); err != nil {
		t.Fatal(err)
	}

	if got := resolveIdentity(dir, "first-name"); got != "first-name" {
		t.Fatalf("the identity was %q, want %q", got, "first-name")
	}
	if got := readIdentity(dir); got != "first-name" {
		t.Errorf("the recorded identity was %q, want %q", got, "first-name")
	}

	if got := resolveIdentity(dir, "another-name"); got != "first-name" {
		t.Errorf("the identity was %q, want the stored %q", got, "first-name")
	}
}

func TestAnUnadmittedDeviceKeepsItsName(t *testing.T) {
	dir := t.TempDir()

	if got := resolveIdentity(dir, "first-name"); got != "first-name" {
		t.Fatalf("the identity was %q, want %q", got, "first-name")
	}
	if got := readIdentity(dir); got != "" {
		t.Errorf("the identity was recorded as %q, want nothing", got)
	}

	if err := os.WriteFile(filepath.Join(dir, "ca.pem"), []byte("ca"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := resolveIdentity(dir, "first-name"); got != "first-name" {
		t.Fatalf("the identity was %q, want %q", got, "first-name")
	}
	if got := readIdentity(dir); got != "first-name" {
		t.Errorf("the recorded identity was %q, want %q", got, "first-name")
	}
}
