# EchoMuse device

The EchoMuse device is a sidecar: a separate process that owns the
connection to the EchoMuse controller, the microphone and the speaker. This
application only renders what it reports.

The binaries are built ahead of time and kept here, one per architecture,
so the package is self-contained and needs nothing from the network at
install time.

## What is here

| File | What it is |
|---|---|
| `echomuse-device-linux-arm64` | The device for arm64 |
| `echomuse-device-linux-amd64` | The device for amd64 |
| `ECHOMUSE-NOTICE.md` | The third-party notices for the libraries it contains |

They are statically linked, so they run on a device with nothing else
installed.

## Rebuilding

The device is built from this repository, from the `device/` directory, and
it needs the EchoMuse SDK, which is developed in a checkout rather than
published.

```sh
npm run build:device
```

Set `ECHOMUSE_SDK` if the SDK is not at
`~/Documents/GitHub/EchoMuse-SDK-upstream`.

The build is pure Go: `CGO_ENABLED=0` is deliberate. The audio reaches
ALSA through [purego](https://github.com/ebitengine/purego), which loads
`libasound.so.2` at run time rather than linking against it. That means
the binaries cross compile for arm64 without a C toolchain, and they still
start on a machine with no sound card.

The ONNX Runtime, which the wake word backend needs, is loaded at run time
rather than linked as well, so the binary builds and boots without it.
Installing it later needs no rebuild.

## How the two talk

The device writes its state to a JSON file and reads its configuration from
another. Both are in the application's data directory.

- `echomuse-state.json` — what the device is doing. Written atomically, so a
  reader never sees half of it.
- `echomuse-config.json` — what the device asks the controller for.

The application's `src/echomuse.js` reads the state file and pushes it to
the interface.

## The licence

The device contains BSD-licensed Go libraries. `ECHOMUSE-NOTICE.md` lists
them and is shipped in the package beside the binary.
