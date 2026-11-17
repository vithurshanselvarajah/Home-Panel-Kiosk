const test = require("node:test");
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");

const root = path.join(__dirname, "..");
const main = fs.readFileSync(path.join(root, "src", "main.js"), "utf8");
const deviceMain = fs.readFileSync(path.join(root, "device", "main.go"), "utf8");
const deviceAudio = fs.readFileSync(path.join(root, "device", "audio.go"), "utf8");
const deviceControl = fs.readFileSync(path.join(root, "device", "control.go"), "utf8");
const devicePlane = fs.readFileSync(path.join(root, "device", "control_plane.go"), "utf8");
const devicePlayer = fs.readFileSync(path.join(root, "device", "esphome", "mediaplayer.go"), "utf8");
const renderer = fs.readFileSync(path.join(root, "src", "ui", "renderer.js"), "utf8");
const html = fs.readFileSync(path.join(root, "src", "ui", "index.html"), "utf8");

test("the mute silences the microphone and not the speaker", () => {

  assert.ok(
    deviceAudio.includes("pcm = make([]byte, len(pcm))"),
    "a muted microphone still sends the room",
  );

  assert.ok(
    deviceAudio.includes("muted := a.sendspinMuted"),
    "the mute still silences the speaker",
  );
});

test("there is no speaker mute", () => {

  assert.ok(
    !deviceAudio.includes("speakerMuted"),
    "the speaker still has a mute of its own",
  );
  assert.ok(
    !deviceMain.includes("setSpeakerMuted"),
    "the media player's mute still silences the speaker",
  );
  assert.ok(
    !deviceMain.includes("speaker_.setMuted(muted)"),
    "the media player's mute still moves the microphone's latch",
  );

  assert.ok(
    !devicePlayer.includes("mediaPlayerVolumeMute"),
    "the media player still offers a mute",
  );
});

test("the panel cannot switch the mute", () => {

  assert.ok(
    !main.includes("controlClient.sendMute("),
    "the panel still tells the device program to mute",
  );
  assert.ok(
    !main.includes("async function setMute("),
    "the panel still has a way to mute",
  );

  assert.ok(
    deviceControl.includes('case "mute":'),
    "the device program does not hear a mute from the panel",
  );
  assert.ok(
    deviceControl.includes("A mute from the panel is ignored."),
    "the device program does not say that it ignores a mute",
  );
  assert.ok(
    !devicePlane.includes("p.server.SetMuteHandler"),
    "the device program still has a mute handler to wire",
  );
});

test("the device's mute is the hardware latch's", () => {

  assert.ok(
    main.includes("followDeviceMute"),
    "the device's mute does not reach the microphone",
  );
  assert.ok(
    main.includes("deviceClient?.state?.muted"),
    "the panel does not follow the device program's mute",
  );
  assert.ok(
    !main.includes("setSystemMicMuted(muted)"),
    "the panel still writes the mixer for the mute",
  );

  assert.ok(
    deviceMain.includes("newMuteController"),
    "the device program does not own the microphone's mute",
  );
});

test("the mute is shown on the screen", () => {

  assert.ok(
    html.includes('id="mute-indicator"'),
    "there is no mute indicator on the screen",
  );
  assert.ok(
    !html.includes('id="mic-mute"'),
    "the drawer still has a mute button, which the hardware does not allow",
  );
  assert.ok(
    renderer.includes("function paintMute("),
    "the indicator is not painted from the state",
  );
  assert.ok(
    renderer.includes("muteIndicator.hidden = !on"),
    "the indicator does not follow the latch",
  );
});

test("the mute light follows the latch", () => {

  assert.ok(
    deviceMain.includes("setMicMuteLED(muted)"),
    "the light does not follow the latch",
  );

  const mute = fs.readFileSync(path.join(root, "device", "mute.go"), "utf8");
  assert.ok(
    mute.includes("isMicMuteLED(name)"),
    "the light is not found by its name",
  );
  assert.ok(
    !mute.includes('"/function"'),
    "the light is still looked for by a function the kernel does not have",
  );
});

test("the drawer's status says when the device is muted", () => {

  assert.ok(
    renderer.includes('"Muted"'),
    "the drawer does not say when the device is muted",
  );
});
