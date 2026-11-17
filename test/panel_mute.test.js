const test = require("node:test");
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const vm = require("node:vm");

const root = path.join(__dirname, "..");
const main = fs.readFileSync(path.join(root, "src", "main.js"), "utf8");

function muteSource() {
  const start = main.indexOf("let systemVolume = null;");
  const end = main.indexOf("async function handleCommand(command) {");
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

    isMuted: () => sandbox.currentMute(),

    deviceSays: (muted) => { deviceState.muted = muted; sandbox.followDeviceMute(); },

    tick: async () => { await sandbox.watchTick(); },
  };
}

test("the panel cannot switch the mute", () => {

  assert.equal(sandbox_setMute(), undefined, "the panel still has a way to mute");
});

function sandbox_setMute() {

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
