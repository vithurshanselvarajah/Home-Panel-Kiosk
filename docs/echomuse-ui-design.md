# EchoMuse UI design

A proposal for showing EchoMuse state on the Home Panel kiosk. Nothing here is
wired to the SDK yet — it is the shape of the interface, so the design can be
argued about before any protocol code is written.

## What the SDK gives us

The Go SDK already answers the questions this UI needs to ask. Nothing has to
be derived from raw protocol messages.

### State (`echomuse/state`)

`Snapshot` is the whole observable state of the device at one instant:

| Field | Meaning | Why the UI wants it |
|---|---|---|
| `Phase` | `unknown`, `idle`, `listening`, `thinking`, `speaking`, `muted`, `pending`, `refused`, `disconnected`, `ready` | The headline state — one word in the drawer, one word on the wave |
| `PhaseReason` | Why, in words a person can read | The difference between "not ready" and "refused by the controller" |
| `Listen` | `unknown`, `local`, `stream`, `degraded` | **The privacy question.** Is audio leaving the device? |
| `ListenReason` | Why degraded or unknown | "No local wake word model" is actionable; "unknown" is not |
| `Muted` | The device's own mute switch | A hard mute must never look like idle |
| `Listening` | The microphone is open | Drives the wave animation |
| `Thinking` | The controller is waiting for Home Assistant | Distinct from listening — the user has spoken, we are waiting |
| `Speaking` | A response is playing | Distinct again |
| `Volume` | Last reported volume | Display only |
| `LinkUp` | Control plane connected | The "is it even talking to the controller" light |
| `Approved` | The controller admitted the device | Distinguishes pending from refused |
| `Features` | Capabilities the controller announced | Informational |
| `LastError` | Most recent error | The error line |

Two rules from the SDK matter for the UI:

- **Every state that is not known is reported as unknown**, never as its
  opposite. A device that has just connected is not muted. The UI must not
  paint "muted" for `unknown`.
- **`ListenMode.Streams()` is the one question a privacy statement has to
  answer.** It is true only for `stream`. The UI must never imply streaming
  when the device is scoring locally.

### The ring (`echomuse/display`)

`Tracker.View()` gives a `Pattern` (`off`, `solid`, `spin`, `rotate`, `pulse`,
`meter`), a `Palette` of colours, and a `Listening` flag.

The controller renders every animation frame and sends the result, so a
device paints exactly what an Echo paints. The emulator's browser UI already
does this — twelve LEDs on a circle, one every 30 degrees, painted from the
palette.

### Audio

The SDK does not capture or resample audio; the application supplies that.
What it gives the UI is a **peak level**: the loudest sample in the last block
as a percentage of full scale.

That is enough for the wave. The emulator's `peakLevel()` walks the signed
16-bit little-endian samples and takes the maximum absolute value.

> The kiosk will not have an Echo's LED ring, so the ring is not literally
> reproducible here. What *is* worth copying is the vocabulary and the
> vocabulary is in `display.Pattern` and `state.Phase`.

## What we show

### 1. Status in the drawer

The drawer already has a service status block with three LED rows — MQTT,
Immich Kiosk, Dashboard. EchoMuse joins it as a fourth row.

```
  ●  MQTT              Online
  ●  Immich Kiosk      Online
  ●  Dashboard         Online
  ●  EchoMuse          Ready
```

The LED colour follows the existing scale (`online` / `connecting` /
`offline`), but the label is the *phase*, not a generic status:

| Phase | Label | LED |
|---|---|---|
| `ready` | Ready | green |
| `idle` | Idle | green |
| `listening` | Listening | green, pulsing |
| `thinking` | Thinking | amber |
| `speaking` | Speaking | amber |
| `pending` | Awaiting approval | amber |
| `refused` | Refused | red |
| `disconnected` | Disconnected | red |
| `muted` | Muted | red |
| `unknown` | Unknown | grey |

