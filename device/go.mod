module github.com/vithurshanselvarajah/Home-Panel-Kiosk/device

go 1.27.0

// The wake word engine is vendored, not fetched: it is the one that runs the
// model, and a version of it that changed under the panel would change what
// the panel hears.
replace github.com/zserge/microwakeword => ./third_party/microwakeword

require (
	github.com/flynn/noise v1.1.0
	github.com/gorilla/websocket v1.5.3
	github.com/grandcat/zeroconf v1.0.0
	github.com/google/flatbuffers v25.12.19+incompatible
	github.com/mewkiz/flac v1.0.14
	github.com/vithurshanselvarajah/Home-Panel-Kiosk/audio v0.0.0
	github.com/zserge/microwakeword v0.0.0-20260330234603-bfaf3840114e
	golang.org/x/crypto v0.46.0
	google.golang.org/protobuf v1.36.11
)

require (
	github.com/bluenviron/gortsplib/v4 v4.16.2 // indirect
	github.com/bluenviron/mediacommon/v2 v2.4.1 // indirect
	github.com/cenkalti/backoff v2.2.1+incompatible // indirect
	github.com/ebitengine/purego v0.9.0 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/icza/bitio v1.1.0 // indirect
	github.com/mewkiz/pkg v0.0.0-20250417130911-3f050ff8c56d // indirect
	github.com/mewpkg/term v0.0.0-20241026122259-37a80af23985 // indirect
	github.com/miekg/dns v1.1.27 // indirect
	github.com/pion/logging v0.2.3 // indirect
	github.com/pion/randutil v0.1.0 // indirect
	github.com/pion/rtcp v1.2.15 // indirect
	github.com/pion/rtp v1.8.21 // indirect
	github.com/pion/sdp/v3 v3.0.15 // indirect
	github.com/pion/srtp/v3 v3.0.6 // indirect
	github.com/pion/transport/v3 v3.0.7 // indirect
	golang.org/x/net v0.48.0 // indirect
	golang.org/x/sys v0.41.0 // indirect
)

replace github.com/vithurshanselvarajah/Home-Panel-Kiosk/audio => ../audio
