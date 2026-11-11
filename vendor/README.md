# The device program

The device program is a sidecar: a separate process that owns the
connection to Home Assistant, the microphone, the speaker and the wake
word. This application only renders what it reports.

The binaries are built ahead of time and kept here, one per architecture,
so the package is self-contained and needs nothing from the network at
install time.

## What is here

| File | What it is |
|---|---|
| `home-panel-device-linux-arm64` | The device for arm64 |
| `home-panel-device-linux-amd64` | The device for amd64 |
| `arm64/libonnxruntime.so.1` | The ONNX Runtime for arm64 |
| `amd64/libonnxruntime.so.1` | The ONNX Runtime for amd64 |

They are dynamically linked against the ONNX Runtime, which is copied
beside them in the package. Everything else is static.

The runtime is in a directory of its own for each architecture because the
loader finds a library by its soname — `libonnxruntime.so.1` — and the two
cannot both have that name in one place.

## Rebuilding

The device is built from this repository, from the `device/` directory.
Everything it needs is in this repository; there is no external checkout
to point at.

```sh
npm run build:device
```

The build fetches the ONNX Runtime for each architecture and links it in,
so a device that has the binary has the wake word: there is nothing to
install on the device and nothing that can be missing. The runtime is
cached in `~/.cache/home-panel/onnxruntime`, so it is fetched once.

CGO is on, because the ONNX Runtime is a C library. The arm64 build uses
the `aarch64-linux-gnu-gcc` cross-compiler, so it needs one:

```sh
sudo apt-get install gcc-aarch64-linux-gnu
```

The audio reaches ALSA through
[purego](https://github.com/ebitengine/purego), which loads
`libasound.so.2` at run time rather than linking against it. That means
the sound system needs no cross-compiled library — only the runtime does —
and the panel still starts on a machine with no sound card.

## The wake word models

The models are not here. They are a few megabytes, they change far less
often than the application, and a model is chosen by the person who
installs it rather than by the build.

They go in `/usr/share/home-panel/wakeword`:

| File | Where it comes from |
|---|---|
| `melspectrogram.onnx` | [openWakeWord](https://github.com/dscripka/openWakeWord) |
| `embedding_model.onnx` | [openWakeWord](https://github.com/dscripka/openWakeWord) |
| `hey_jarvis_v0.1.onnx` | [openWakeWord](https://github.com/dscripka/openWakeWord) — or any other wake word |

The directory can be changed with `HOME_PANEL_WAKEWORD_DIR`.

Without the models the panel still works: it is a screen with some
switches, and Home Assistant can ask it to speak. With them it is a voice
satellite that hears the wake word itself, and nothing is sent until it
fires.
