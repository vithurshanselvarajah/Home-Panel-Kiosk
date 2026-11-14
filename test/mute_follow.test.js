const test = require("node:test");
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const vm = require("node:vm");

// The mute follows.
//
// The mute is the same thing in two places: the device's switch, which
// Home Assistant reads and writes, and the microphone itself, which is
// what actually stops hearing the room.
//
// On the Echo Show 8 the microphone's mute is a hardware latch, and the
// device program owns it: the panel asks the device program, the device
// program moves the latch, and the latch says where it ended up. So the
// panel does not write the mixer — the microphone is cut at the board,
// and there is no mixer control for it.
//
// Each follows the other, and each only follows the other when the
// change did not come from here. Without that, the two argue: the panel
// mutes the microphone, the device says it is muted, the panel sees
// that it said so itself and mutes it again, for ever.

const root = path.join(__dirname, "..");
const main = fs.readFileSync(path.join(root, "src", "main.js"), "utf8");

// The part of the main process that follows the mute.
function muteSource() {
  const start = main.indexOf("let systemVolume = null;");
  const end = main.indexOf("// handleCommand acts on a command.");
  return main.slice(start, end);
}

function run() {
  const sent = [];
  const deviceState = { muted: false };
  const sandbox = {
    // The mixer is neither read nor written: the device program owns
    // the microphone and reports where the latch ended up.
    sendState: () => {},
    controlClient: { sendMute: (muted) => sent.push(muted) },
    deviceClient: { state: deviceState },
    console,
    setInterval: () => 0, clearInterval: () => {}, setTimeout: () => 0, clearTimeout: () => {},
    Math, Number,
  };
  sandbox.globalThis = sandbox;
  vm.createContext(sandbox);
  vm.runInContext(muteSource(), sandbox);
  return {
    sandbox,
    sent,
    deviceState,
    // What the panel shows is the panel's own state, which is what the
    // drawer draws and what is published to Home Assistant.
    isMuted: () => sandbox.currentMute(),
    // The device's state arrives.
    deviceSays: (muted) => { deviceState.muted = muted; sandbox.followDeviceMute(); },
    // The watcher follows the device program's state.
    tick: async () => { await sandbox.watchTick(); },
  };
}

test("the panel's mute asks the device program", async () => {
  const { sandbox, sent } = run();

  await sandbox.setMute(true);

  assert.deepEqual(sent, [true], "the device was not told");
});

test("the device's mute is shown", async () => {
  const { sandbox, isMuted, deviceSays } = run();

  deviceSays(true);
  await new Promise((resolve) => setImmediate(resolve));

  assert.equal(isMuted(), true, "the panel does not show the mute");
});

test("the panel does not chase its own mute", async () => {
  const { sandbox, sent, deviceSays } = run();

  // The panel mutes.
  await sandbox.setMute(true);
  assert.deepEqual(sent, [true], "the device was not told");

  // The device says it is muted. That is the panel's own change coming
  // back, and it must not be followed.
  deviceSays(true);
  await new Promise((resolve) => setImmediate(resolve));
  assert.deepEqual(sent, [true], "the panel chased its own change");
});

test("pressing the mute twice does not turn it back on", async () => {
  // The watcher follows the device program's state, and the device
  // program reports the latch. If the watcher read a state the device
  // program had not applied yet, it would take it for a change from
  // outside and undo it — which is a mute that turns itself off.
  const { sandbox, sent, deviceSays, tick } = run();

  await sandbox.setMute(true);
  // The device program confirms the mute, which is what it does when
  // the latch has moved.
  deviceSays(true);
  await tick();
  await new Promise((resolve) => setImmediate(resolve));

  assert.deepEqual(sent, [true], "the device was told twice");
});

test("a microphone muted outside the panel turns the device's switch on", async () => {
  const { sandbox, sent } = run();

  // The microphone is muted by something else.
  sandbox.followMicMute(true);
  assert.deepEqual(sent, [true], "the device was not told");
});
