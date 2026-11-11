module github.com/vithurshanselvarajah/Home-Panel-Kiosk/device

go 1.25.0

require (
	github.com/flynn/noise v1.1.0
	github.com/gorilla/websocket v1.5.3
	github.com/grandcat/zeroconf v1.0.0
	github.com/mewkiz/flac v1.0.14
	github.com/vithurshanselvarajah/Home-Panel-Kiosk/audio v0.0.0
	golang.org/x/crypto v0.46.0
	google.golang.org/protobuf v1.36.11
)

require (
	github.com/cenkalti/backoff v2.2.1+incompatible // indirect
	github.com/ebitengine/purego v0.9.0 // indirect
	github.com/icza/bitio v1.1.0 // indirect
	github.com/mewkiz/pkg v0.0.0-20250417130911-3f050ff8c56d // indirect
	github.com/mewpkg/term v0.0.0-20241026122259-37a80af23985 // indirect
	github.com/miekg/dns v1.1.27 // indirect
	golang.org/x/net v0.48.0 // indirect
	golang.org/x/sys v0.41.0 // indirect
)

replace github.com/vithurshanselvarajah/Home-Panel-Kiosk/audio => ../audio
