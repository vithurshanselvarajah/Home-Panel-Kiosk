const test = require("node:test");
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const vm = require("node:vm");

// The panel's own mute.
//
// The mute is the microphone's. The device program owns the microphone —
// on the Echo Show 8 that is the hardware latch — so the panel tells the
// device program, and the device program moves the latch and reports
// where it ended up.
//
// The mixer is not written: the microphone is cut at the board, and
// there is no mixer control for it.

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
  const sandbox = {
    sendState: () => {},
    controlClient: { sendMute: (muted) => sent.push(muted) },
    console,
    setInterval: () => 0, clearInterval: () => {}, setTimeout: () => 0, clearTimeout: () => {},
    Math, Number,
  };
  sandbox.globalThis = sandbox;
  vm.createContext(sandbox);
  vm.runInContext(muteSource(), sandbox);
  // What the panel shows is the panel's own state.
  return { sandbox, sent, isMuted: () => sandbox.currentMute() };
}

test("the panel's mute tells the device program", async () => {
  const { sandbox, sent } = run();
  await sandbox.setMute(true);
  assert.deepEqual(sent, [true], "the device program was not told");
});

test("unmuting works the other way round", async () => {
  const { sandbox, sent } = run();
  await sandbox.setMute(true);
  await sandbox.setMute(false);
  assert.deepEqual(sent, [true, false], "the device program was not told");
});

test("the panel shows the mute it asked for", async () => {
  const { sandbox, isMuted } = run();
  await sandbox.setMute(true);
  assert.equal(isMuted(), true, "the panel does not show the mute");
  await sandbox.setMute(false);
  assert.equal(isMuted(), false, "the panel does not show the unmute");
});
