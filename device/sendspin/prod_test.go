package sendspin

import "github.com/vithurshanselvarajah/Home-Panel-Kiosk/audio/speaker"

// The placement is a background sound: it is stood down when a reply takes
// the speaker, and carried on afterwards.
var _ speaker.Producer = (*placement)(nil)
