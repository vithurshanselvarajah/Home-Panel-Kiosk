package sendspin

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/curve25519"
)

const (
	keySize = 32
	pskSize = 32
	idLen   = 43
)

var sentinelPSK = func() []byte { h := sha256.Sum256([]byte("sendspin-sentinel-psk-v1")); return h[:] }()

func b64u(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

func unb64u(s string) ([]byte, error) {
	return base64.RawURLEncoding.DecodeString(strings.TrimRight(s, "="))
}

func pskID(psk []byte) string {
	h := sha256.New()
	h.Write([]byte("sendspin-psk-id-v1"))
	h.Write(psk)
	return b64u(h.Sum(nil))
}

type identity struct {
	priv, pub []byte
}

func newIdentity() (identity, error) {
	priv := make([]byte, keySize)
	if _, err := rand.Read(priv); err != nil {
		return identity{}, err
	}
	return identityFrom(priv)
}

func identityFrom(priv []byte) (identity, error) {
	if len(priv) != keySize {
		return identity{}, fmt.Errorf("sendspin: private key is %d bytes", len(priv))
	}
	pub, err := curve25519.X25519(priv, curve25519.Basepoint)
	if err != nil {
		return identity{}, err
	}
	return identity{priv: append([]byte(nil), priv...), pub: pub}, nil
}

func (id identity) clientID() string { return b64u(id.pub) }

func peerKey(id string) ([]byte, error) {
	if len(id) != idLen {
		return nil, fmt.Errorf("sendspin: peer id is %d chars, want %d", len(id), idLen)
	}
	k, err := unb64u(id)
	if err != nil || len(k) != keySize {
		return nil, errors.New("sendspin: peer id is not a 32-byte base64url key")
	}
	return k, nil
}

func randomPSK() ([]byte, error) {
	p := make([]byte, pskSize)
	_, err := rand.Read(p)
	return p, err
}

var b32 = base32.StdEncoding.WithPadding(base32.NoPadding)

func pairingToken(clientKey, pairingPSK []byte) string {
	body := b32.EncodeToString(append(append([]byte(nil), clientKey...), pairingPSK...))
	return "SP:0" + strings.ReplaceAll(body, "2", "9")
}
