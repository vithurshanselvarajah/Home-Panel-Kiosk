package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// identityFile is where the device keeps the identity the credentials
// belong to.
//
// The link credential is issued to one device and not another: the
// controller confirms the token against the device id in the registration,
// and a connection with a token that belongs to a different device is
// refused and leaves a new device behind in the dashboard.
//
// So the identity and the credential have to be kept together. The
// credential is a token and a certificate authority, and neither says
// which device they are for.
const identityFile = "identity"

// readIdentity reads the identity the credentials belong to.
//
// It is empty when there is none, which is a device that has never been
// admitted.
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

// writeIdentity records the identity the credentials belong to.
//
// It is written once, when the device is first admitted, and it is never
// changed. A device that changed its identity would present a token that
// belongs to its previous self, and the controller would treat it as a
// new device every time.
func writeIdentity(dir, id string) error {
	if dir == "" || id == "" {
		return nil
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("echomuse-device: creating %s: %w", dir, err)
	}
	path := filepath.Join(dir, identityFile)
	// The identity is not a secret, but it lives with the credentials and
	// the directory is private, so the file follows the directory.
	return os.WriteFile(path, []byte(id+"\n"), 0o600)
}

// resolveIdentity decides which device id the device uses.
//
// The identity the credentials belong to wins over anything the caller
// asked for. A caller that names a different device is naming one the
// credential does not belong to, and the controller would create it.
//
// The first identity is recorded, so a device that has been admitted
// keeps the identity it was admitted with.
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
	// The identity is recorded only when the credentials are there too,
	// because a device with an identity and no credential is a device
	// waiting to be admitted, and it has not been admitted under any
	// identity yet.
	if hasCredentials(dir) {
		_ = writeIdentity(dir, asked)
	}
	return asked
}

// hasCredentials says whether the device has been admitted.
//
// The certificate authority is what the controller sends after an
// approval, so its presence is what says the device has one.
func hasCredentials(dir string) bool {
	if dir == "" {
		return false
	}
	_, err := os.Stat(filepath.Join(dir, "ca.pem"))
	return err == nil
}
