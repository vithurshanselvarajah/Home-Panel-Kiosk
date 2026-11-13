const test = require("node:test");
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");

// The mute.
//
// The mute is the microphone's and not the speaker's: a person who
// mutes the panel wants the room to stop hearing them, and they still
// want to hear the music.
//
// It is also the same thing said in two places. Home Assistant has a
// switch on the device, and the system has a microphone that can be
// muted, and each one tells the other what it has just been told.
//
// These are checked by looking at the code, because what matters is
// that the two sides are wired to each other and not that a line of
// code is there.

const root = path.join(__dirname, "..");
const main = fs.readFileSync(path.join(root, "src", "main.js"), "utf8");
const deviceMain = fs.readFileSync(path.join(root, "device", "main.go"), "utf8");
const deviceAudio = fs.readFileSync(path.join(root, "device", "audio.go"), "utf8");
const deviceControl = fs.readFileSync(path.join(root, "device", "control.go"), "utf8");
const renderer = fs.readFileSync(path.join(root, "src", "ui", "renderer.js"), "utf8");
const html = fs.readFileSync(path.join(root, "src", "ui", "index.html"), "utf8");

test("the mute silences the microphone and not the speaker", () => {
  // The microphone is silenced before anything hears it, so the wake
  // word does not fire and Home Assistant hears silence.
  assert.ok(
    deviceAudio.includes("pcm = make([]byte, len(pcm))"),
    "a muted microphone still sends the room",
  );
  // And the speaker is not silenced by it: the music server's own mute
  // is the speaker's.
  assert.ok(
    deviceAudio.includes("muted := a.sendspinMuted"),
    "the mute still silences the speaker",
  );
});

test("the device microphone switch does not call the speaker mute", () => {
  const start = deviceMain.indexOf("control.OnMuted(func(muted bool) {");
  const end = deviceMain.indexOf("\n\t})", start);
  assert.notEqual(start, -1, "the device has no microphone mute handler");
  assert.notEqual(end, -1, "the microphone mute handler is incomplete");
  const handler = deviceMain.slice(start, end);
  assert.ok(handler.includes("reporter.setMuted(muted)"), "the device mute state is not reported");
  assert.ok(!handler.includes("speaker_.setMuted"), "the microphone mute also mutes the speaker");
});

test("the panel asks the device program to mute the microphone", () => {
  // The device program owns the microphone, so the panel asks for it.
  assert.ok(
    deviceControl.includes('case "mute":'),
    "the device program cannot be asked to mute",
  );
  assert.ok(
    main.includes("controlClient.sendMute(value)"),
    "the panel does not tell the device program to mute",
  );
});

test("the device's mute mutes the system's microphone", () => {
  // The switch in Home Assistant is the device's, and it is the
  // system's microphone that it means.
  assert.ok(
    main.includes("followDeviceMute"),
    "the device's mute does not reach the microphone",
  );
  assert.ok(
    main.includes("setSystemMicMuted(muted)"),
    "the microphone is not muted when the device says so",
  );
});

test("a muted microphone turns the device's switch on", () => {
  // The other way round: the microphone muted by something else turns
  // the device's switch on, so Home Assistant shows what is really
  // happening.
  assert.ok(
    main.includes("controlClient.sendMute(muted)"),
    "a muted microphone does not turn the device's switch on",
  );
});

test("the mute is the microphone's own button", () => {
  // The mute is beside the volume's slider and not in its heading, and
  // it is the microphone's and not the speaker's.
  assert.ok(
    html.includes('id="mic-mute"'),
    "there is no microphone mute button",
  );
  assert.ok(
    !html.includes('id="volume-mute"'),
    "the mute is still the volume's own icon",
  );
  assert.ok(
    html.includes('aria-label="Mute the microphone"'),
    "the mute button does not say what it mutes",
  );
});

test("the drawer's status says when the device is muted", () => {
  // The mute is the microphone's, and the microphone belongs to the
  // device, so it is the device's row that says so.
  assert.ok(
    renderer.includes('"Muted"'),
    "the drawer does not say when the device is muted",
  );
});
