const test = require("node:test");
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const vm = require("node:vm");

const root = path.join(__dirname, "..");
const main = fs.readFileSync(path.join(root, "src", "main.js"), "utf8");

function backlightSource() {

  const start = main.indexOf("const BACKLIGHT_FLOOR = 1;");
  const end = main.indexOf("async function setBacklightOff(off) {");
  assert.ok(start > 0, "the backlight's levels are not in the script");
  assert.ok(end > start, "the backlight's code is not where it should be");
  const reader = main.indexOf("function backlightRawLevel()");
  assert.ok(reader > 0, "the backlight's level is not read");
  const readerEnd = main.indexOf("function currentBrightness() {");
  assert.ok(readerEnd > reader, "the backlight's level is not where it should be");
  return main.slice(start, end) + main.slice(reader, readerEnd);
}

function harness({ max = 1023, powered = true, brightness = 512 } = {}) {
  const written = [];
  const delays = [];

  let current = powered ? brightness : 0;

  const fakeFs = {
    readdir: async (dir) => (dir === "/sys/class/backlight" ? ["backlight-lcd0"] : []),
    readFile: async (file) => {
      if (file.endsWith("max_brightness")) return String(max);
      if (file.endsWith("brightness")) return String(current);
      throw new Error(`no such file: ${file}`);
    },
    writeFile: async (file, content) => {
      if (!file.endsWith("brightness")) throw new Error(`no such file: ${file}`);
      current = Number(content);
      written.push(current);
    },
  };

  const sandbox = {
    fs: fakeFs,
    fsSync: {
      readdirSync: () => ["backlight-lcd0"],
      readFileSync: (file) => {
        if (file.endsWith("max_brightness")) return String(max);
          return String(current);
      },
    },

    setTimeout: (fn, ms) => {
      delays.push(ms);
      fn();
      return 0;
    },
    clearTimeout: () => {},
    console,
    path,
    Math, Number, Promise, String, Object, Array, Boolean,
  };
  sandbox.globalThis = sandbox;
  vm.createContext(sandbox);
  vm.runInContext(backlightSource(), sandbox);

  return {
    ...sandbox,
    written,
    delays,

    isOff: () => vm.runInContext("backlightIsOff", sandbox),
  };
}

test("a brightness of zero never reaches the driver", async () => {
  const backlight = harness();

  await backlight.writeBacklight(0);

  assert.equal(backlight.written.length, 1, "the brightness was not written");
  assert.notEqual(backlight.written[0], 0, "the driver was given a level of zero, which switches its PWM off");
  assert.equal(backlight.written[0], 1, "the lowest level is the one above zero");
});

test("a brightness of one is above the floor", async () => {
  const backlight = harness();

  await backlight.writeBacklight(1);

  assert.equal(backlight.written.length, 1, "the brightness was not written");
  assert.ok(backlight.written[0] > 0, "the driver was given a level of zero");
});

test("a brightness of half is half of the driver's range", async () => {
  const backlight = harness();

  await backlight.writeBacklight(50);

  assert.deepEqual(backlight.written, [512], "the brightness is not half of the range");
});

test("a brightness above the top is the top", async () => {
  const backlight = harness();

  await backlight.writeBacklight(100);

  assert.deepEqual(backlight.written, [1023], "the brightness is not the top of the range");
});

test("a brightness below zero is the floor", async () => {
  const backlight = harness();

  await backlight.writeBacklight(-10);

  assert.ok(backlight.written[0] > 0, "the driver was given a level of zero");
});

test("the light is brought back at full before the level it should be at", async () => {
  const backlight = harness();

  await backlight.relight(512);

  assert.deepEqual(backlight.written, [1000, 512], "the light was not brought back at full first");
});

test("the light is held at full long enough to take effect", async () => {
  const backlight = harness();

  await backlight.relight(512);

  assert.deepEqual(backlight.delays, [10], "the light was not held at full for ten milliseconds");
});

test("a screen that comes back on leaves the light on", async () => {
  const backlight = harness();

  await backlight.relight(512);

  assert.equal(backlight.isOff(), false, "the light is still believed to be off after it was brought back");
});

test("the screen's off is allowed to be zero", async () => {
  const backlight = harness();

  await backlight.writeBacklightLevel(0);

  assert.deepEqual(backlight.written, [0], "the screen's off did not write zero");
});

test("the driver's own range is read rather than assumed", async () => {
  const backlight = harness({ max: 255 });

  await backlight.writeBacklight(50);

  assert.deepEqual(backlight.written, [128], "the level was not the driver's own");
});

test("a backlight the driver says is off is brought back before the level", async () => {

  const backlight = harness({ powered: false });

  await backlight.writeBacklight(50);

  assert.deepEqual(backlight.written, [1000, 512], "the light was not brought back at full first");
});

test("a backlight that is on is given the level on its own", async () => {
  const backlight = harness({ powered: true });

  await backlight.writeBacklight(50);

  assert.deepEqual(backlight.written, [512], "the light was brought back when it did not need it");
});
