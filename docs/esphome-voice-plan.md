# Moving the panel to a real Home Assistant Voice device

The panel is already an ESPHome device to Home Assistant. This plan makes it a
**voice satellite** as well: an `assist_satellite` that Home Assistant talks
to over the native API, with the wake word, the speaker and Sendspin all
running on the device. It also adds a camera.

The template is the EchoMuse Controller's own ESPHome implementation
(`EchoMuse/controller/em_esphome.py`, 3655 lines, working against real HA
Core). That code has already solved the hard parts — the wake-word handshake,
the audio format, the barge-in barrier, the announcement path — and this plan
ports those answers into Go, against the panel's existing ESPHome server.

---

## 1. Why this is worth doing

The panel currently has two separate links to Home Assistant: the ESPHome
native API for control, and the EchoMuse SDK for voice. The SDK link goes to
the EchoMuse Controller (192.168.1.132:8767), which is *itself* an ESPHome
voice satellite for HA. So a voice turn today is:

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

The SDK also has a `replace` directive pointing at an unpublished local
checkout (`../../EchoMuse-SDK-upstream/clients/go`). That alone blocks a
release. Removing it removes the blocker.

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

### Phase 1 — Extend the protocol

`device/esphome/esphome_api.proto` gains the voice, media player, camera,
event and sendspin messages. `trim_proto.py`'s `NEEDED` list grows
accordingly. Regenerate with `tools/build-esphome-proto.sh`.

The wire check (`tools/check_wire.py`) already proves the trimmed subset is
byte-identical to `aioesphomeapi`'s `api_pb2` — extend it to cover the new
messages. **This is the gate that must pass before any of phase 2 onwards is
trustworthy.**

New messages: ~40. The proto goes from 55 to roughly 95 messages.

### Phase 2 — The voice satellite

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

## 8. The immediate next step

Phase 1 and phase 2's protocol work first, because everything else depends on
the proto being right.

The first concrete deliverable is the extended proto passing the wire check
against `aioesphomeapi`, followed by a `DeviceInfoResponse` carrying the voice
flags and a `ListEntities` carrying a media player — enough for HA to create
the `assist_satellite` entity and the `media_player` entity. That is the
"it is a voice device" milestone, and it is verifiable on the dev HA.

Then the turn itself: a `VoiceAssistantRequest`, mic audio, and a transcript
in HA's log. That is the "it talks" milestone.
