const test = require("node:test");
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const vm = require("node:vm");

// The panel's mute.
//
// The mute is the microphone's, and on the Echo Show 8 it is a hardware
// latch: the button on top of the unit flips it, and the latch is
// one-way from software — the button is also the only thing that
// releases it.
//
// So the panel cannot switch the mute. It follows the latch and says
// where the latch is, on the screen, so that a muted device does not
// look like a live one.
//
// The mixer is not written: the microphone is cut at the board, and
// there is no mixer control for it.

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
    ensureOverlayView: () => {},
    resizeOverlay: () => {},
    sendToOverlay: () => {},
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
    // What the panel shows is the panel's own state.
    isMuted: () => sandbox.currentMute(),
    // The device program's state arrives.
    deviceSays: (muted) => { deviceState.muted = muted; sandbox.followDeviceMute(); },
    // The watcher follows the device program's state.
    tick: async () => { await sandbox.watchTick(); },
  };
}

test("the panel cannot switch the mute", () => {
  // The latch is one-way from software, so there is nothing for the
  // panel to ask for that the hardware would do.
  assert.equal(sandbox_setMute(), undefined, "the panel still has a way to mute");
});

function sandbox_setMute() {
  // The panel's setMute is gone; this is what is left of it.
  const { sandbox } = run();
  return sandbox.setMute;
}

test("the device's mute is shown", async () => {
  const { isMuted, deviceSays } = run();

  deviceSays(true);
  await new Promise((resolve) => setImmediate(resolve));

  assert.equal(isMuted(), true, "the panel does not show the mute");
});

test("the panel follows the latch when it is released", async () => {
  const { isMuted, deviceSays } = run();

  deviceSays(true);
  await new Promise((resolve) => setImmediate(resolve));
  deviceSays(false);
  await new Promise((resolve) => setImmediate(resolve));

  assert.equal(isMuted(), false, "the panel still shows the mute");
});

test("the watcher follows the latch", async () => {
  const { isMuted, tick } = run();

  tick();
  await new Promise((resolve) => setImmediate(resolve));

  assert.equal(isMuted(), false, "the panel shows a mute that is not there");
});
