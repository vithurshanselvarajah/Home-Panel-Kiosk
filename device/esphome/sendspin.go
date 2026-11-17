package esphome

type Sendspin struct {
	clientID string

	pairingToken string

	enabled bool
}

func NewSendspin(clientID, pairingToken string) *Sendspin {
	return &Sendspin{clientID: clientID, pairingToken: pairingToken, enabled: true}
}

func (s *Sendspin) ClientID() string {
	if s == nil {
		return ""
	}
	return s.clientID
}

func (s *Sendspin) PairingToken() string {
	if s == nil {
		return ""
	}
	return s.pairingToken
}

func (s *Sendspin) Enabled() bool {
	return s != nil && s.enabled
}
