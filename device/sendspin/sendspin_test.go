package sendspin

import (
	"encoding/base32"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The pairing token is what a server types to pair with the device, so its
// shape is a contract with the server rather than an internal detail.

func TestPairingTokenIsTheDocumentedShape(t *testing.T) {
	clientKey := make([]byte, keySize)
	psk := make([]byte, pskSize)
	for i := range clientKey {
		clientKey[i] = byte(i)
	}
	for i := range psk {
		psk[i] = byte(0xe0 + i)
	}

	token := pairingToken(clientKey, psk)

	if !strings.HasPrefix(token, "SP:0") {
		t.Errorf("the token %q does not start with SP:0", token)
	}
	// The body is base32 with the padding stripped and every '2' written
	// as '9', so the token is QR-alphanumeric and survives being typed.
	if strings.ContainsAny(token, "=+/") {
		t.Errorf("the token %q holds a character that cannot be typed", token)
	}
	if strings.Contains(token, "2") {
		t.Errorf("the token %q holds a 2, which is written as a 9", token)
	}
}

func TestPairingTokenRoundTrips(t *testing.T) {
	clientKey := make([]byte, keySize)
	psk := make([]byte, pskSize)
	for i := range clientKey {
		clientKey[i] = byte(i)
	}
	for i := range psk {
		psk[i] = byte(0xe0 + i)
	}

	token := pairingToken(clientKey, psk)
	gotKey, gotPSK, err := decodePairingToken(token)
	if err != nil {
		t.Fatalf("the token did not decode: %v", err)
	}
	if string(gotKey) != string(clientKey) {
		t.Errorf("the client key did not survive the round trip")
	}
	if string(gotPSK) != string(psk) {
		t.Errorf("the pairing PSK did not survive the round trip")
	}
}

func TestPairingTokenBodyIsTheTwoKeys(t *testing.T) {
	clientKey := make([]byte, keySize)
	psk := make([]byte, pskSize)
	token := pairingToken(clientKey, psk)

	body := strings.TrimPrefix(token, "SP:0")
	raw, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(strings.ReplaceAll(body, "9", "2"))
	if err != nil {
		t.Fatalf("the token body did not decode: %v", err)
	}
	if len(raw) != 2*keySize {
		t.Errorf("the token body is %d bytes, want %d", len(raw), 2*keySize)
	}
}

// The client id is the device's identity to every server it has ever met, so
// it has to survive a restart.

func TestClientIDSurvivesAReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sendspin.json")

	first, err := openStore(path)
	if err != nil {
		t.Fatalf("the store did not open: %v", err)
	}
	id := first.id.clientID()
	if len(id) != idLen {
		t.Errorf("the client id is %d chars, want %d", len(id), idLen)
	}
	if err := first.saveLocked(); err != nil {
		t.Fatalf("the store did not save: %v", err)
	}

	second, err := openStore(path)
	if err != nil {
		t.Fatalf("the store did not reopen: %v", err)
	}
	if got := second.id.clientID(); got != id {
		t.Errorf("the client id changed on reopen: %q then %q", id, got)
	}
}

func TestTheTokenSurvivesAReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sendspin.json")

	first, err := openStore(path)
	if err != nil {
		t.Fatalf("the store did not open: %v", err)
	}
	token := first.token()
	if err := first.saveLocked(); err != nil {
		t.Fatalf("the store did not save: %v", err)
	}

	second, err := openStore(path)
	if err != nil {
		t.Fatalf("the store did not reopen: %v", err)
	}
	if got := second.token(); got != token {
		t.Errorf("the pairing token changed on reopen")
	}
}

// A corrupt file must not be a silent reset: resetting would change the
// client id and unpair the device from every server it knows.

func TestACorruptStoreIsAnError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sendspin.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := openStore(path); err == nil {
		t.Error("a corrupt store was read as a fresh one")
	}
}

// The secrets live in the store file and nowhere else.

func TestTheStoreFileHoldsTheSecrets(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sendspin.json")
	s, err := openStore(path)
	if err != nil {
		t.Fatalf("the store did not open: %v", err)
	}
	if err := s.saveLocked(); err != nil {
		t.Fatalf("the store did not save: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var file storeFile
	if err := json.Unmarshal(data, &file); err != nil {
		t.Fatal(err)
	}
	if file.PrivateKey == "" {
		t.Error("the private key was not saved")
	}
	if file.PairingPSK == "" {
		t.Error("the pairing PSK was not saved")
	}
}

// The pairing records are bounded, and a pairing never fails for want of
// room: the least recently used one goes.

func TestPairingRecordsAreBounded(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sendspin.json")
	s, err := openStore(path)
	if err != nil {
		t.Fatalf("the store did not open: %v", err)
	}
	for i := 0; i < maxRecords+4; i++ {
		psk := make([]byte, pskSize)
		psk[0] = byte(i)
		if err := s.addRecord("server-"+string(rune('a'+i)), psk, func(string) bool { return false }); err != nil {
			t.Fatalf("the record was not added: %v", err)
		}
	}
	if got := len(s.f.Records); got > maxRecords {
		t.Errorf("the store holds %d records, want at most %d", got, maxRecords)
	}
}
