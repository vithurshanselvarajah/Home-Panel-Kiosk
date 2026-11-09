module github.com/vithurshanselvarajah/Home-Panel-Kiosk/audio

go 1.25.0

require (
	github.com/ebitengine/purego v0.9.0
	github.com/wilbowes/EchoMuse-SDK/clients/go v0.0.0
)

require (
	github.com/cenkalti/backoff v2.2.1+incompatible // indirect
	github.com/gorilla/websocket v1.5.3 // indirect
	github.com/grandcat/zeroconf v1.0.0 // indirect
	github.com/miekg/dns v1.1.27 // indirect
	golang.org/x/crypto v0.46.0 // indirect
	golang.org/x/net v0.48.0 // indirect
	golang.org/x/sys v0.41.0 // indirect
)

// The SDK is developed in a checkout rather than published, so the build
// uses the copy on this machine. The sidecar is built from the same
// checkout, so the two are always the same revision.
replace github.com/wilbowes/EchoMuse-SDK/clients/go => ../../EchoMuse-SDK-upstream/clients/go