`PhaseReason` is the second line, in the muted colour. It is the difference
between "Refused" and "Refused — the controller rejected this device's
credentials".

### 2. The privacy badge

This is the part that matters most and it should not be buried.

The SDK's rule is that the client's listening mode is a privacy promise. The
drawer shows it as a small badge next to the EchoMuse row:

```
  ●  EchoMuse   Ready   [ local ]
```

| `Listen` | Badge | Meaning |
|---|---|---|
| `local` | `local` (green) | The device scores its own wake word. **No audio leaves until it fires.** |
| `stream` | `streaming` (amber) | Audio is streaming to the controller continuously. |
| `degraded` | `no wake word` (red) | The device cannot run a wake word and is button-only. |
| `unknown` | `unknown` (grey) | The device has not said. |

The badge reads `streaming` in amber rather than hiding it, because the SDK
is explicit that streaming "does not meet this repository's private-listening
requirement". If the kiosk ever runs in `stream` mode, the user should be able
to see that at a glance.

### 3. The voice wave

A wave drawn over the Home Assistant UI, showing the level the microphone is
hearing.

The user asked for it to "match the audio being received from mic". The SDK
gives us a peak percentage, which is a level and not a waveform. That is the
honest limit of what the SDK exposes today.

**What we can do with a level:**

- A vertical bar that grows and shrinks with the level.
- A radial ring that pulses, with the radius driven by the level.

**What we cannot do** without more from the SDK: draw the actual waveform
shape, because only the peak is exposed. Getting a real waveform would need
either a block of PCM samples over IPC, or a small spectrum (a few bands).

**Proposed compromise for now:** a **radial waveform** built from the peak
level, rendered on a `<canvas>`, positioned over the HA view.

The design: a horizontal band of bars across the bottom of the screen, each
bar's height driven by the current level, with a history so the bars
smoothly fall away rather than jitter. A short decaying history per bar is
what makes it read as a wave rather than a bar chart.

```
        ▁▂▃▅▇▅▃▂▁▂▃▄▆█▆▄▃▂▁▁▂▃▄▅▆▇▆▄▃▂▁
```

Why a band at the bottom rather than a ring:

- The kiosk already uses the bottom of the screen for the on-screen keyboard.
  The wave needs to get out of the way when the keyboard is up.
- A ring is the Echo's language, but this device has no ring, so a ring here
  would be decoration pretending to be a hardware feature.
- A band is honest: it says "this is a level meter", not "this is your
  Echo's light ring".

**Placement:** bottom-centre, above the keyboard when it is up, otherwise
bottom-centre of the view. It should be an overlay in the *overlay* web
contents (the one that already hosts the drawer and keyboard), not injected
into the HA page. That way it survives HA re-renders and needs no CSS
injection into a page we do not control.

**When it shows:**

| Condition | Wave |
|---|---|
| `Listening` is true | Visible, driven by the level |
| `Phase` is `thinking` or `speaking` | Visible but flat/dim — the mic is not the story any more |
| `Muted` | Hidden, or shown as a flat muted line |
| `Listen` is `degraded` | Hidden — there is no microphone to show |
| Link down | Hidden |

It must never show when the device is not actually listening. Showing a wave
that is not measuring anything is the same class of lie as reporting a state
that is not true.

### 4. Settings

A separate **EchoMuse** tab in the settings panel, alongside the existing
device setup form. The settings panel is already full screen and scrolls, so
the tab becomes a second view within it.

Fields, drawn from the SDK's `Options` and `protocol.Config`:

**Connection**
| Field | Type | Notes |
|---|---|---|
| Controller URL | url | Empty means discover by mDNS. Placeholder `ws://192.168.1.10:8767` |
| Controller TLS port | number | Only for a controller that offers an encrypted port |
| Device ID | text | **Must be stable.** The controller keys the device's row, config and credentials on it |

**Identity**
| Field | Type | Notes |
|---|---|---|
| Board | text | The controller names the model in HA from it |
| Base OS | select | The controller uses it to pick the right profile |

