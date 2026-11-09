# EchoMuse sidecar

The EchoMuse link lives in a sidecar: a separate process that owns the
connection to the EchoMuse controller, the microphone, the speaker and the
wake word. This application only renders what the sidecar reports.

The binaries here are built from the SDK and shipped in the package, so an
install needs nothing from the network.

## What is here

| File | What it is |
|---|---|
| `echomuse-sidecar-linux-arm64` | The sidecar for arm64 |
| `echomuse-sidecar-linux-amd64` | The sidecar for amd64 |
| `ECHOMUSE-NOTICE.md` | The third-party notices for the libraries it contains |

They are statically linked, so they run on a device with nothing else
installed.

## Rebuilding

The sidecar is built from the SDK. The SDK is a separate repository, and the
revision it is built from is a decision, not an accident — the state the
sidecar reports has to match what this application expects.

```sh
git clone https://github.com/wilbowes/EchoMuse-SDK.git
cd EchoMuse-SDK/clients/go/tools/sidecar

for arch in arm64 amd64; do
  CGO_ENABLED=0 GOOS=linux GOARCH=$arch \
    go build -trimpath -ldflags="-s -w" \
    -o "/path/to/Home-Panel-Kiosk/vendor/echomuse-sidecar-linux-$arch" .
done
```

`CGO_ENABLED=0` is deliberate: the sidecar is pure Go, so it cross compiles
without a C toolchain and the binary has no shared library dependencies.

The ONNX Runtime, which the wake word backend needs, is loaded at runtime
rather than linked, so the binary builds and boots without it. Installing it
later needs no rebuild.

## How the two talk

The sidecar writes its state to a JSON file and reads its configuration from
another. Both are in the application's data directory.

- `echomuse-state.json` — what the device is doing. Written atomically, so a
  reader never sees half of it.
- `echomuse-config.json` — what the device asks the controller for.

The application's `src/echomuse.js` reads the state file and pushes it to the
interface.

## The licence

The sidecar contains BSD-licensed Go libraries. `ECHOMUSE-NOTICE.md` lists
them and is shipped in the package beside the binary.
