# Voice assistant UI design

A proposal for showing the voice assistant's state on the Home Panel kiosk.
It was written before any of it was built — it is the shape of the interface,
so the design could be argued about before any protocol code was written.

It has been built since, and it has moved on. The panel is now a voice
satellite to Home Assistant over the ESPHome native API: it hears the wake
word itself, and the audio leaves the device only for the length of a turn.
There is no controller and no broker in the middle any more, and the
settings tab is the voice tab rather than the EchoMuse one.

## What the device program gives us

The device program answers the questions this UI needs to ask. Nothing has to
be derived from raw protocol messages.

### State (`device/state`)

`Snapshot` is the whole observable state of the panel at one instant:

| Field | Meaning | Why the UI wants it |
|---|---|---|
| `Phase` | `unknown`, `idle`, `listening`, `thinking`, `speaking`, `muted`, `disconnected`, `ready` | The headline state — one word in the drawer, one word on the wave |
| `PhaseReason` | Why, in words a person can read | The difference between "not ready" and "the link is down" |
| `Listen` | `unknown`, `local`, `stream`, `degraded` | **The privacy question.** Is audio leaving the device? |
| `ListenReason` | Why degraded or unknown | "No local wake word model" is actionable; "unknown" is not |
| `Muted` | The device's own mute switch | A hard mute must never look like idle |
| `Listening` | The microphone is open | Drives the wave animation |
| `Thinking` | Home Assistant is working on the reply | Distinct from listening — the user has spoken, we are waiting |
| `Speaking` | A response is playing | Distinct again |
| `Volume` | Last reported volume | Display only |
| `LinkUp` | The link to Home Assistant is up | The "is it even talking to Home Assistant" light |
| `WakeWord` | The model the panel is listening for | Which wake word the picker shows |
| `WakeWordReady` | The model and the runtime are installed | "Not installed" is actionable |
| `WakeWordEnabled` | The wake word is switched on | The picker and the panel agree |
| `LastError` | Most recent error | The error line |

Two rules from the device program matter for the UI:

- **Every state that is not known is reported as unknown**, never as its
  opposite. A panel that has just started is not muted. The UI must not
  paint "muted" for `unknown`.
- **`ListenMode.Streams()` is the one question a privacy statement has to
  answer.** It is true only for `stream`. The UI must never imply streaming
  when the panel is scoring locally.

### The ring

There is no ring. The Echo's LED ring is an Echo's, and the panel has a
screen, so the indicator is drawn on the screen instead: a row of dots along
the bottom of the view, the way a voice assistant shows it is listening.

The dots move and scale with the microphone level, and a different animation
plays while a reply is playing. They are only on screen while the panel is
doing something.

### Audio

The device program does not capture or resample audio; the application
supplies that. What it gives the UI is a **peak level**: the loudest sample in
the last block as a percentage of full scale.

That is enough for the wave. The emulator's `peakLevel()` walks the signed
16-bit little-endian samples and takes the maximum absolute value.

## What we show

### 1. Status in the drawer

The drawer already has a service status block with three LED rows — ESPHome,
Immich Kiosk, Dashboard. The voice assistant joins it as a fourth row, in exactly the same
layout: a coloured dot, a label, and the value on the right.

```
  ●  ESPHome           Online
  ●  Immich Kiosk      Online
  ●  Dashboard         Online
  ●  Voice assistant   Connected
```

The value is the **connection** to the controller, not the listening mode. The
phases are about what the device is doing; what a person wants to know
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

The user asked for it to "match the audio being received from mic". The
gives us a peak percentage, which is a level and not a waveform. That is the
honest limit of what the device program exposes today.

**What we can do with a level:** size each dot by the level, with a short
delay between dots so the row reads as a wave travelling rather than
everything jumping at once.

**What we cannot do** without more from the device program: draw the actual waveform
shape, because only the peak is exposed. Getting a real waveform would need
either a block of PCM samples over IPC, or a small spectrum (a few bands).

**Why dots and not a bar chart.** A row of bars reads as a measurement; a row
of dots reads as a voice assistant. The second is what this is.

**The rainbow.** The dots and the label cycle through a spectrum rather than
sitting in one colour. Google colours its voice assistant the same way, and a
single flat colour does not read as "the assistant is working" nearly as well.
The two colours either side of a position are blended, so the row shows a
smooth sweep rather than seven separate colours, and the whole thing drifts
slowly rather than flickering.

Both the dots and the label glow. The label takes the colour of the middle
dot, so the text and the dots are the same moment of the same rainbow, and the
glow on each is the colour of the dot it belongs to.

**Placement:** bottom-centre of the view, in the *overlay* web contents (the
one that already hosts the drawer and keyboard) rather than injected into the
Home Assistant page. That way it survives HA re-renders and needs no CSS
injection into a page we do not control. It lifts above the on-screen keyboard
when that is up.

**The rest of the display dims** while the assistant is working, so the eye
goes to the indicator rather than the dashboard. The dim is a separate layer
that fades in and out rather than switching, so it does not flash.

It must never show when the device is not actually listening or speaking.
Showing an indicator that is not measuring anything is the same class of lie as
reporting a state that is not true.

### 3. Settings

A separate **voice** tab in the settings panel, alongside the existing
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

## What the device program still needs to give us

| Need | Today | Required |
|---|---|---|
| Mic level for the indicator | `peakLevel()` exists in the emulator but is not exposed by the device program | A level the app can read, e.g. `Device.OnMicLevel(func(int))` |
| Actual waveform | not available | A block of samples or a small spectrum, if the dots are to become a real waveform |
| Connection state | `state.Snapshot` has `LinkUp` and `Approved`; the phases carry the rest | The app needs to map those onto connected / unpaired / disabled |
| Ring colours | `display.Tracker` has them | Needs to reach the UI, probably as part of the state snapshot |
| Config push | `protocol.Config` decodes it | The app needs to read the pushed config and apply it, and to send config changes back |

The level is the one that matters. The `display` package already exists for
the ring, so the natural shape is for the device program to publish a comparable
`audio.Tracker` that derives a level from the capture path — the same job
`display.Tracker` does for the ring.

## Design decisions to argue about

1. **A level, not a waveform.** The device program only exposes a peak. The
   indicator sizes dots by that level. If a real waveform is wanted, it needs to
   expose samples.

2. **Dots, not a ring.** The ring is the Echo's language and this device has
   no ring. A row of dots reads as a voice assistant, which is what this is.

3. **The rainbow, like Google.** The dots and the label cycle through a
   spectrum, and the display dims behind them. A single flat colour does not
   read as "the assistant is working" nearly as well.

4. **The indicator only appears when the device is doing something.** It is
   not a permanent row of status LEDs.

5. **Unknown is drawn as unknown.** Never as the opposite. A grey "Unknown"
   is correct; a green "Connected" when the device has not said is a lie.

6. **The settings page is short on purpose.** Board, wake word, microphone
   and speaker. Everything else is either constant or a deployment concern.

## Prototype

This branch carries a working prototype:

- The drawer shows a voice assistant row in the same layout as the other services,
  with the controller connection as the value.
- A canvas indicator draws over the view — dots sized by the microphone level
  while listening, pulsing along while speaking — driven by a **mock** level
  source, so the motion can be judged without the device program.
- The settings panel has a voice tab with the connection status and the
  short settings list.

The mock data lives in one clearly marked place so it can be swapped for the
real device program when it lands.
