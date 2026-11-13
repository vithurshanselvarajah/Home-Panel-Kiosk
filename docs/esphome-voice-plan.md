# Moving the panel to a real Home Assistant Voice device

The panel is already an ESPHome device to Home Assistant. This plan makes it a
**voice satellite** as well: an `assist_satellite` that Home Assistant talks
to over the native API, with the wake word, the speaker and Sendspin all
running on the device. It also adds a camera.

> **What has been done.** The voice satellite, the wake word, the music
> player and the SDK removal are built. The camera and the media player
> entity are not. The sections below are the plan as it was written; the
> "as built" notes say what changed and why.

The template is the EchoMuse Controller's own ESPHome implementation
(`EchoMuse/controller/em_esphome.py`, 3655 lines, working against real HA
Core). That code has already solved the hard parts — the wake-word handshake,
the audio format, the barge-in barrier, the announcement path — and this plan
ports those answers into Go, against the panel's existing ESPHome server.

---

## 1. Why this is worth doing

The panel had two separate links to Home Assistant: the ESPHome native API
for control, and the EchoMuse SDK for voice. The SDK link went to the
EchoMuse Controller (192.168.1.132:8767), which is *itself* an ESPHome
voice satellite for HA. So a voice turn was:

```
panel → EchoMuse SDK → EchoMuse Controller → ESPHome API → Home Assistant
```

Moving to a native implementation makes it:

```
panel → ESPHome API → Home Assistant
```

One link, one protocol, one set of entities. The controller disappears from the
voice path entirely.

What that buys:

