const test = require("node:test");
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const vm = require("node:vm");

// The mute follows.
//
// The mute is the microphone's, and on the Echo Show 8 it is a hardware
// latch that only the button on top of the unit moves. The latch is
// one-way from software: the button is also the only thing that
// releases it.
//
// So there is only one direction to follow. The device program watches
// the latch and reports where it ended up, and the panel shows that.
// Nothing is written, and so there is nothing to race with: the mute
// that used to turn itself off was a mute that was being written and
// read at the same time.

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
    // indicator draws and what is published to Home Assistant.
    isMuted: () => sandbox.currentMute(),
    // The device's state arrives.
    deviceSays: (muted) => { deviceState.muted = muted; sandbox.followDeviceMute(); },
    // The watcher follows the device program's state.
    tick: async () => { await sandbox.watchTick(); },
  };
}

test("the device's mute is shown", async () => {
  const { isMuted, deviceSays } = run();

  deviceSays(true);
  await new Promise((resolve) => setImmediate(resolve));

  assert.equal(isMuted(), true, "the panel does not show the mute");
});

test("the panel never writes the mute", async () => {
  const { sent } = run();

  // Nothing the panel does can switch the mute, so nothing is sent.
  assert.deepEqual(sent, [], "the panel tried to switch the mute");
});

test("the watcher follows the latch", async () => {
  const { isMuted, deviceState, tick } = run();

  deviceState.muted = true;
  await tick();
  await new Promise((resolve) => setImmediate(resolve));

  assert.equal(isMuted(), true, "the watcher did not follow the latch");
});

test("the mute does not turn itself off", async () => {
  // The mute that used to turn itself off was a mute that was written
  // and read at the same time: the panel wrote the mixer, the watcher
  // read the value from before the write, and took it for a change
  // from outside.
  //
  // Nothing is written now, so there is nothing to undo.
  const { isMuted, deviceSays, tick } = run();

  deviceSays(true);
  await tick();
  await new Promise((resolve) => setImmediate(resolve));
  await tick();
  await new Promise((resolve) => setImmediate(resolve));

  assert.equal(isMuted(), true, "the mute turned itself off");
});
