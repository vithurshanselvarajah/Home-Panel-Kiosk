module github.com/vithurshanselvarajah/Home-Panel-Kiosk/audio

go 1.27.0

// The wake word engine is vendored, not fetched: it is the one that runs the
// model, and a version of it that changed under the panel would change what
// the panel hears.
replace github.com/zserge/microwakeword => ./third_party/microwakeword

require (
	github.com/ygelfand/go-esphome-device v0.0.20
	github.com/zserge/microwakeword v0.0.0-20260330234603-bfaf3840114e
	golang.org/x/text v0.44.0
)

require (
	github.com/flynn/noise v1.1.0 // indirect
	github.com/google/flatbuffers v25.12.19+incompatible // indirect
	golang.org/x/crypto v0.54.0 // indirect
	golang.org/x/sys v0.47.0 // indirect
	google.golang.org/protobuf v1.36.11 // indirect
)
