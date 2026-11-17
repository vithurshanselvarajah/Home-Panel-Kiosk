const test = require("node:test");
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const vm = require("node:vm");

let deviceMuted = false;
let deviceVolume = 50;

const root = path.join(__dirname, "..");
const main = fs.readFileSync(path.join(root, "src", "main.js"), "utf8");

function volumeSource() {

  const holding = main.indexOf("let systemVolume = null;");
  const start = main.indexOf("async function refreshVolume() {");
  const end = main.indexOf("async function handleCommand(command) {");
  return main.slice(holding, end);
}

function run() {
  const sent = [];
  const configs = [];
  const timers = [];
  const requested = [];
  const deviceState = { muted: deviceMuted };
  const sandbox = {
    sendState: (force) => sent.push({ force }),
    ensureOverlayView: () => {},
    resizeOverlay: () => {},
    sendToOverlay: () => {},

    deviceClient: { state: deviceState, sendConfig: (config) => { configs.push(config); } },

    controlClient: {
      connected: true,
      sendVolume: (percent) => {
        requested.push(percent);
        deviceVolume = percent;
        return true;
      },
      sendMute: (muted) => {
        deviceMuted = muted;
        deviceState.muted = muted;
        return true;
      },
    },
    console,
    setInterval: (fn, ms) => { timers.push({ fn, ms, alive: true }); return timers.length - 1; },
    clearInterval: (id) => { if (timers[id]) timers[id].alive = false; },
    setTimeout: () => 0,
    clearTimeout: () => {},

    settings: { musicEnabled: true, musicName: "home-panel", musicUnpaired: false, cameraEnabled: false, voiceAudioInput: "", voiceAudioOutput: "" },
    settingBoolean: (value) => Boolean(value),
    voiceMonitorMic: false,
    voiceTestSound: 0,
    Math,
    Number,
  };
  sandbox.globalThis = sandbox;
  vm.createContext(sandbox);
  vm.runInContext(volumeSource(), sandbox);

  sandbox.tick = async () => {
    for (const timer of timers) {
      if (timer.alive) await timer.fn();
    }
  };
  return { sandbox, sent, configs, requested };
}

test("the panel asks the device program for a volume", async () => {
  deviceVolume = 50;
  const { sandbox, requested } = run();

  await sandbox.setVolume(70);

  assert.deepEqual(requested, [70], "the device program was not asked");
  assert.equal(sandbox.currentVolume(), 70, "the panel does not show the level it asked for");
});

test("the panel follows the level the device program reports", async () => {
  deviceVolume = 50;
  const { sandbox, sent } = run();

  sandbox.followDeviceVolume(25);

  assert.equal(sandbox.currentVolume(), 25, "the panel did not follow the level");
  assert.equal(sent.length, 1, "the drawer was not told about the level");
});

test("the panel does not chase a level it set itself", async () => {
  deviceVolume = 50;
  const { sandbox, sent } = run();

  await sandbox.setVolume(70);
  assert.equal(sandbox.currentVolume(), 70, "the panel's own change did not take");

  sent.length = 0;
  sandbox.followDeviceVolume(70);
  assert.equal(sent.length, 0, "the panel chased its own change");
});

test("the mute is read with the volume", async () => {
  deviceMuted = true;
  const { sandbox } = run();
  await sandbox.refreshVolume();
  assert.equal(sandbox.currentMute(), true, "the mute was not read");
});

test("the drawer follows a mute changed outside the panel", async () => {
  deviceMuted = false;
  const { sandbox, sent } = run();

  sandbox.watchVolume();
  sent.length = 0;

  deviceMuted = true;
  sandbox.deviceClient.state.muted = true;
  await sandbox.tick();

  assert.equal(sandbox.currentMute(), true, "the panel did not follow the mute");
  assert.equal(sent.length, 1, "the drawer was not told about the mute");
});

test("the mute is the microphone's and comes from the device", async () => {

  deviceMuted = false;
  const { sandbox } = run();

  sandbox.deviceClient.state.muted = true;
  sandbox.followDeviceMute();

  assert.equal(sandbox.currentMute(), true, "the panel does not say it is muted");
});

test("the watcher stops when the drawer closes", async () => {
  deviceMuted = false;
  const { sandbox, sent } = run();

  sandbox.watchVolume();
  sandbox.stopWatchingVolume();
  sent.length = 0;

  deviceMuted = true;
  await sandbox.tick();
  assert.equal(sent.length, 0, "the drawer was told about a volume it was not showing");
});
