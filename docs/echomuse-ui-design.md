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
Immich Kiosk, Dashboard. EchoMuse joins it as a fourth row, in exactly the same
layout: a coloured dot, a label, and the value on the right.

```
  ●  MQTT              Online
  ●  Immich Kiosk      Online
  ●  Dashboard         Online
  ●  EchoMuse          Connected
```

The value is the **connection** to the controller, not the listening mode. The
SDK's phases are about what the device is doing; what a person wants to know
from the drawer is whether this thing is hooked up at all.

| Connection | Value | LED |
|---|---|---|
| `connected` | Connected | green |
| `unpaired` | Unpaired | amber |
| `disabled` | Disabled | red |
| `refused` | Refused | red |
| `disconnected` | Disconnected | red |
| `unknown` | Unknown | grey |

The reason is the tooltip on the value, and the second line in the settings
tab. It is the difference between "Unpaired" and "Unpaired — the controller
has not admitted this device".

### 2. The voice indicator

A row of dots along the bottom of the screen, the way a voice assistant shows
it is listening. It is **not** a row of LEDs that sits there all the time — it
appears only while the device is doing something, and disappears otherwise.

| State | Indicator |
|---|---|
| Listening | Seven dots, sized by the microphone level, a wave travelling across them |
| Speaking | Seven dots, pulsing along in sequence |
| Thinking | Nothing — the device is waiting, the microphone is not the story |
| Everything else | Nothing |

The user asked for it to "match the audio being received from mic". The SDK
gives us a peak percentage, which is a level and not a waveform. That is the
honest limit of what the SDK exposes today.

**What we can do with a level:** size each dot by the level, with a short
delay between dots so the row reads as a wave travelling rather than
everything jumping at once.

**What we cannot do** without more from the SDK: draw the actual waveform
shape, because only the peak is exposed. Getting a real waveform would need
either a block of PCM samples over IPC, or a small spectrum (a few bands).

**Why dots and not a bar chart.** A row of bars reads as a measurement; a row
of dots reads as a voice assistant. The second is what this is.

**Placement:** bottom-centre of the view, in the *overlay* web contents (the
one that already hosts the drawer and keyboard) rather than injected into the
Home Assistant page. That way it survives HA re-renders and needs no CSS
injection into a page we do not control. It lifts above the on-screen keyboard
when that is up.

It must never show when the device is not actually listening or speaking.
Showing an indicator that is not measuring anything is the same class of lie as
reporting a state that is not true.

### 3. Settings

A separate **EchoMuse** tab in the settings panel, alongside the existing
device setup form. The settings panel is already full screen and scrolls, so
the tab becomes a second view within it.

The tab opens with the **connection status**, so the same information is in
the drawer and in the settings:

```
  ●  Connected
     the controller admitted this device
```

Then the settings, which are deliberately short. The device can do a lot, but
a person should not have to configure it to use it.

**Device**
| Field | Type | Notes |
|---|---|---|
| Board | text | The controller names the model in HA from it |

**Wake word**
| Field | Type | Notes |
|---|---|---|
| Wake word mode | select | `on` (device scores), `off` (controller scores — **streams audio**), `shadow` (developer diagnostic, not a user choice) |
| Wake threshold | number | 0–1, float |
| Wake sound | select | On / Off |
| Wake word enabled | toggle | `false` is "No wake word" — a distinct meaning from "unset" |
| Barge-in | toggle | |

**Microphone**
| Field | Type | Notes |
|---|---|---|
| Microphone gain | number | dB |
| Startup volume | number | 0–100 |
| VAD threshold | number | 0–1 |
| VAD speech time | number | ms |
| VAD silence time | number | ms |

**Speaker**
| Field | Type | Notes |
|---|---|---|
| Output volume | number | 0–100 |
| Ducking | number | dB, how far the music ducks under a response |
| Mute switch | toggle | Whether the device has its own mute switch |
| Volume button sound | toggle | |

**What was left out, and why.**

- **Capabilities.** These are what the device announces to the controller, and
  they are all true of this device. Exposing them in a settings page invites
  someone to tick a box the hardware cannot honour — announcing a capability
  and not implementing it produces a device that scores, stays silent and
  looks broken.
- **Controller URL, TLS port, Device ID.** The controller is found by mDNS and
  the device identity is derived from the machine. These are deployment
  concerns, not something a person on a wall display should be editing.
- **Base OS.** It is a constant.

## What the SDK still needs to give us

| Need | Today | Required |
|---|---|---|
| Mic level for the indicator | `peakLevel()` exists in the emulator but is not exposed by the SDK | A level the app can read, e.g. `Device.OnMicLevel(func(int))` |
| Actual waveform | not available | A block of samples or a small spectrum, if the dots are to become a real waveform |
| Connection state | `state.Snapshot` has `LinkUp` and `Approved`; the phases carry the rest | The app needs to map those onto connected / unpaired / disabled |
| Ring colours | `display.Tracker` has them | Needs to reach the UI, probably as part of the state snapshot |
| Config push | `protocol.Config` decodes it | The app needs to read the pushed config and apply it, and to send config changes back |

The level is the one that matters. The `display` package already exists for
the ring, so the natural shape is for the SDK to publish a comparable
`audio.Tracker` that derives a level from the capture path — the same job
`display.Tracker` does for the ring.

## Design decisions to argue about

1. **A level, not a waveform.** The SDK only exposes a peak. The indicator
   sizes dots by that level. If a real waveform is wanted, the SDK needs to
   expose samples.

2. **Dots, not a ring.** The ring is the Echo's language and this device has
   no ring. A row of dots reads as a voice assistant, which is what this is.

3. **The indicator only appears when the device is doing something.** It is
   not a permanent row of status LEDs.

4. **Unknown is drawn as unknown.** Never as the opposite. A grey "Unknown"
   is correct; a green "Connected" when the device has not said is a lie.

5. **The settings page is short on purpose.** Board, wake word, microphone
   and speaker. Everything else is either constant or a deployment concern.

## Prototype

This branch carries a working prototype:

- The drawer shows an EchoMuse row in the same layout as the other services,
  with the controller connection as the value.
- A canvas indicator draws over the view — dots sized by the microphone level
  while listening, pulsing along while speaking — driven by a **mock** level
  source, so the motion can be judged without the SDK.
- The settings panel has an EchoMuse tab with the connection status and the
  short settings list.

The mock data lives in one clearly marked place so it can be swapped for the
real SDK when it lands.