**Listening**
| Field | Type | Notes |
|---|---|---|
| Wake word mode | select | `on` (device scores), `off` (controller scores — **streams audio**), `shadow` (developer diagnostic, not a user choice) |
| Wake word enabled | toggle | `false` is "No wake word" — a distinct meaning from "unset" |
| Wake threshold | number | 0–1, float |
| Barge-in | toggle | |
| Mute button | toggle | Whether the device's own mute switch exists |

**Audio**
| Field | Type | Notes |
|---|---|---|
| Microphone gain | number | dB |
| VAD threshold | number | |
| VAD speech time | number | ms |
| VAD silence time | number | ms |
| Startup volume | number | 0–100 |
| Ducking | number | dB, how far the music ducks under a response |

**Ring / display**
| Field | Type | Notes |
|---|---|---|
| LED scene | text | |
| Listen colour | colour | |
| Think colour | colour | |

**Capabilities**

A multi-select of the capability strings the SDK announces. These must all be
true of the device — announcing one and not implementing it produces a device
that scores, stays silent and looks broken.

`mic`, `speaker`, `leds`, `led_anim`, `buttons`, `oww_shadow`, `oww_trigger`,
`button_hold`, `audio_mix`, `oww_local_only`, `aec_hw_ref`, `output_chain`,
`wake_cue`, `volume_cue`, `wake_mic`, `remote_volume_arc`, `response_level`,
`stock_sound`, `ambient_light`, `sendspin`, `wake_word_off`, `pairing`,
`ble_connect`.

For the kiosk the sensible set is `mic`, `speaker`, `buttons`, `pairing` and
possibly `oww_local_only`.

### 5. Status detail

A read-only "EchoMuse" readout, in the same dark LCD style the emulator uses:

```
  Link            up
  Admitted        yes
  Credential      received
  Listen          local
  Phase           idle
  Volume          42
  Features        mic, speaker, buttons
```

This mirrors the emulator's LCD block, which is the same product.

## What the SDK still needs to give us

| Need | Today | Required |
|---|---|---|
| Mic level for the wave | `peakLevel()` exists in the emulator but is not exposed by the SDK | A level the app can read, e.g. `Device.OnMicLevel(func(int))` |
| Actual waveform | not available | A block of samples or a small spectrum, if the band is to be a real waveform |
| Ring colours | `display.Tracker` has them | Needs to reach the UI, probably as part of the state snapshot |
| Config push | `protocol.Config` decodes it | The app needs to read the pushed config and apply it, and to send config changes back |

The level is the one that matters. The `display` package already exists for
the ring, so the natural shape is for the SDK to publish a comparable
`audio.Tracker` that derives a level from the capture path — the same job
`display.Tracker` does for the ring.

## Design decisions to argue about

1. **A level, not a waveform.** The SDK only exposes a peak. The wave shows
   a level that decays over time. If a real waveform is wanted, the SDK needs
   to expose samples.

2. **A band, not a ring.** The ring is the Echo's language and this device
   has no ring. A bottom band is honest and does not fight the on-screen
   keyboard for space.

3. **The privacy badge is always visible.** `local` / `streaming` /
   `no wake word` sits next to the EchoMuse row. It is the single most
   important thing this UI says.

4. **Unknown is drawn as unknown.** Never as the opposite. A grey "Unknown"
   is correct; a green "Ready" when the device has not said is a lie.

5. **The wave only shows when actually listening.** A wave that is not
   measuring anything is exactly the lie the SDK's rules exist to prevent.

## Prototype

This branch carries a working prototype of items 1 to 3:

- The drawer shows an EchoMuse row with a status LED, a phase label and a
  privacy badge.
- A canvas wave draws over the view, driven by a **mock** level source, so the
  motion can be judged without the SDK.
- The settings panel has an EchoMuse tab with the fields above.

The mock data lives in one clearly marked place so it can be swapped for the
real SDK when it lands.
