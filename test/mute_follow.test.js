const test = require("node:test");
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const vm = require("node:vm");

// The mute follows.
//
// The mute is the same thing in two places: the device's switch, which
// Home Assistant reads and writes, and the system's microphone, which is
// what actually stops hearing the room.
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
  let micMuted = false;
  // The mixer the test pretends is there. It remembers the value that
  // was asked for and does not answer with it until the write has
  // landed, which is what a real mixer does.
  let pending = null;
  const deviceState = { muted: false };
  const sandbox = {
    systemVolumePercent: async () => 50,
    setSystemVolume: async () => true,
    // The mixer answers with what it has been told, and a write that
    // has not landed yet is not what it says.
    systemMicMuted: async () => micMuted,
    setSystemMicMuted: async (muted) => {
      // The write lands on the next turn of the event loop, which is
      // what a real mixer does: the command goes out and the mixer
      // answers afterwards.
      await new Promise((resolve) => setImmediate(resolve));
      micMuted = muted;
      return true;
    },
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
    isMuted: () => micMuted,
    // The device's state arrives.
    deviceSays: (muted) => { deviceState.muted = muted; sandbox.followDeviceMute(); },
    // The watcher reads the mixer.
    tick: async () => { await sandbox.watchTick(); },

  };
}

test("the device's mute mutes the microphone", async () => {
  const { isMuted, deviceSays } = run();
  deviceSays(true);
  await new Promise((resolve) => setImmediate(resolve));
  await new Promise((resolve) => setImmediate(resolve));
  assert.equal(isMuted(), true, "the microphone was not muted");
});

test("the panel does not chase its own mute", async () => {
  const { sandbox, sent, isMuted, deviceSays } = run();

  // The panel mutes.
  await sandbox.setMute(true);
  assert.equal(isMuted(), true, "the microphone was not muted");
  assert.deepEqual(sent, [true], "the device was not told");

  // The device says it is muted. That is the panel's own change coming
  // back, and it must not be followed.
  deviceSays(true);
  await new Promise((resolve) => setImmediate(resolve));
  assert.deepEqual(sent, [true], "the panel chased its own change");
});

test("pressing the mute twice does not turn it back on", async () => {
  // The button writes the mixer and the watcher reads it. If the
  // watcher reads the value from before the button wrote it, it takes
  // it for a change from outside and undoes it — which is a mute that
  // turns itself off.
  const { sandbox, sent, isMuted, tick } = run();

  await sandbox.setMute(true);
  // The watcher reads the mixer while the button's write is on its way.
  await tick();
  await new Promise((resolve) => setImmediate(resolve));

  assert.equal(isMuted(), true, "the mute turned itself off");
  assert.deepEqual(sent, [true], "the device was told twice");
});

test("a microphone muted outside the panel turns the device's switch on", async () => {
  const { sandbox, sent } = run();

  // The microphone is muted by something else.
  sandbox.followMicMute(true);
  assert.deepEqual(sent, [true], "the device was not told");
});
