# Home Panel Kiosk

An independent Electron kiosk for a Home Assistant touch display. Immich photos are shown by opening an **Immich Kiosk** URL hosted on a separate server; this application does not implement photo selection or slideshow generation.

## Current build

- Fullscreen Home Assistant dashboard surface with a swipe-in controls drawer. Swipe left from the right edge to open it and swipe right to close it.
- Fullscreen or window mode. The drawer has a button that switches between them, and the mode is remembered. The device can also be started in either mode with `--fullscreen` or `--window`.
- An exit button in the drawer, which shuts the app down cleanly.
- Optional automatic touch keyboard appears when an editable Home Assistant field receives focus. Enable or disable it in Settings.
- Configurable dashboard and external Immich Kiosk URLs.
- Idle transition from dashboard to slideshow, then optional display power-off.
- The panel is an ESPHome device to Home Assistant, over the ESPHome native API. It announces itself on the network and appears in Home Assistant like any other device.
- A Home Assistant camera entity uses the host's default camera across supported operating systems. It captures only when Home Assistant requests an image or stream and sends scaled JPEG frames.
- Entities for the supported view, screen, brightness, notification, and URL controls. Unsupported device controls are omitted.
- Drawer indicators for the ESPHome link, Immich Kiosk page load, and Home Assistant dashboard page load.
- Local notification overlay and settings UI.
- Display power commands for `wlopm`, `kscreen-doctor`, and `xset`.

## The Home Assistant connection

The panel talks to Home Assistant over the ESPHome native API: the protocol every ESPHome device speaks. The device program makes the connection and holds the entities, and the panel tells it what it is doing.

The panel announces itself on the network, so Home Assistant finds it on its own. The address that is announced is the one on the interface that carries the default route, which is the one Home Assistant can reach it on.

### The encryption key

**The key is optional, and it is off by default.**

Without a key the connection is made in plain text, and Home Assistant sets the device up in one step: it finds the panel, asks for its address and its port, and that is all. The traffic is readable by anything on the network, which is the whole of what a key is for.

Turn on **Encrypt the connection to Home Assistant** and a key is made for you. It is shown in the settings, because it is a setting you read and copy into Home Assistant rather than a password you type once and forget. **Make a new key** makes another one.

Copy the key into Home Assistant when the panel is added: it asks for it.

The key is kept out of the settings file and encrypted with Electron `safeStorage`, the same way a password would be.

The key also protects Sendspin pairing: the device withholds its Sendspin pairing token unless the ESPHome API has a configured encryption key. Sendspin itself always encrypts its session with Noise. The Home Assistant **Music · Allow unpaired servers** setting controls guest access; turning guest access off requires a server to pair, but does not turn encryption on or off.

### The entities

| Entity | Kind | What it does |
|---|---|---|
| Screen | switch | Turns the screen off and on |
| Brightness | number | The screen brightness, 0 to 100 |
| Volume | number | The volume of the room, 0 to 100 |
| View | select | `dashboard` or `slideshow` |
| Display mode | select (configuration) | `fullscreen` or `window` |
| Return to Home | button | Shows the dashboard |
| Exit | button | Shuts the app down |
| Drawer open | binary sensor | Whether the drawer is open |
| Keyboard visible | binary sensor | Whether the touch keyboard is up |
| Last activity | sensor | When somebody last did something |
| Version | text sensor | The version of the application |
| Camera | camera | Snapshot and live frames from the system-default camera; reports capture failures when unavailable |
| Display · Idle to slideshow | number (configuration) | Minutes before the dashboard changes to the slideshow; zero disables it |
| Display · Slideshow before screen off | number (configuration) | Minutes before the display powers off; zero disables it |
| Display · Touch keyboard | switch (configuration) | Enables the on-screen keyboard |
| Audio · Startup volume | number (configuration) | Volume applied when the audio device starts |
| Voice · Wake word / sensitivity / music ducking | switch / numbers (configuration) | Voice satellite settings |
| Music · Music Assistant | switch (configuration) | Enables the Sendspin player |
| Music · Allow unpaired servers | switch (configuration) | Allows local servers to play without pairing |

Configuration entities appear under the device's configuration section in Home Assistant. The dashboard and slideshow URLs and local microphone/speaker selection remain in setup because they are connection details or depend on locally enumerated hardware. The view selector appears only when a slideshow URL is configured. Screen power controls require a supported display command (`wlopm`, `kscreen-doctor`, or `xset`); the installer adds `wlopm` when it is available from the configured apt sources. Brightness requires a writable Linux backlight interface. Camera frames use the system default camera; the operating system must allow the installed app to access it.

There is a control for each thing the panel can do and a sensor for each thing that can be seen but not set. Nothing is offered twice: the screen switch turns the screen off and on, so there is no separate button for either.

### The command contract

Home Assistant acts on the panel through the entities above, so an automation uses the ordinary services:

```yaml
action: switch.turn_off
target:
  entity_id: switch.home_panel_screen
```

The panel's own interface sends the same commands to itself, and they are the actions it has always used:

