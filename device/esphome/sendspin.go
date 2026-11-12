package esphome

// Sendspin is the music player, as the device's capabilities describe it.
//
// The device reports two things about it: the client's identity, and the
// fact that it hands out a pairing token. A music server — Music
// Assistant, for one — pairs with the player over that token, and the
// server learns which player it is from the identity.
//
// That is what stops the same speaker appearing twice. Without the
// identity, a music server has no way of knowing that the player it
// found is the player Home Assistant already knows about, and it makes
// one of its own.
type Sendspin struct {
	// clientID is the player's identity: the base64url of its public
	// key.
	clientID string
	// pairingToken is the token a music server pairs with.
	pairingToken string
	// enabled says the player is running.
	enabled bool
}

// NewSendspin describes a music player.
func NewSendspin(clientID, pairingToken string) *Sendspin {
	return &Sendspin{clientID: clientID, pairingToken: pairingToken, enabled: true}
}

// ClientID returns the player's identity.
func (s *Sendspin) ClientID() string {
	if s == nil {
		return ""
	}
	return s.clientID
}

// PairingToken returns the token a music server pairs with.
func (s *Sendspin) PairingToken() string {
	if s == nil {
		return ""
	}
	return s.pairingToken
}

// Enabled says whether the player is running.
//
// It is false when the panel has no music, and the flag says so rather
// than the device pretending to be a player with no identity.
func (s *Sendspin) Enabled() bool {
	return s != nil && s.enabled
}
