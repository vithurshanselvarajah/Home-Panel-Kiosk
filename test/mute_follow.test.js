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
    deviceState,

    isMuted: () => sandbox.currentMute(),

    deviceSays: (muted) => { deviceState.muted = muted; sandbox.followDeviceMute(); },

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

  const { isMuted, deviceSays, tick } = run();

  deviceSays(true);
  await tick();
  await new Promise((resolve) => setImmediate(resolve));
  await tick();
  await new Promise((resolve) => setImmediate(resolve));

  assert.equal(isMuted(), true, "the mute turned itself off");
});
