package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TestTheIdentityFollowsTheCredentials checks that the device keeps the
// identity its credentials belong to.
//
// The controller issues a link credential to one device. A device that
// presents it under a different name is a different device as far as the
// controller is concerned, so it creates one — and the dashboard fills up
// with copies of the same panel.
func TestTheIdentityFollowsTheCredentials(t *testing.T) {
	dir := t.TempDir()
	// The device has been admitted: it has the certificate authority.
	if err := os.WriteFile(filepath.Join(dir, "ca.pem"), []byte("ca"), 0o600); err != nil {
		t.Fatal(err)
	}

	// The first run names it, and the identity is recorded.
	if got := resolveIdentity(dir, "first-name"); got != "first-name" {
		t.Fatalf("the identity was %q, want %q", got, "first-name")
	}
	if got := readIdentity(dir); got != "first-name" {
		t.Errorf("the recorded identity was %q, want %q", got, "first-name")
	}

	// A later run that names it differently keeps the identity it was
	// admitted with.
	if got := resolveIdentity(dir, "another-name"); got != "first-name" {
		t.Errorf("the identity was %q, want the stored %q", got, "first-name")
	}
}

// TestAnUnadmittedDeviceKeepsItsName checks that a device with no
// credentials is not given a recorded identity.
//
// A device that has never been admitted has no identity to keep: it is
// waiting to be admitted, and it is admitted under whatever name it
// arrives with.
func TestAnUnadmittedDeviceKeepsItsName(t *testing.T) {
	dir := t.TempDir()

	if got := resolveIdentity(dir, "first-name"); got != "first-name" {
		t.Fatalf("the identity was %q, want %q", got, "first-name")
	}
	if got := readIdentity(dir); got != "" {
		t.Errorf("the identity was recorded as %q, want nothing", got)
	}

	// The credentials arrive and the device is admitted.
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
