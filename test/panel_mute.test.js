const test = require("node:test");
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const vm = require("node:vm");

// The panel's own mute.
//
// The mute is the microphone's. The panel mutes the system's microphone
// — it owns the mixer — and tells the device program, which owns the
// microphone, so that Home Assistant's switch and the microphone are
// the same thing.

const root = path.join(__dirname, "..");
const main = fs.readFileSync(path.join(root, "src", "main.js"), "utf8");

// The part of the main process that mutes.
function muteSource() {
  const start = main.indexOf("let systemVolume = null;");
  const end = main.indexOf("// handleCommand acts on a command.");
  return main.slice(start, end);
}

function run() {
  const sent = [];
  let micMuted = false;
  const sandbox = {
    systemVolumePercent: async () => 50,
    setSystemVolume: async () => true,
    systemMicMuted: async () => micMuted,
    setSystemMicMuted: async (muted) => { micMuted = muted; return true; },
    sendState: () => {},
    controlClient: { sendMute: (muted) => sent.push(muted) },
    console,
    setInterval: () => 0, clearInterval: () => {}, setTimeout: () => 0, clearTimeout: () => {},
    Math, Number,
  };
  sandbox.globalThis = sandbox;
  vm.createContext(sandbox);
  vm.runInContext(muteSource(), sandbox);
  return { sandbox, sent, isMuted: () => micMuted };
}

test("the panel's mute mutes the microphone and tells the device", async () => {
  const { sandbox, sent, isMuted } = run();
  await sandbox.setMute(true);
  assert.equal(isMuted(), true, "the microphone was not muted");
  assert.deepEqual(sent, [true], "the device program was not told");
});

test("unmuting works the other way round", async () => {
  const { sandbox, sent, isMuted } = run();
  await sandbox.setMute(true);
  await sandbox.setMute(false);
  assert.equal(isMuted(), false, "the microphone was not unmuted");
  assert.deepEqual(sent, [true, false], "the device program was not told");
});
