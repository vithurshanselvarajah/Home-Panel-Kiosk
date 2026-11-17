package sendspin

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/flynn/noise"
)

const suiteName = "25519_ChaChaPoly_SHA256"

var cipherSuite = noise.NewCipherSuite(noise.DH25519, noise.CipherChaChaPoly, noise.HashSHA256)

var placeholderPSK = make([]byte, pskSize)

type transport struct {
	send, recv *noise.CipherState
}

func (t *transport) encrypt(pt []byte) ([]byte, error) {
	return t.send.Encrypt(nil, nil, pt)
}

func (t *transport) decrypt(ct []byte) ([]byte, error) {
	return t.recv.Decrypt(nil, nil, ct)
}

type responder struct {
	hs *noise.HandshakeState
}

func newResponder(id identity, serverPub, prologue []byte) (*responder, error) {
	hs, err := noise.NewHandshakeState(noise.Config{
		CipherSuite:           cipherSuite,
		Pattern:               noise.HandshakeKK,
		Initiator:             false,
		Prologue:              prologue,
		StaticKeypair:         noise.DHKey{Private: id.priv, Public: id.pub},
		PeerStatic:            serverPub,
		PresharedKey:          placeholderPSK,
		PresharedKeyPlacement: 2,
	})
	if err != nil {
		return nil, err
	}
	return &responder{hs: hs}, nil
}

type msg1Payload struct {
	PskID       string `json:"psk_id"`
	PskCategory string `json:"psk_category,omitempty"`
}

func (r *responder) readMsg1(msg []byte) (msg1Payload, error) {
	pt, _, _, err := r.hs.ReadMessage(nil, msg)
	if err != nil {
		return msg1Payload{}, fmt.Errorf("noise message 1: %w", err)
	}
	var p msg1Payload
	if err := json.Unmarshal(pt, &p); err != nil || len(p.PskID) != idLen {
		return msg1Payload{}, errors.New("noise message 1: malformed payload")
	}
	return p, nil
}

func (r *responder) writeMsg2(psk []byte) ([]byte, *transport, []byte, error) {
	if err := r.hs.SetPresharedKey(psk); err != nil {
		return nil, nil, nil, err
	}
	msg, c1, c2, err := r.hs.WriteMessage(nil, []byte("{}"))
	if err != nil {
		return nil, nil, nil, err
	}
	if c1 == nil || c2 == nil {
		return nil, nil, nil, errors.New("noise: handshake did not complete")
	}

	return msg, &transport{send: c2, recv: c1}, r.hs.ChannelBinding(), nil
}

const (
	msgJSON         = 0
	msgFragment     = 1
	msgFragMore911  = 2
	msgFragEnd911   = 3
	msgAudioChunk   = 4
	maxReassembled  = 16 << 20
	fragFlagFirst   = 1 << 1
	fragFlagLast    = 1 << 0
	fragFlagsUnused = 0xFC
)

type reassembler struct {
	buf    []byte
	typ    byte
	active bool
}

var errFragment = errors.New("sendspin: malformed fragment sequence")

func (r *reassembler) push(pt []byte) ([]byte, error) {
	if len(pt) == 0 {
		return nil, errFragment
	}
	switch pt[0] {
	case msgFragment:
		if len(pt) < 2 || pt[1]&fragFlagsUnused != 0 {
			return nil, errFragment
		}
		flags, data := pt[1], pt[2:]
		if flags&fragFlagFirst != 0 {
			if r.active || len(data) < 1 || data[0] == msgFragment {
				return nil, errFragment
			}
			r.typ, r.buf, r.active = data[0], append(r.buf[:0], data[1:]...), true
		} else {
			if !r.active {
				return nil, errFragment
			}
			r.buf = append(r.buf, data...)
		}
		return r.finish(flags&fragFlagLast != 0)
	case msgFragMore911:
		if !r.active {
			if len(pt) < 2 {
				return nil, errFragment
			}
			r.typ, r.buf, r.active = pt[1], append(r.buf[:0], pt[2:]...), true
			return nil, r.check()
		}
		r.buf = append(r.buf, pt[1:]...)
		return nil, r.check()
	case msgFragEnd911:
		if !r.active {
			return nil, errFragment
		}
		r.buf = append(r.buf, pt[1:]...)
		return r.finish(true)
	default:
		if r.active {
			return nil, errFragment
		}
		return pt, nil
	}
}

func (r *reassembler) check() error {
	if len(r.buf) > maxReassembled {
		return errFragment
	}
	return nil
}

func (r *reassembler) finish(last bool) ([]byte, error) {
	if err := r.check(); err != nil {
		return nil, err
	}
	if !last {
		return nil, nil
	}
	out := make([]byte, 1+len(r.buf))
	out[0] = r.typ
	copy(out[1:], r.buf)
	r.active, r.buf = false, r.buf[:0]
	return out, nil
}
