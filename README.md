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
- Entities for the supported view, screen, brightness, notification, and URL controls. Unsupported device controls are omitted.
- Drawer indicators for the link to Home Assistant, Immich Kiosk page load, and Home Assistant dashboard page load.
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

### The entities

| Entity | Kind | What it does |
|---|---|---|
| Screen | switch | Turns the screen off and on |
| Wake screen | button | Wakes the screen |
| Turn screen off | button | Turns the screen off |
| Brightness | number | The screen brightness, 0 to 100 |
| View | select | `dashboard` or `slideshow` |
| Open slideshow | button | Shows the slideshow |
| Return to Home | button | Shows the dashboard |
| Display mode | select | `fullscreen` or `window` |
| Send notification | text | Shows a notification on the panel |
| Open display URL | text | Shows an address on the panel |
| Exit | button | Shuts the app down |
| Panel state | text sensor | What the panel is doing, as JSON |

The view selector appears only when a slideshow URL is configured. Screen power controls require a supported display command (`wlopm`, `kscreen-doctor`, or `xset`); the installer adds `wlopm` when it is available from the configured apt sources. Brightness requires a writable Linux backlight interface.

### The command contract

Home Assistant acts on the panel through the entities above, so an automation uses the ordinary services:

```yaml
action: switch.turn_off
target:
  entity_id: switch.home_panel_screen
```

The panel's own interface sends the same commands to itself, and they are the actions the MQTT contract used:

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
| `PITOUCH_DEVICE_NAME` | Device name shown in Home Assistant (default `Home Panel`). |
| `PITOUCH_ESPHOME_ENCRYPTION` | `true` / `false` — encrypt the connection to Home Assistant (default `false`). |
| `PITOUCH_ESPHOME_KEY` | The encryption key. Blank means the application makes one when the connection is turned to encrypted. The same key is given to Home Assistant when the device is added. |
| `PITOUCH_SKIP_SETUP` | Set to `1` to skip install-time setup entirely; the app will open its settings UI on first launch. |
| `PITOUCH_SKIP_SERVICE` | Set to `1` to skip installing, enabling, or starting the systemd user service. |
| `PITOUCH_INSTALL_WLOPM` | Set to `0` to skip installing the optional Wayland display power helper. |

Example fully unattended install:

```sh
PITOUCH_DASHBOARD_URL="https://homeassistant.example.com" \
PITOUCH_SLIDESHOW_URL="https://immich-kiosk.example.com" \
PITOUCH_DEVICE_NAME="Kitchen Display" \
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

### Running against a mock controller

The EchoMuse device needs a controller to talk to. For development there is
one: a mock controller in the SDK that speaks enough of the protocol to
register a device, approve it and hold a link.

```sh
npm run dev
```

That builds the mock controller from the SDK, starts it, and starts the app
pointed at it. The EchoMuse row in the drawer goes green and the settings tab
shows the device as connected.

The SDK checkout is expected at `~/Documents/GitHub/EchoMuse-SDK-upstream`.
Set `ECHOMUSE_SDK` to point somewhere else:

```sh
ECHOMUSE_SDK=/path/to/EchoMuse-SDK npm run dev
```

`npm run dev -- --pending` starts the controller in pending mode, so the
device registers but is not admitted — which is what an unapproved device
looks like.

### Running against a real controller

```sh
npm run dev
```

The device finds the controller by mDNS, which is what a device on the
network does. It asks to be admitted, and the request appears in the
controller's dashboard where an administrator approves it.

To name the controller instead:

```sh
npm run dev -- --controller ws://192.168.1.132:8767
```

A device that has never been seen arrives as **pending** and waits in a
list. Asking to pair puts the request in front of an administrator
instead, which is what a person setting the device up wants. The **Ask the
controller to admit this device** setting turns that off, and the device
then waits as an ordinary pending device.

### Running the packaged build

```sh
npm run packaged
```

This builds the app the way the .deb does and runs that build, so the
device program is found beside the executable the way it is on a device, rather
than in the repository's vendor directory. The build is made if it is not
there, or if the source is newer than it.

The packaged build keeps its settings in the same place a device does, so
a packaged run and a development run see the same configuration.

To point it at a controller without the dev script:

```sh
ECHOMUSE_CONTROLLER_URL=ws://192.168.1.132:8767 ECHOMUSE_ENABLED=1 npm start
```

Both environment variables are read at start-up and neither is written to
the settings file, so a development run never changes how the device is set
up. `ECHOMUSE_ENABLED=0` turns the link off for the run.

The EchoMuse link is **off by default**. A device that has just been
installed has never been admitted, and asking the controller to admit a
device nobody has configured yet is not helpful — so the settings page is
where it is turned on.

### The audio devices

The **Microphone** and **Speaker** pickers in the EchoMuse settings list the
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

### The EchoMuse device program

The EchoMuse link lives in a separate program: it owns the connection to the
controller, the microphone and the speaker, and it reports what the device
is doing. The application renders that.

It is written in Go, in two modules:

| Module | What it is |
|---|---|
| `audio/` | The ALSA backend and the device listing. |
| `device/` | The device program itself. |

The audio reaches ALSA through [purego](https://github.com/ebitengine/purego),
which loads `libasound.so.2` at run time rather than linking against it.
That means the binaries cross compile for arm64 without a C toolchain, and
they still start on a machine with no sound card.

The device needs the EchoMuse SDK, which is developed in a checkout rather
than published. Set `ECHOMUSE_SDK` to point somewhere else:

```sh
ECHOMUSE_SDK=/path/to/EchoMuse-SDK npm run build:device
```

`npm run build:device` writes the binaries into `vendor/`, one per
architecture, and the packaging step copies them into the package.

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