```json
{"action":"wake"}
{"action":"dashboard"}
{"action":"slideshow"}
{"action":"screen_off"}
{"action":"brightness","value":45}
{"action":"open_url","url":"http://192.168.1.20:8080/stream"}
{"action":"notification","title":"Delivery","message":"Package at the door","duration":12000,"priority":"high"}
{"action":"dismiss_notification"}
{"action":"set_mode","mode":"dashboard"}
{"action":"display_mode","mode":"window"}
{"action":"exit"}
```

`display_mode` takes `fullscreen` or `window`. `exit` shuts the app down, so the service is left stopped until it is started again.

## Install on Debian or Raspberry Pi OS

From a terminal in a graphical desktop session, run the installer directly from GitHub:

```sh
bash <(wget -qO- https://raw.githubusercontent.com/vithurshanselvarajah/Home-Panel-Kiosk/main/install.sh)
```

It downloads the latest stable release for 64-bit Raspberry Pi OS (`arm64`) or Intel/AMD Linux (`amd64`). It installs the package, creates and starts a systemd user service, and saves the current `DISPLAY` and `WAYLAND_DISPLAY` values in that service (defaulting `DISPLAY` to `:0` when needed). The unit is stored under `${XDG_CONFIG_HOME:-$HOME/.config}/systemd/user`; the installer verifies it is active and reports its status if startup fails. On first install the script asks a few questions so the kiosk can launch preconfigured; you can also pre-fill those values via environment variables (see below) or set `PITOUCH_SKIP_SETUP=1` to fall back to the in-app settings UI. Set `PITOUCH_SKIP_SERVICE=1` to install or update the package without changing the user service. Set `PITOUCH_REPOSITORY` to override the default repository.

### Pre-configuring the install

The installer writes a `settings.json` into Electron's per-user app config directory (`${XDG_CONFIG_HOME:-$HOME/.config}/homepanel-kiosk/settings.json`) so the kiosk launches with your settings applied. Any of the following `PITOUCH_*` environment variables can be supplied — when at least one is set, the installer skips its interactive prompts:

| Variable | Description |
|----------|-------------|
| `PITOUCH_DASHBOARD_URL` | Home Assistant dashboard URL (default `http://homeassistant.local:8123`). |
| `PITOUCH_SLIDESHOW_URL` | Optional Immich Kiosk URL. Leave empty to disable the slideshow. |
| `PITOUCH_IDLE_SLIDESHOW_MINUTES` | Idle minutes before the slideshow opens (`0` disables). |
| `PITOUCH_SLIDESHOW_OFF_MINUTES` | Slideshow minutes before the screen turns off (`0` disables). |
| `PITOUCH_TOUCH_KEYBOARD` | `true` / `false` — enable the automatic touch keyboard. |
| `PITOUCH_DISPLAY_MODE` | `fullscreen` / `window` — how the device starts (default `fullscreen`). |
| `PITOUCH_ESPHOME_ENCRYPTION` | `true` / `false` — encrypt the connection to Home Assistant (default `false`). |
| `PITOUCH_ESPHOME_KEY` | The encryption key. Blank means the application makes one when the connection is turned to encrypted. The same key is given to Home Assistant when the device is added. |
| `PITOUCH_SKIP_SETUP` | Set to `1` to skip install-time setup entirely; the app will open its settings UI on first launch. |
| `PITOUCH_SKIP_SERVICE` | Set to `1` to skip installing, enabling, or starting the systemd user service. |
| `PITOUCH_INSTALL_WLOPM` | Set to `0` to skip installing the optional Wayland display power helper. |

Example fully unattended install:

```sh
PITOUCH_DASHBOARD_URL="https://homeassistant.example.com" \
PITOUCH_SLIDESHOW_URL="https://immich-kiosk.example.com" \
bash <(wget -qO- https://raw.githubusercontent.com/vithurshanselvarajah/Home-Panel-Kiosk/main/install.sh)
```

To re-run setup on an existing install, delete the file at `${XDG_CONFIG_HOME:-$HOME/.config}/homepanel-kiosk/settings.json` (or remove the `configured` key) and re-run the installer; the UI settings panel can still be used at any time to change values after installation.

To install without the script, download the `.deb` for your architecture from GitHub Releases and run `sudo apt install ./homepanel-kiosk_*.deb`. To disable automatic launch, run `systemctl --user disable --now homepanel-kiosk.service`. To update, run `bash install.sh` again.

## Security scans

GitHub Actions runs the Node.js unit tests, CodeQL against the JavaScript/TypeScript source, and an audit of the npm dependency tree on pushes and pull requests, with a weekly scheduled scan. The dependency audit fails on high or critical vulnerabilities. Run the unit tests locally with `npm test`.

## Releases

To publish a release, update the `version` in `package.json` and push a commit to `main` whose message starts with `release:`. The release workflow creates or reuses the matching `v<version>` Git tag, then builds and uploads the Linux packages to a draft GitHub release.

## Development

```sh
npm ci
npm start
```

Settings live in Electron's per-user application data directory. Use `npm run make -- --arch=arm64` or `--arch=x64` to create a Linux package.

### Running against a Home Assistant server

