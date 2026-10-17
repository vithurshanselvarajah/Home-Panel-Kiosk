# Home Panel Kiosk

An independent Electron kiosk for a Home Assistant touch display. Immich photos are shown by opening an **Immich Kiosk** URL hosted on a separate server; this application does not implement photo selection or slideshow generation.

## Current build

- Fullscreen Home Assistant dashboard surface with a swipe-in controls drawer. Swipe left from the right edge to open it and swipe right or tap × to close it.
- Optional automatic touch keyboard appears when an editable Home Assistant field receives focus. Enable or disable it in Settings.
- Configurable dashboard and external Immich Kiosk URLs.
- Idle transition from dashboard to slideshow, then optional display power-off.
- A Home Assistant camera dropdown populated automatically from available `camera.*` entities. Selecting a camera opens its Home Assistant more-info view.
- MQTT command topic and Home Assistant MQTT discovery for supported view, screen, brightness, notification, URL, and camera controls. Unsupported device controls are omitted; legacy keyboard and duplicate exit entities are cleared.
- Drawer indicators for MQTT connection, Immich Kiosk page load, and Home Assistant dashboard page load.
- Local notification overlay and settings UI.
- Display power commands for `wlopm`, `kscreen-doctor`, and `xset`.

## MQTT command contract

Configure a broker and topic prefix in the settings panel. Publish JSON commands to `<prefix>/command`:

```json
{"action":"wake"}
{"action":"dashboard"}
{"action":"slideshow"}
{"action":"screen_off"}
{"action":"brightness","value":45}
{"action":"camera","name":"camera.front_door"}
{"action":"open_url","url":"http://192.168.1.20:8080/stream"}
{"action":"notification","title":"Delivery","message":"Package at the door","duration":12000,"priority":"high"}
{"action":"dismiss_notification"}
{"action":"set_mode","mode":"dashboard"}
```

Camera options come from the Home Assistant session already open on the display; no camera URL list or separate API token is needed. Automations can send a camera entity ID in the `name` field.

The app publishes JSON state to `<prefix>/state` and online status to `<prefix>/availability`. Discovery is published with retained configuration messages and refreshed when Home Assistant announces that its MQTT integration is online. The brightness number uses `<prefix>/brightness` for its retained state.

Home Assistant MQTT discovery entities use the default `homeassistant` discovery prefix. Configure the broker and prefix in the app before expecting entities to appear. The configured topic prefix only changes the app's command and state topics; it does not change Home Assistant's discovery prefix.

The view selector appears only when a slideshow URL is configured. Screen power controls require a supported display command (`wlopm`, `kscreen-doctor`, or `xset`), and brightness requires a writable Linux backlight interface. Enter an HTTP(S) URL in **Open display URL** to show it on the display. The MQTT text value clears after use and the display returns to the dashboard after 30 seconds. Home Assistant camera entities appear in one dropdown automatically; selecting an option opens that entity’s native more-info camera view in full screen. Camera views return to the dashboard automatically after 30 seconds. A **Return to Home** button appears over custom URLs and camera views; Home Assistant automations can also send `{"action":"dashboard"}`.

## Install on Debian or Raspberry Pi OS

From a terminal in a graphical desktop session, run the installer directly from GitHub:

```sh
bash <(wget -qO- https://raw.githubusercontent.com/vithurshanselvarajah/pitouch/main/install.sh)
```

It downloads the latest stable release for 64-bit Raspberry Pi OS (`arm64`) or Intel/AMD Linux (`amd64`). It installs the package, creates and starts a systemd user service, and saves the current `DISPLAY` and `WAYLAND_DISPLAY` values in that service (defaulting `DISPLAY` to `:0` when needed). The first launch opens the app settings so you can enter the Home Assistant dashboard URL and optional Immich Kiosk and MQTT settings. Set `PITOUCH_REPOSITORY` to override the default repository.

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

Settings live in Electron's per-user application data directory. Use `npm run make:linux -- --arch=arm64` or `--arch=x64` to create a Linux package.

## Suggested next improvements

- Add per-command acknowledgements and error reporting to MQTT so automations can confirm that a URL or camera opened.
- Support a configurable hardware wake source and test display power support against the exact kiosk hardware.
- Add multiple dashboard profiles and optional allowlists for URLs accepted over MQTT.

## Notes

- Screen power support depends on the Linux desktop session, permissions, and display hardware. A successful command is not proof that every monitor physically powered down.
- A touch display may not deliver input while powered off. Configure a Home Assistant wake automation, motion sensor, or other external wake source for that case.
- MQTT is optional. Dashboard and slideshow views can run without it.
