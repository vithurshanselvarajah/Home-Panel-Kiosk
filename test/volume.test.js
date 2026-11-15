const test = require("node:test");
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const vm = require("node:vm");

// The volume.
//
// The level is the device program's: it applies the level to everything
// it plays, on a curve, and tells the panel where it is. The panel is a
// control surface for it.
//
// So the panel's slider asks the device program for a level, and the
// panel follows the level the device program reports. The mixer is not
// involved: a level applied in two places is a level nobody can reason
// about.
//
// The microphone's mute comes from the device program, because on the
// Echo Show 8 it is the hardware latch's position and only the button on
// the unit moves it.
//
// This is checked by running the code with a device program that
// answers with whatever the test says it should.

// The state the test pretends is there.
//
// The mute comes from the device program: on the Echo Show 8 it is the
// hardware latch's position, and the device program reports it.
let deviceMuted = false;
let deviceVolume = 50;

// The main process's source.
const root = path.join(__dirname, "..");
const main = fs.readFileSync(path.join(root, "src", "main.js"), "utf8");

// The part of the main process that reads and watches the volume.
function volumeSource() {
  // The volume the panel is showing, and the reading of it.
  const holding = main.indexOf("let systemVolume = null;");
  const start = main.indexOf("// refreshVolume reads the microphone's mute.");
  const end = main.indexOf("// handleCommand acts on a command.");
  return main.slice(holding, end);
}

// run runs the volume handling with a device program that answers.
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
    // The device program's state carries the mute, which is the
    // hardware latch's position. The configuration is where the level
    // is kept, so the panel writes the level there.
    deviceClient: { state: deviceState, sendConfig: (config) => { configs.push(config); } },
    // The control client is what tells the device program. It answers
    // with the level it now holds, which is what the device program
    // would have applied.
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
    // The settings the device configuration is built from.
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
  // The watcher reads the mute on a timer, and the test runs the timer
  // by hand rather than waiting for it.
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

  // Something outside the panel moved the level: the physical buttons,
  // or Home Assistant.
  sandbox.followDeviceVolume(25);

  assert.equal(sandbox.currentVolume(), 25, "the panel did not follow the level");
  assert.equal(sent.length, 1, "the drawer was not told about the level");
});

test("the panel does not chase a level it set itself", async () => {
  deviceVolume = 50;
  const { sandbox, sent } = run();

  // The panel's own change: the device program now holds what the panel
  // asked for, so there is nothing to follow.
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

  // Something outside the panel mutes the microphone: the button on the
  // unit, which is the hardware latch, and the device program reports
  // where the latch ended up.
  deviceMuted = true;
  sandbox.deviceClient.state.muted = true;
  await sandbox.tick();

  assert.equal(sandbox.currentMute(), true, "the panel did not follow the mute");
  assert.equal(sent.length, 1, "the drawer was not told about the mute");
});

test("the mute is the microphone's and comes from the device", async () => {
  // A person who mutes the panel wants the room to stop hearing them,
  // and they still want to hear the music.
  //
  // The mute is the hardware latch's and the latch is one-way from
  // software, so the panel follows it rather than setting it.
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