The panel is a voice satellite to Home Assistant, and it finds Home
Assistant the same way any ESPHome device does: by announcing itself on
the network. There is nothing to point it at.

```sh
npm run dev
```

That starts the app with its voice satellite enabled. The panel appears in
Home Assistant as an ordinary ESPHome device, with its entities and its
services, and — once the wake word models are installed — as a voice
satellite.

The wake word and its sensitivity are switched from Home Assistant on the
panel's device page.

### The audio devices

The **Microphone** and **Speaker** pickers in the voice settings list the
audio endpoints the machine has, the way the mixer names them. The list
comes from the device program, because the device program is what opens the
audio.

**System default** is the system's own choice and is what the device uses
when nothing is picked. The names are stored as the device reported them,
so a device that is unplugged keeps its name and can be picked again when
it comes back.

Under each picker is a way to check it:

- **Test microphone** opens the microphone and shows its level. The level is
  what the microphone is hearing, on a scale a quiet room still moves. The
  microphone is released when the settings page is closed.
- **Test speaker** plays a short tone through the chosen speaker, so a
  person can hear which speaker is which before they commit to it.

The microphone and the speaker have to be **different devices**. ALSA will
not open a device for capture while it is open for playback, so a device
that is its own microphone cannot also be its own speaker.

The names come from the kernel and the mixer, and ALSA opens them as
`hw:CARD=...,DEV=...` — the hardware itself, rather than the sound
server's view of it. A device that is already in use, by the sound server
or by another program, is reported as busy rather than opened, because
ALSA answers a busy device by taking the process down.

### The device program

The voice link lives in a separate program: it owns the connection to Home
Assistant, the microphone, the speaker and the wake word. The application
renders what it reports.

It is written in Go, in two modules:

| Module | What it is |
|---|---|
| `audio/` | The ALSA backend and the device listing. |
| `device/` | The device program itself. |

The audio reaches ALSA through [purego](https://github.com/ebitengine/purego),
which loads `libasound.so.2` at run time rather than linking against it.
That means the sound system needs no cross-compiled library, and the panel
still starts on a machine with no sound card.

The wake word's ONNX Runtime *is* linked, because it is a C library and
the panel is better with the wake word than without it. The build fetches
the runtime for each architecture and links it in, so a device that has the
binary has the wake word: there is nothing to install on the device and
nothing that can be missing. The runtime travels beside the binary in the
package, and the binary looks for it there.

The arm64 build needs a cross-compiler:

```sh
sudo apt-get install gcc-aarch64-linux-gnu
```

```sh
npm run build:device
```

That writes the binaries into `vendor/`, one per architecture, and the
packaging step copies them into the package.

### The wake word models

The models are not in the package. They are a few megabytes, they change
far less often than the application, and a model is chosen by the person
who installs the panel rather than by the build.

They go in `/usr/share/home-panel/wakeword`:

| File | Where it comes from |
|---|---|
| `melspectrogram.onnx` | [openWakeWord](https://github.com/dscripka/openWakeWord) |
| `embedding_model.onnx` | [openWakeWord](https://github.com/dscripka/openWakeWord) |
| `hey_jarvis_v0.1.onnx` | [openWakeWord](https://github.com/dscripka/openWakeWord) — or any other wake word |

The directory can be changed with `HOME_PANEL_WAKEWORD_DIR`.

Without the models the panel still works: it is a screen with some
switches, and Home Assistant can ask it to speak. With them it hears the
wake word itself, and nothing is sent to Home Assistant until it fires.

### The ESPHome protocol

The device program is also the device half of the ESPHome native API. It
is in `device/esphome/`, and it is the part that speaks to Home
Assistant.

The protocol file is trimmed down to the messages the kiosk needs, and
the Go types are generated from the trimmed file. The upstream file is
kept beside it, so the trim can be run again when the protocol changes:

```sh
pip3 install grpcio-tools
go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
npm run build:esphome-proto
```

The wire format is checked against the other implementation of it. Home
Assistant speaks this protocol with `aioesphomeapi`, so the types here
are compared against `aioesphomeapi`'s own generated types, message by
message, and the bytes have to be identical. A difference would be a
silent bug: the message would parse, the field would be wrong, and the
symptom would be an entity that never changes.

```sh
python3 tools/check_wire.py
go test ./device/esphome/
```

The tests drive `aioesphomeapi` itself rather than a peer written for
them, so what is checked is what Home Assistant will do. They are skipped
when Python or `aioesphomeapi` is not there, so the device still builds
without either of them.

## Suggested next improvements

- Add per-command acknowledgements and error reporting so automations can confirm that a URL opened.
- Support a configurable hardware wake source and test display power support against the exact kiosk hardware.
- Add multiple dashboard profiles and optional allowlists for the URLs the panel accepts.

## Notes

- Screen power support depends on the Linux desktop session, permissions, and display hardware. A successful command is not proof that every monitor physically powered down.
- A touch display may not deliver input while powered off. Configure a Home Assistant wake automation, motion sensor, or other external wake source for that case.
- The Home Assistant connection is optional. Dashboard and slideshow views can run without it.