| | Today | After |
|---|---|---|
| Voice turns | panel → SDK → controller → HA | panel → HA |
| Latency per turn | one extra hop, one extra encode/decode | one hop |
| Device identity in HA | none (voice is the controller's) | the panel's own ESPHome device |
| Wake word | the controller's openWakeWord | the panel's, on the panel |
| Music | EchoMuse's sendspin | ESPHome's sendspin, same identity |
| Camera | none | ESPHome camera entity |

The SDK also had a `replace` directive pointing at an unpublished local
checkout (`../../EchoMuse-SDK-upstream/clients/go`). That alone blocked a
release. Removing it removes the blocker.

**As built.** The SDK is gone. The panel builds from its own tree alone,
which is checked by cloning the repository and building it: that is the
acceptance test, and it passes.

---

## 2. What the protocol actually gives us

Everything needed is already in the upstream `api.proto`. The panel's trimmed
copy (`device/esphome/esphome_api.proto`, 55 messages) needs these added:

### 2.1 Voice assistant

```
VoiceAssistantRequest        (90)  device → HA: start a turn
VoiceAssistantResponse       (91)  HA → device: ack
VoiceAssistantEventResponse  (92)  HA → device: RUN_START, STT_*, TTS_*
VoiceAssistantAudio          (106) device → HA: PCM chunks
VoiceAssistantAnnounceRequest      HA → device: speak this
VoiceAssistantAnnounceFinished     device → HA: done speaking
VoiceAssistantConfigurationRequest / Response   wake word picker
VoiceAssistantSetConfiguration                  HA → device: enable/disable wake word
VoiceAssistantTimerEventResponse                timer events
```

The transport is **not** the TCP connection's protobuf stream for audio. ESPHome
sends PCM over `VoiceAssistantAudio` on the same encrypted connection. HA
buffers it and replies with `VoiceAssistantAudio` carrying the TTS.

The audio format is fixed: **16 kHz, mono, signed 16-bit little endian**. That
matches `protocol.MicSampleRate = 16000` on the SDK side, so the existing
capture path is already right — no resampling needed.

It is the *reply* that needs converting. HA speaks to a speaker at 16 kHz
whatever the speaker runs at, and the mixer takes the reply out of its queue
at the speaker's own rate, so the reply is converted from 16 kHz to 48 kHz
before it is queued. A reply that is not converted is played three times too
fast.

**The reply's end is an event, not a flag.** HA sends the reply's audio with
`VoiceAssistantAudio.end` clear and says the reply is over with
`VOICE_ASSISTANT_TTS_STREAM_END`. The flag is only ever true for a reply that
was cut short, so a device that waits for the flag waits for ever.

### 2.2 Media player

```
ListEntitiesMediaPlayerResponse
MediaPlayerCommandRequest    HA → device: play / pause / stop / volume / mute
MediaPlayerStateResponse     device → HA: what it is doing
MediaPlayerSupportedFormat
```

**A media player entity is mandatory.** The EchoMuse controller's code says so
explicitly (`feature_flags.py`, `MediaPlayerEntityFeature` docstring): HA does
not surface a device in Devices & Services at all if it reports zero entities,
even with voice flags set.

### 2.3 Camera

```
ListEntitiesCameraResponse
CameraImageRequest   (45)  HA → device: single / stream
CameraImageResponse  (44)  device → HA: chunked image, `done` flag on the last
```

The image is raw bytes. HA reassembles chunks into one `CameraState.data` and
serves it as a still or an MJPEG stream. Real ESPHome cameras send JPEG.

### 2.4 Sendspin

```
DeviceCapabilitiesResponse.sendspin   (client_id + feature flags)
SendspinPairingTokenRequest/Response  (the pairing token)
```

**This is the part that fixes the Music Assistant duplication.** HA's ESPHome
integration has two websocket commands built for exactly this:

- `sendspin/subscribe_clients` — "the ids of the connected devices that hand
  out a pairing token"
- `sendspin/get_pairing_token` — "the Sendspin pairing token of the device with
  this Sendspin client id"

MA calls those to pair with real ESPHome devices instead of creating its own
player for them. The duplication happens because the EchoMuse controller
exposes its sendspin client through the *EchoMuse* protocol, not through the
ESPHome native API — so HA does not know the controller's `client_id` belongs
to the same device HA already has a `media_player` for.

Making the panel a native ESPHome device with a native sendspin client means
HA tracks its `client_id`, and MA can pair with it. **One device, one player.**

---

## 3. Design

### 3.1 Architecture

Everything lives in the Go sidecar (`device/`), which already owns the
microphone and the speaker. The Electron app stays a renderer and talks to the
sidecar over the existing socket in `/dev/shm`.

```
┌───────────────────────── Electron ─────────────────────────┐
│  UI: drawer, settings, wake-word upload, camera view       │
│         │  /dev/shm socket (existing)                      │
└─────────┼──────────────────────────────────────────────────┘
          │
┌─────────▼────────── device/ (Go sidecar) ───────────────────┐
│                                                         │
│  esphome/            ESPHome API server (existing)       │
│    ├── control plane      entities, services   (done)   │
│    ├── voice              NEW: satellite                  │
│    ├── mediaplayer        NEW                             │
│    ├── camera             NEW                             │
│    └── sendspin           NEW                             │
│                                                         │
│  audio.go           microphone + speaker (existing)      │
│  wakeword/          NEW: openWakeWord ONNX (port from    │
│                     EchoMuse device/internal/wakeword)    │
└─────────────────────────────────────────────────────────┘
```

### 3.2 The voice turn

Mirroring `run_esphome_voice_turn` in the controller:

1. Wake word fires (or the panel's button, or HA's
   `assist_satellite.start_conversation`).
2. Device sends `VoiceAssistantRequest{start: true, conversation_id, flags: 0,
   wake_word_phrase: "<phrase>"}`.
   - `flags: 0` means "the device already detected the wake word" — HA skips
     its own wake-word stage.
   - `wake_word_phrase` **must not be empty** for a wake-word turn. HA's
     setup dialog reads it and raises `AssistSatelliteError("No wake word
     phrase provided")` if it is missing. An empty string is how the protocol
     says "no wake word" (aioesphomeapi maps `""` → `None`), which is right
     for a button turn and wrong for a wake-word turn.
3. Device streams mic PCM as `VoiceAssistantAudio{data: ...}`.
4. Device sends `VoiceAssistantAudio{data: "", end: true}` at end of speech.
   Endpointing is HA's `STT_VAD_END` — authoritative, model-driven. The
   device's own VAD is advisory.
5. HA replies with `VoiceAssistantEventResponse` events: `RUN_START`,
   `STT_START`, `STT_END` (with the text), `INTENT_START`, `INTENT_END`,
   `TTS_START`, `TTS_END`, `RUN_END`.
6. TTS audio arrives as `VoiceAssistantAudio` from HA; the device plays it.
7. Device sends `VoiceAssistantAnnounceFinished`.

### 3.3 The barge-in barrier

This is the subtle one, and the controller documents why it exists.

HA acknowledges an abort with **no wire message at all** — `_abort_pipeline`
just cancels the task. So when a barge-in happens, the aborted run's tail
(`RUN_END` arriving ~4 ms after the new turn starts) lands on the new turn.
HA's `RUN_END` handler then reads "HA ended a run it never started" and ends
the turn.

The fix is **ordering, not timing**: arm a barrier on abort, discard every
event until the next `RUN_START` (which is necessarily ours, because HA emits
`RUN_START` only when it creates a pipeline and we created exactly one more).
Bounded to one turn.

Without it, every barge-in ends with zero audio captured — measured 5 of 5 in
the controller's own testing.

**As built.** The barrier is armed when the running turn is interrupted
(`Voice.cutOff`) and every event is discarded until the next `RUN_START`
(`device/esphome/voice.go`). A turn that starts while another is running
interrupts it rather than being refused, so saying the wake word during a
reply cuts the reply off and starts a new turn.

**The threshold, and why it is not lower.** The controller's threshold sits
*below* the wake word's own, which is what makes interrupting easy — but only
once the device has **echo cancellation**. The controller's own journal says
so: *"Enable AEC first — without it the watcher scores raw echo and the raised
threshold is the only defence."*

This panel has no AEC, so the barge bar sits **above** the wake word's own
(`bargeInThreshold = 0.85`). The echo of the reply is about 25 dB louder at
the microphone than a person talking over it, and a lower bar would make the
panel interrupt itself. The two-frame rule at the low bar is already in the
scorer (`device/wakeword/scorer/scorer.go`); only the threshold's position
depends on the AEC.

### 3.4 Wake word

The user is happy to upload a WW file by hand, so the model is a file on disk
rather than something HA pushes.

**The model runs on the device.** ESPHome's `micro_wake_word` is ESP32-only
(`esp32.only_on_variant` on ESP32/ESP32S3/ESP32P4/ESP32H4/ESP32S31) and needs
PSRAM. That rules it out on a Pi. But the EchoMuse project already has a
**Go** implementation of the openWakeWord pipeline:

```
EchoMuse/device/internal/wakeword/
├── stream.go        the buffering: melspectrogram → embedding → classifier
├── ort/ort.go       ONNX Runtime via dlopen (cgo, -ldl only)
├── ort/shim.h      vendored ONNX Runtime C header (MIT)
└── shadow/          the shadow model
```

`stream.go` is pure Go with no cgo, so it builds and tests on the host with no
ONNX Runtime present. `ort.go` **dlopens** `libonnxruntime.so` at runtime
rather than linking it — so the sidecar builds and boots with the library
absent, and `Open` returns an error the caller can fall back from.

Measured on the Echo Dot Gen 2 (Cortex-A7 @ ~1 GHz): **36.2% of one core** at
one thread with XNNPACK, against 243% for ONNX Runtime's own defaults. On a
4-core Pi that is roughly 9% of the machine, duty-cycled (29 ms of work per
80 ms frame). That is the right trade for a 2 GB box.

The three models are chained per 80 ms of audio:

```
audio → melspectrogram → ring of mel frames → embedding → ring → classifier → score
```

Tensor shapes are fixed by the openWakeWord exports:
`melspectrogram: [1, samples] → [1,1,frames,32]`,
`embedding: [1,76,32,1] → [1,1,1,96]`,
`classifier: [1,16,96] → [1,1]`.

**HA still manages it.** The device reports its model in
`VoiceAssistantConfigurationResponse{available_wake_words, active_wake_words}`,
and HA's wake-word picker writes back through
`VoiceAssistantSetConfiguration{active_wake_words}`. "No wake word" in HA
turns detection off. This is exactly the controller's existing behaviour, and
it is what "managed by HA" means.

The panel's `wakeWordEnabled` setting and HA's picker are the same switch seen
from two sides. HA is authoritative when it has written; the panel's setting
is the default until then.

The **phrase** matters: HA compares the `wake_word_phrase` on a pipeline start
against the state of its wake-word select entity, whose options are the
*display names* we advertise. So the phrase announced and the name advertised
must be the same string, or HA matches neither and silently falls back to
pipeline 1. One function produces both — the controller has exactly this in
`em_oww_models.display_name()`.

### 3.5 The media player

One `media_player` entity, named empty (HA composes "<device> <entity>" itself
and would render the label twice).

Supported formats: the controller uses **48 kHz, mono, S16, WAV** for both
default and announcement purposes, and tells HA to run audio through its
ffmpeg proxy so it arrives already at the wire format. That avoids holding
audio back between sentences waiting for a decoder.

Feature flags: `PAUSE | VOLUME_SET | VOLUME_MUTE | PLAY_MEDIA | STOP | PLAY |
MEDIA_ANNOUNCE`.

`MediaPlayerCommandRequest` handling:
- `media_url` → play the URL (HA's proxy URL, already converted).
- `announcement: true` → interrupt music, play, resume.
- `command` → play/pause/stop/mute/unmute.
- `volume` → HA's float 0.0–1.0 → the device's native level.

### 3.6 Announcements

`VoiceAssistantAnnounceRequest{media_id, text, preannounce_media_id,
start_conversation}` makes HA speak to the device.

- With `start_conversation: true`, the announcement is the *preannounce* of a
  turn — HA then expects a `VoiceAssistantRequest` back from the device.
- Without it, it is a plain announcement: the device plays the audio and
  sends `VoiceAssistantAnnounceFinished`.

The `ANNOUNCE` feature flag is required, or HA never sends
`VoiceAssistantConfigurationRequest` and the wake-word picker never appears.

### 3.7 Timers

`VoiceAssistantTimerEventResponse` carries timer start/update/cancel/finished.
The panel can show a timer state as a sensor. This is cheap and the controller
already has it, but it is **optional** — it needs no firmware support and can
come later.

### 3.8 Sendspin

The panel becomes a Sendspin player:

- A persistent Curve25519 keypair. `client_id` is the base64url public key
  (43 chars). The private key must be persistent across reboots, and the
  identity *is* the key.
- A persistent pairing PSK. The **pairing token** is
  `client_key (32 bytes) || pairing_psk (32 bytes)`, base64url-encoded.
- mDNS on `_sendspin._tcp` with `path=/sendspin`.
- A WebSocket server on port 8928 at `/sendspin`, speaking the Sendspin
  protocol (`KKpsk2` Noise pattern, server is the initiator).

**The SDK already has this, and the panel already runs it.**
`EchoMuse-SDK-upstream/clients/go/echomuse/sendspin/` is a complete Sendspin
client (2893 lines): `client.go` (the websocket server, session admission),
`keys.go` (identity, PSK, token), `codec.go` (FLAC/PCM decode), `player.go`
(the jitter buffer), `store.go` (persistence), `noise.go`, `outclock.go`,
`session.go`, `messages.go`, `timefilter.go`, `volume.go`.

`device/audio.go` already instantiates it — `sendspin.New(sendspin.Config{...})`
at `audio.go:1272`, with the store at `~/.config/.../sendspin.json`, and the
speaker's `outputLoop` already calls `player.Fill(out, playAt, uncertain)`.

So phase 5 is **not** new work: it is the same code, moved into this
repository, with the ESPHome-native announcement of it added.

This is the one place where **vendoring** is the right answer rather than a
rewrite. The Sendspin protocol is non-trivial (re-handshake to promote a
session to paired, PSK categories, sentinel fallback) and this code is
already written, tested, and MIT-licensed. The user's stated goal is to
remove the dependency on *another project's* code — vendoring a file set into
this repository, with its licence, achieves that: the panel builds from its
own tree with no `replace` directive and no external checkout.

The dependencies come along unchanged: `gorilla/websocket` and `mewkiz/flac`
are already in `device/go.mod` as indirect deps of the SDK, and become direct
ones.

The protocol implementation to vendor is roughly:

```
device/sendspin/          (from EchoMuse-SDK-upstream/clients/go/echomuse/sendspin/)
├── client.go     the websocket server, session admission
├── codec.go      FLAC + PCM decode
├── keys.go       identity, PSK, pairing token
├── messages.go   the JSON message layer
├── noise.go      KKpsk2
├── outclock.go   the output clock
├── player.go     the jitter buffer
├── session.go    one connected server
├── store.go      persistence
├── timefilter.go
└── volume.go
```

`flac` is already a dependency (`github.com/mewkiz/flac`) and `gorilla/websocket`
would be added.

### 3.9 Music Assistant: one device, one player

The duplication, precisely:

- The panel's ESPHome device gives HA a `media_player` entity.
- HA's `hass_players` provider in MA turns every HA `media_player` with
  `PLAY_MEDIA` into an MA player.
- The EchoMuse controller *also* exposes a sendspin player to MA directly.
- MA therefore has two players for one speaker.

The fix is the one HA built the websocket commands for. When the panel is a
native ESPHome device:

1. The panel's sendspin client reports its `client_id` in
   `DeviceCapabilitiesResponse.sendspin`.
2. HA tracks it (`esphome/sendspin.py`, `_async_set_client_id`).
3. MA calls `sendspin/subscribe_clients`, sees the `client_id`, and calls
   `sendspin/get_pairing_token` to get the token.
4. MA pairs with the **existing** sendspin client and does **not** create a
   second player.

MA's own `hass_players` helper has a hint of this already —
`get_esphome_supported_audio_formats` reads the ESPHome config entry's
diagnostics to learn the device's formats, and `player.py` sets
`bypass_proxy: True` for `hass_domain == "esphome"` because "MA already
delivers an optimized stream".

**One thing to confirm during implementation:** whether MA's sendspin provider
currently skips HA `media_player` entities that have a paired sendspin client,
or whether it needs the pairing to be visible from the HA side. The
`sendspin/subscribe_clients` + `get_pairing_token` path is exactly designed
for this, but MA's provider code was not fully readable in this pass and the
behaviour must be verified against a real MA instance.

### 3.10 Camera

The user wants the panel's camera feed viewable in HA.

**The panel has no camera today** — `cdd595d` removed the HA camera feature
(the MQTT-era camera list), and there is no V4L2 or `getUserMedia` code in the
tree. So this is a new capture source, not a re-wiring.

Design:

```
V4L2 /dev/video0 ──► capture ──► JPEG encode ──► CameraImageResponse
   (or an RTSP/HTTP URL)              chunks
```

The panel is a Raspberry Pi, so the realistic sources are:

1. **A USB or CSI camera via V4L2.** Capture a frame with `VIDIOC_DQBUF`,
   encode JPEG, serve it. On a Pi, `libjpeg-turbo` or `libv4l2` + `mjpeg`
   from `ffmpeg`. If the camera already outputs MJPEG, no re-encode is needed —
   the frame is copied straight out, which is the lightest option and keeps
   the CPU near zero.
2. **An IP camera (RTSP/HTTP).** The panel pulls a frame and re-encodes.

The protocol side is simple:

- `ListEntitiesCameraResponse{object_id, key, name}` — one camera entity.
- On `CameraImageRequest{single: true}`: capture one frame, encode, send it as
  one or more `CameraImageResponse{key, data, done}`.
- On `CameraImageRequest{stream: true}`: keep sending frames as they are
  captured, each its own `CameraImageResponse` with `done: true`.

HA reassembles chunks by `key` and caps a frame at 8 MB. `done` marks the end
of a frame, not of the stream — for a stream each frame is its own complete
sequence.

**A note on the current framing.** The panel's `frame.go` caps a plaintext
frame at some size; the encrypted path uses a 2-byte length prefix, so a
single frame can be 64 KB. A 640×480 JPEG is typically 30–60 KB, which fits
in one encrypted frame. Larger images must be chunked — which is exactly what
`CameraImageResponse`'s chunking is for, so send images in 32 KB chunks
regardless. That keeps the memory high-water mark low and the write pattern
simple.

Memory: a 640×480 JPEG frame is ~40 KB in RAM, and there is no disk involved.
The frames are handed to the socket and dropped.

**Cost to check:** whether the panel's ALSA capture and a V4L2 capture can run
at the same time without fighting over the USB bus. They can — they are
different devices — but the panel's camera may be a USB webcam on the same
bus as a USB sound card, which is a bandwidth question, not a correctness one.

---

## 4. Lightness

The 2 GB budget is the constraint that shapes every choice.

### 4.1 What we are not adding

- **No Python.** The whole point is a Go sidecar that is already running. A
  Python `esphome` host build would add ~40 MB RSS and a second runtime.
- **No Chromium for the camera.** The camera is decoded by HA, not by the
  panel. The panel does not display its own camera feed in this plan.
- **No `micro_wake_word` (ESP32-only, needs PSRAM).**

### 4.2 What we are adding

| Item | Cost | Note |
|---|---|---|
| ONNX Runtime | ~12 MB on disk, ~15–20 MB RSS | dlopened, optional at runtime |
| openWakeWord models | ~5 MB | three ONNX files, uploaded by the user |
| gorilla/websocket | already an indirect dep | becomes direct; needed by Sendspin |
| mewkiz/flac | already an indirect dep | becomes direct; Sendspin's FLAC decode |
| Sendspin code | ~2900 lines, vendored | from the SDK, into this tree |
| Camera | depends on source | MJPEG passthrough ≈ 0 CPU |

ONNX Runtime is the one real addition. It is **dlopened at runtime, not
linked**, so:
- The sidecar builds and boots with no `libonnxruntime.so` present.
- `ort.Open` returns an error, and the caller falls back to HA-side detection
  (the device sends `flags: USE_WAKE_WORD` and lets HA run the wake-word
  stage instead).
- A user who never uploads a wake-word model never pays for it.

The current sidecar is **16 MB RSS** for the SDK + audio + the ESPHome server.
Removing the SDK takes that down; adding ONNX Runtime takes it back up.
Net: roughly flat, with the SDK's dependency removed.

**Disk writes: zero.** The wake-word models are read once at startup and
mapped. The Sendspin store (keypair, PSK, pairing records) is written only
when it changes — pairing, volume change, a new server. Not on a timer. The
camera writes nothing: frames go to the socket and are dropped.

**System RAM for temporary data**, as asked:
- The Sendspin jitter buffer is a fixed in-RAM ring, sized by the negotiated
  format.
- The wake-word feature ring is 76 mel frames of 32 floats — about 10 KB.
- The camera frame is one buffer, reused.

---

## 5. Work breakdown

Each phase is a commit. The proto is extended in phase 1, and every phase
after that is Go work against it.

Phases 1, 2, 4 and 7 are done, along with the wake word's gate and the
music player's move into this tree. Phases 3 (the media player) and 6 (the
camera) are not started.

### Phase 1 — Extend the protocol

**Done.** 80 messages, 41 of them checked byte-for-byte against
`aioesphomeapi`.

`device/esphome/esphome_api.proto` gains the voice, media player, camera,
event and sendspin messages. `trim_proto.py`'s `NEEDED` list grows
accordingly. Regenerate with `tools/build-esphome-proto.sh`.

The wire check (`tools/check_wire.py`) already proves the trimmed subset is
byte-identical to `aioesphomeapi`'s `api_pb2` — extend it to cover the new
messages. **This is the gate that must pass before any of phase 2 onwards is
trustworthy.**

New messages: ~40. The proto goes from 55 to roughly 95 messages.

### Phase 2 — The voice satellite

**Done.** `device/esphome/voice.go`, `voice_server.go` and `voice_turn.go`.

New file `device/esphome/voice.go`:

- `VoiceAssistant` — holds the turn state, the conversation id, the event
  handlers.
- Handle `SubscribeVoiceAssistantRequest`, `VoiceAssistantConfigurationRequest`,
  `VoiceAssistantSetConfiguration`, `VoiceAssistantEventResponse`,
  `VoiceAssistantTimerEventResponse`.
- Send `VoiceAssistantRequest`, `VoiceAssistantAudio`,
  `VoiceAssistantAnnounceFinished`.
- The barge-in barrier.

Extend `device/esphome/server.go`:
- `DeviceInfoResponse.voice_assistant_feature_flags` — `VOICE_ASSISTANT |
  API_AUDIO | ANNOUNCE | TIMERS | START_CONVERSATION`.
- `DeviceCapabilitiesResponse` stays mostly empty.

Extend `device/esphome/entity.go`:
- `KindMediaPlayer`, `KindCamera`, `KindEvent` — these have their own
  listing and state messages, so they need their own kinds.

New file `device/voice_turn.go` — the turn orchestration, mirroring
`run_esphome_voice_turn`:
- Start the turn, stream the mic, end on `STT_VAD_END`, play the TTS.
- The 20-second hard cap on the streaming phase.
- The no-speech timeout.

Wire it to the existing `device/audio.go`:
- `startCapture` already opens the mic at `captureRate` and pushes frames.
- The frames must reach the turn instead of the SDK's queue. Today they go
  into `a.pushFrame(pcm)`; the new path adds a consumer.

**Validation:** against the dev HA (192.168.1.132, HA 2026.11.0), the panel
appears with an `assist_satellite` entity, and a turn completes with the
transcript visible in the HA log.

### Phase 3 — The media player

New file `device/esphome/mediaplayer.go`:

- `ListEntitiesMediaPlayerResponse` with the supported formats and feature
  flags.
- `MediaPlayerCommandRequest` handling.
- `MediaPlayerStateResponse` pushes.
- The volume mapping (HA float → device level).

The playback path reuses `device/audio.go`'s speaker. The controller's
`_play_media_announcement` wraps the standalone play so music pauses and
resumes — the same is needed here.

**Validation:** `media_player.play_media` on the panel's entity plays audio;
`media_player.volume_set` changes the level; HA's player card shows the
right state.

### Phase 4 — Wake word

**Done**, with the gate as well.

New package `device/wakeword/`, ported from `EchoMuse/device/internal/wakeword/`:

- `stream.go` — the buffering (pure Go, no cgo).
- `ort/ort.go` — ONNX Runtime via dlopen.
- `ort/shim.h` — the vendored MIT C header.

New `device/wakeword_models.go`:
- Model discovery in a directory.
- The display-name function (one source for the advertised name and the
  announced phrase).
- The score → trigger logic with the threshold and the shadow model.

Wire it into the turn: the wake word fires → the turn starts with
`wake_word_phrase` set and `flags: 0`.

**Validation:** the model is uploaded through the panel's settings; the wake
word fires; HA's transcript shows the right phrase; HA's wake-word picker
turns it off and the panel stops listening.

### Phase 5 — Sendspin

**Partly done.** The player is vendored and running; the mDNS
advertisement and the pairing token over the native API are not.

Vendor `EchoMuse-SDK-upstream/clients/go/echomuse/sendspin/` into
`device/sendspin/`, with its MIT licence file.

The code itself is unchanged — it is already running on the panel today. What
is new is how the device **announces** it:

Add:
- The mDNS advertisement on `_sendspin._tcp` with `path=/sendspin`, alongside
  the existing `_esphomelib._tcp` one. The instance name must be unique on
  the LAN — the SDK's `Config.Instance` is currently the fixed string
  `"echomuse-home-panel"`, which would collide if two panels were on the
  same network. It needs to be per-device.
- `DeviceCapabilitiesResponse.sendspin{client_id, feature_flags}`. The
  `client_id` is the base64url public key (43 chars) from the sendspin store.
- `SendspinPairingTokenRequest` → `SendspinPairingTokenResponse{status, token}`.
  The token is `client_key || pairing_psk`, base64url-encoded — the SDK
  already has `sendspin.Client.PairingToken()`.

The `SendspinPairingTokenStatus` enum matters:
- `OK` — the token is handed out.
- `ENCRYPTION_REQUIRED` — the device has no API encryption key, so HA will
  not ask for a token. Ask again once a key is set.
- `NOT_READY` / `DISABLED` / `FAILED` — as they are.

**Validation:** the panel appears in Music Assistant as one player; MA can
play to it; the panel's ESPHome `media_player` entity is the *same* player.

### Phase 6 — Camera

New file `device/camera.go`:

- The capture: V4L2, or an HTTP/RTSP pull.
- The JPEG encode (or MJPEG passthrough).
- `ListEntitiesCameraResponse`.
- `CameraImageRequest` handling — single and stream.

Add to `device/esphome/entity.go`: `KindCamera`.

**Validation:** the camera appears in HA, a still is served, and the MJPEG
stream works.

### Phase 7 — Remove the SDK

**Done.** A clean clone builds and passes every test with nothing outside
this repository.

This is the phase that unblocks the release.

- Delete the `echomuse` imports from `device/main.go` and `device/audio.go`.
- Delete the `replace` directive from `device/go.mod`.
- The SDK's `sendspin` copy moves to `device/sendspin/` — the code is
  unchanged, so `device/audio.go`'s `sendspin.New(...)` call keeps working
  with a different import path.
- **`audio/backend.go` also imports the SDK** — for `echomuse.AudioBackend`,
  `echomuse.AudioDeviceProvider`, `echomuse.ClockedAudioBackend`,
  `echomuse.AudioDevice` and `echomuse.ClockedAudioOutput`. These are three
  small interfaces and one struct. They move into the panel's own `audio`
  package (or a small `device/audiodevice` package), and the interface
  assertions are kept as compile-time checks.
- Keep the parts that are not the SDK: `device/audio.go`'s ALSA capture and
  playback, the ONNX wake word, the VAD.
- The `device/protocol` config shape changes: `wakeWordEnabled`,
  `wakeThreshold`, `micGainDb` and the rest stay; the EchoMuse-specific ones
  (controller URL, credentials directory, board) go.
- The Electron app's settings page loses the EchoMuse-specific fields.

**Validation:** `go build ./...` with no external checkout present. This is the
acceptance test: clone the repo alone and it builds.

The SDK packages the panel uses, and where each one goes:

| SDK package | Lines | Goes to | Why |
|---|---|---|---|
| `echomuse/sendspin` | 2893 | `device/sendspin/` | vendored as-is; already running |
| `echomuse/wakeword` | 698 | `device/wakeword/` | the `Inferer` boundary; the ONNX side comes from the EchoMuse device tree |
| `echomuse/vad` | 898 | `device/vad/` | the endpointing fallback; still needed as the device-side guard |
| `echomuse/turn` | 182 | `device/turn/` | the turn guard; the SDK's `turn.Guard` bounds the device's own turn |
| `echomuse/state` | 465 | `device/state/` | the snapshot the panel publishes |
| `echomuse/display` | 316 | `device/display/` | the ring buffer tracker |
| `echomuse/protocol` | 1381 | `device/protocol/` | the config shape; the EchoMuse-specific parts go |
| `echomuse/audiodevice` | 327 | `device/audiodevice/` | the ALSA device listing |
| `echomuse` (root) | 4857 | — | **deleted**: the client, the pairing, the controller link |

`echomuse/vad` and `echomuse/turn` are worth keeping for a reason that is not
obvious: HA's VAD is the *authoritative* endpoint, but HA's VAD only runs once
a turn has started. The device's own guard is what stops a turn that never
reached HA from hanging.

---

## 6. Risks

| Risk | Severity | Mitigation |
|---|---|---|
| HA's wake-word stage never runs because the device sent `flags: 0` | high | `flags: 0` is only correct when the device really did detect the wake word; the controller's logic is copied exactly |
| The barge-in barrier is wrong and every barge-in captures no audio | high | port the controller's barrier verbatim, with its own tests |
| MA still creates a second player | medium | verify against a real MA instance in phase 5; the `sendspin/subscribe_clients` path is built for this |
| ONNX Runtime is unavailable on the target | medium | it is dlopened, not linked; the fallback is HA-side wake word detection |
| The camera and the microphone fight over the USB bus | low | a bandwidth question, not a correctness one; test on the real hardware |
| The trimmed proto drifts from upstream | low | the wire check already exists; extend it and keep it in CI |
| The audio format is wrong and HA rejects the stream | low | 16 kHz mono S16, confirmed from ESPHome's own `SAMPLE_RATE_HZ` |

---

## 7. What is deliberately out of scope

- **Replacing the Electron app.** It stays the renderer.
- **The panel displaying its own camera.** The camera is for HA.
- **The `assist_satellite` UI in the panel.** HA owns that.
- **Multi-channel audio.** The panel has one microphone.
- **A local HA-side wake word fallback in the panel's own code.** That is what
  ESPHome's `micro_wake_word` does on ESP32; on a Pi the device does the
  detection itself with the ported openWakeWord pipeline.

---

## 8. What changed while building it

### Two kinds of music, and they take turns

There are two musics: the one Home Assistant plays, which arrives as a
stream from its proxy, and the one a music server plays, which arrives over
SendSpin. They are both music, and playing both at once is neither of them.

So they take turns. When Home Assistant's music starts, the music server's is
paused; when the music server's starts, Home Assistant's is stopped. The
microphone and the reply are never stopped — they are the point of the panel.

The ducking follows from the same arrangement: the reply turns the music
down under it and the music comes back up when the reply is over. The duck
was *set* and never *applied* — `setDucked` existed and nothing called it,
so a duck of twenty decibels did nothing at all.

### The music had to be buffered, and it was not

The first version of the streaming played the music as the network delivered
it. The speaker takes a period every ten milliseconds and the network is
slower than that, so the speaker outran the network and the queue ran dry:
the music arrived in bursts with silence between them, which is a tick
rather than music.

The fetch now keeps a second of music queued ahead of the speaker and waits
when it is four seconds ahead, so it neither starves nor runs away and
buffers the whole thirty-megabyte track. The controller's own pacing module
describes the same problem: *"The voice path used to send every period as
fast as the socket would take it, and that cut long responses off
mid-sentence."*

### The end of the run is not the end of the reply

When home assistant streams the answer, it ends the *run* when the agent
has written the answer, and then keeps sending the audio. The run's end
and the reply's end are different moments.

The device used to end the reply on the run's end, which cut the answer
off in the middle and left the overlay thinking for ever. The reply now
ends when the speaker has drained it, which is the only moment it is
really over — the last frame arriving is not the end, because the speaker
still has the words in it.

ESPHome's own firmware says the same thing: the reply's end is the end of
the audio stream (`TTS_STREAM_END`), and the run's end only stops the
microphone.

### Where the answer's words come from

Home Assistant sends the answer's text in two places, and they are not
equally good.

The **start of the text-to-speech** carries the text going into the
engine, and it is there whatever the answer is and whatever kind of
markup it is in. It is the reliable one.

The **end of the intent** carries the answer too, but only the plain
kind of it: an answer written as ssml or as something else is empty
there. It is kept as a fallback, for when the text-to-speech does not
carry the words either.

The device takes the text-to-speech's and falls back to the intent's.

### The reply's progress is counted in one set of units

The reply arrives at sixteen kilohertz and is played at forty-eight, so
the audio that has arrived and the audio in the queue are three times
different sizes.

Counting the arrival and comparing it with the queue gave a progress
that was always nothing played, so the answer's words never appeared
and the screen sat there with the question and no answer.

Both are counted after the conversion now, so they are the same bytes.

### The player goes back to idle when the reply has been said

The media player card in Home Assistant shows the speaker's state, and a
player left on "playing" is a player that looks busy for ever.

The reply is over when the speaker has drained it, so that is where the
state goes back to idle — not where the last frame arrives, because the
speaker still has the words in it then.

The EchoMuse controller has the same rule, and says why: *"while a voice
turn owns the speaker our internal state is PAUSED but HA must keep
seeing PLAYING, or a spoken 'pause' is answered with 'already paused'
and never sent."*

### The words are part of the state

The panel shows what was heard and what is being said. The state is only
written when it has *changed*, and the words were not part of that
comparison, so the state was not rewritten when they changed and the
panel was never told. The words and the progress of the reply are now
compared like everything else.

### The wake word overlay

The panel now shows a full-screen overlay while the assistant is working,
in the way a smart display does. It covers the screen and closes the
drawer, the keyboard and the settings page. It shows the words being heard
while it listens and the words being said while it answers, and the text
is carried from one state to the next: the words that were heard stay on
the screen while the assistant thinks about them.

### The voice settings belong to Home Assistant

The wake word's sensitivity, the ducking and the mute were in the device's
own configuration file, which meant Home Assistant could neither read them
nor change them — the person had to edit a file on the machine. They are
entities on the panel now, so Home Assistant reads and changes them the way
it changes everything else. The panel no longer writes them, so it cannot
undo what Home Assistant has set.
Three things turned out differently from the plan, and each one was worth
finding out.

### The device needs no speech detector

The plan had the panel running its own voice activity detection, ending a
turn when the person stopped talking. It does not need to.

Home Assistant runs its own: the assist pipeline uses microVAD and a
`VoiceCommandSegmenter`, and ESPHome stops the microphone when it sees
`STT_VAD_END`. The device streams audio until Home Assistant says the
utterance is over. So the panel has no VAD at all, and the microphone is
open for exactly as long as Home Assistant wants it.

That is better than the plan on every count: one less detector to run, one
less set of thresholds to tune, and the endpointing is done by the thing
that is actually transcribing the words.

### The wake word needed a gate, not just a duty cycle

Scoring every frame of every second costs about a third of a core for
ever, and the panel shares four cores with a browser. So the frame is
measured before it is scored — a sum of squares, which is cheap — and only
the frames loud enough to be somebody talking reach the scorer.

The threshold is learned from the room: it comes down quickly and goes up
slowly, so it follows a room that gets louder without a single loud frame
raising it past the speech.

### The ONNX Runtime is linked, not loaded

The runtime was originally loaded at run time, so the binary was small and
the runtime was optional. It is now linked in, which means a device that
has the binary has the wake word: there is nothing to install and nothing
that can be missing.

The cost is a cross-compiler on the machine that builds the arm64 binary,
and the runtime travels beside the binary in the package.

### The music metadata comes from Sendspin, not from ESPHome

The drawer's now-playing panel says what is playing: the title, the
artist, the album, the album art and where the track has got to. The
ESPHome media player protocol carries none of that — `MediaPlayerStateResponse`
has only the state, the volume, the mute and the device id.

Sendspin does carry it, in the `metadata@v1` role. The server sends a
`server/state` message with a metadata object in it, and the client has
to ask for the role in its `client/hello`:

```
client/hello  supported_roles: ["player@v1", "metadata@v1", "controller@v1"]
server/state  {"metadata": {"title": ..., "artist": ..., "artwork_url": ...,
                            "progress": {"track_progress": ..., "track_duration": ...,
                                         "playback_speed": 1000}}}
```

The progress's `playback_speed` is a multiplier of a thousand: a thousand
is playing and zero is paused. The server clears the metadata by sending
it as `null`, which is a different answer from the object simply not
being there — one says the music has stopped and the other says nothing
changed.

The `controller@v1` role is what makes the transport work. The server
says which commands it will take (`play`, `pause`, `next`, `previous`,
`seek`), and the panel only offers the ones it has been told about: a
button that does nothing is worse than no button.

The music panel is drawn by the panel from the device program's state
file, and the buttons travel the other way over the control socket. The
transport belongs to the music server and the panel has the buttons, so
the request goes to the device program, which passes it on.

This is checked against the real server: `device/sendspin/peer_test.go`
drives `aiosendspin` — the library Music Assistant speaks the protocol
with — against the panel over the wire, and the panel's own metadata is
read back out of it.

---

## 9. The answer's words are shown whole

The overlay shows the answer's words as they are spoken. The text
arrives all at once and the audio arrives as it is generated, so the
text is revealed in step with the audio.

Getting that wrong truncates the answer, and it truncated it in a way
that looked like a speech-recognition fault. The panel showed:

```
Sorry, I am not aware of any device called a jo
10:14 P
```

and the answer Home Assistant had actually given was:

```
Sorry, I am not aware of any device called a joke
10:24 PM
```

Both were the same bug, and it was not in the microphone. Home
Assistant heard "joke" in full — it matched it against a device called
"joke" and said so. The panel's own microphone path is byte-exact: the
recording is played into it and the audio that goes on the wire is
compared with the recording, byte for byte
(`device/recording_test.go`).

### Three things had to be true

**The word boundary.** The reveal walked forward to the next space and
stopped *at* it. That cuts the last word in half, and it loses the last
word entirely when the answer is over — "10:18 PM" was shown as
"10:18 ". The walk now goes on to the end of the word and then past the
space, so a word is always whole.

**The progress has to reach the end.** The progress of the reply is
measured against the audio that arrived, and the last of that is held
by the converter until the reply ends. So the count ran short and the
progress never quite reached the end: at 88% the answer stopped at
"...called a jo". Two things fix it: the tail is counted as part of
the reply, and the reply that has been played out is reported as
complete whatever the count says — the speaker has drained it, so it
has been said.

**The words have to arrive before the mode goes off.** The reply ends
and the speaking stops at the same moment, so the state that says the
answer is complete arrives with the state that says the assistant has
finished. The words are given to the overlay first and the mode after,
or the answer stops a word short every time.

### The pattern

Each of these is the same mistake in a different place: the panel was
showing the answer as the audio was played, and every part of that had
to be right — the boundary, the count, and the order the state arrived
in. A part that was wrong did not fail loudly. It showed an answer
that was nearly right, which is the hardest kind of wrong to notice.

---

## 10. The immediate next step

**Done:** the protocol extension, the voice satellite, the wake word with its
gate, the music player and the SDK removal. The wire check covers 41
messages, all byte-identical to `aioesphomeapi`.

**Next:** the media player entity, so the reply to a voice turn has
somewhere to go, and the camera.

The media player is the smaller of the two. It is one entity, the
supported formats, and the handling of `MediaPlayerCommandRequest` — and
the plan has the details in section 3.5.

The camera is the larger one, because the panel has no camera today:
`cdd595d` removed the old Home Assistant camera feature and there is no
V4L2 or `getUserMedia` code in the tree. It needs a capture source, a
JPEG encoder and the chunked image responses.

---

## 11. What the panel costs

The panel is a kiosk on a machine with 4 GB of eMMC and 1 GB of RAM to
spare. What it costs matters more than what it does, because a panel
that is too heavy is a panel that is slow to answer.

The numbers below are from the panel running against the real Home
Assistant, measured over thirty seconds of each activity, with the
writes counted from `/proc/<pid>/io`.

### The memory

| Part | RSS |
|---|---|
| The browser process | 237 MB |
| The graphics process | 170 MB |
| The dashboard's renderer | 170 MB |
| The utility process | 86 MB |
| The three helper processes | 134 MB |
| The device program | 56 MB |
| **All of it** | **833 MB** |

The device program is the small one: 56 MB, and it holds the wake word
model, the microphone and the speaker.

Most of the rest is Chromium's own. A bare Electron with one empty
window uses 249 MB, so the panel's own code is a small part of the
figure and the dashboard and the graphics stack are the rest. There is
no leak: the browser process sits at 234 MB and stays there.

The overlay is a whole renderer process, and it is closed after a minute
of nothing needing it. That is worth 140 MB on a machine where the
memory matters, and it is why the panel sits at 833 MB rather than
985 MB.

### The writes

**Nothing.** The whole panel writes 94 KB of disk in its whole life,
and that is the graphics caches being filled the first time a page is
drawn. After that: zero.

The three things that used to write have been moved:

- The live state is in memory. It is a reading and not a record: it
  changes as the device listens and speaks, and it is worth nothing
  once the process is gone. Writing it to the flash would wear the
  flash out for nothing.
- The microphone's level is in memory, and is written in place rather
  than replaced, so the directory does not change.
- Chromium's caches are in memory and capped at 16 MB, because the
  default is a share of the free disk and a kiosk on a 4 GB card does
  not need a hundred times that.

### The processor

**0.03%** while idle, and **0.04%** while the music streams, the
drawer opens, the volume moves and the microphone is muted.

The panel is not doing much when it is waiting, and that is the point.
Three things used to make it work harder than it needed to:

- The mixer was read every half second, in two processes, for as long
  as the panel was running. The two are read in one process now, and
  the watch runs once a second.
- The device's state file was read four times a second. It is watched
  now, and the poll beside it runs once a second as a backstop.
- The state was compared before it was sent, so a state that had not
  changed did not cross the socket.

### The device program

The device program writes nothing to the disk while it is idle, and its
state file is only rewritten when the state has actually changed. The
level file is written at most four times a second, and only when the
level has moved far enough to see.

### What was changed

| Change | Before | After |
|---|---|---|
| The mixer is read | two processes every 500 ms | one process every 1000 ms |
| The state file is read | four times a second | watched, with a backstop |
| The overlay | always alive | closed after a minute |

### What was left alone

The dashboard is 170 MB of Home Assistant's own Lovelace, and it is not
the panel's to trim. The graphics process is 170 MB because the machine
has no working VAAPI and Chromium falls back to software, and it does
not get smaller when Vulkan is turned off. The three helper processes
are Chromium's own and there is nothing to be done about them from
here.
