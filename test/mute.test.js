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
// On the Echo Show 8 the microphone's mute is a hardware latch, and
// only the button on top of the unit moves it. The latch is one-way
// from software: the button is also the only thing that releases it.
// So neither the panel nor Home Assistant can switch it, and the panel
// shows where the latch is instead.
//
// These are checked by looking at the code, because what matters is
// that the two sides are wired to each other and not that a line of
// code is there.

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

test("there is no speaker mute", () => {
  // There is one mute on this device and it is the microphone's, and
  // it is the hardware latch's: only the button on top of the unit
  // moves it.
  //
  // The speaker has a level rather than a mute. A level of zero is
  // silence when a person wants it, and the media player is not offered
  // a mute that the hardware would not honour.
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
  // And the media player does not offer the mute to Home Assistant.
  assert.ok(
    !devicePlayer.includes("mediaPlayerVolumeMute"),
    "the media player still offers a mute",
  );
});

test("the panel cannot switch the mute", () => {
  // The latch is one-way from software, so there is nothing for the
  // panel to ask for that the hardware would do.
  assert.ok(
    !main.includes("controlClient.sendMute("),
    "the panel still tells the device program to mute",
  );
  assert.ok(
    !main.includes("async function setMute("),
    "the panel still has a way to mute",
  );
  // The device program still hears a mute from the panel, and ignores
  // it. A panel that has not been updated yet asks for one, and the
  // hardware would refuse it anyway — so the ask is heard and dropped
  // rather than being a command the device program does not know.
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
  // The switch in Home Assistant is the device's, and it is the
  // microphone's mute that it means.
  //
  // On the Echo Show 8 the microphone's mute is a hardware latch, and
  // the device program owns it: it watches the latch and reports where
  // it ended up. So the panel follows the device program's state
  // rather than writing the mixer, which is not where the microphone
  // is cut.
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
  // And the device program is what watches the latch.
  assert.ok(
    deviceMain.includes("newMuteController"),
    "the device program does not own the microphone's mute",
  );
});

test("the mute is shown on the screen", () => {
  // The mute is the hardware's, so the screen cannot change it. It
  // says where it is, and it says it without the drawer being open:
  // a muted device that only says so inside a drawer is a device that
  // looks live.
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
  // The button's light is the red bar on the top of the unit, and it
  // is what says at a glance that the microphones are cut.
  assert.ok(
    deviceMain.includes("setMicMuteLED(muted)"),
    "the light does not follow the latch",
  );
  // The light is found by its name: the LED class on this kernel has
  // no "function" file, so a lookup by function finds nothing.
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
  // The mute is the microphone's, and the microphone belongs to the
  // device, so it is the device's row that says so.
  assert.ok(
    renderer.includes('"Muted"'),
    "the drawer does not say when the device is muted",
  );
});
