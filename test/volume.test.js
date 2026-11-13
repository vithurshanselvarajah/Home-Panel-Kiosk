const test = require("node:test");
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const vm = require("node:vm");

// The volume.
//
// The panel shows the volume of the system mixer, and the volume can be
// changed by something other than the panel: the keyboard's media keys,
// a desktop's own control, an automation. So the panel follows the
// mixer while the drawer is open.
//
// This is checked by running the code with a mixer that answers with
// whatever the test says it should.

// The mixer the test pretends is there.
let mixerValue = 50;

// The main process's source.
const root = path.join(__dirname, "..");
const main = fs.readFileSync(path.join(root, "src", "main.js"), "utf8");

// The part of the main process that reads and watches the volume.
function volumeSource() {
  // The volume the panel is showing, and the reading of it.
  const holding = main.indexOf("let systemVolume = null;");
  const reading = main.indexOf("function currentVolume() {");
  const start = main.indexOf("// refreshVolume reads the volume of the mixer.");
  const end = main.indexOf("// handleCommand acts on a command.");
  return main.slice(holding, end);
}

// run runs the volume handling with a mixer that answers.
function run() {
  const sent = [];
  const timers = [];
  const sandbox = {
    systemVolumePercent: async () => mixerValue,
    setSystemVolume: async (percent) => { mixerValue = percent; return true; },
    sendState: (force) => sent.push({ force }),
    console,
    setInterval: (fn, ms) => { timers.push({ fn, ms, alive: true }); return timers.length - 1; },
    clearInterval: (id) => { if (timers[id]) timers[id].alive = false; },
    setTimeout: () => 0,
    clearTimeout: () => {},
    Math,
    Number,
  };
  sandbox.globalThis = sandbox;
  vm.createContext(sandbox);
  vm.runInContext(volumeSource(), sandbox);
  // The watcher reads the mixer on a timer, and the test runs the timer
  // by hand rather than waiting for it.
  sandbox.tick = async () => {
    for (const timer of timers) {
      if (timer.alive) await timer.fn();
    }
  };
  return { sandbox, sent };
}

test("the volume is read when it is refreshed", async () => {
  mixerValue = 42;
  const { sandbox } = run();
  await sandbox.refreshVolume();
  assert.equal(sandbox.currentVolume(), 42, "the volume was not read");
});

test("the drawer follows a volume changed outside the panel", async () => {
  mixerValue = 50;
  const { sandbox, sent } = run();

  // The watcher is running.
  sandbox.watchVolume();
  sent.length = 0;

  // Something outside the panel changes the volume.
  mixerValue = 25;
  await sandbox.tick();

  assert.equal(sandbox.currentVolume(), 25, "the panel did not follow the volume");
  assert.equal(sent.length, 1, "the drawer was not told about the volume");
});

test("the panel does not chase a volume it set itself", async () => {
  mixerValue = 50;
  const { sandbox, sent } = run();

  sandbox.watchVolume();
  sent.length = 0;

  // The panel's own change: the mixer has the value the panel asked
  // for, so there is nothing to follow.
  await sandbox.setVolume(70);
  assert.equal(sandbox.currentVolume(), 70, "the panel's own change did not take");

  sent.length = 0;
  await sandbox.tick();
  assert.equal(sent.length, 0, "the panel chased its own change");
});

test("the watcher stops when the drawer closes", async () => {
  mixerValue = 50;
  const { sandbox, sent } = run();

  sandbox.watchVolume();
  sandbox.stopWatchingVolume();
  sent.length = 0;

  mixerValue = 25;
  await sandbox.tick();
  assert.equal(sent.length, 0, "the drawer was told about a volume it was not showing");
});
