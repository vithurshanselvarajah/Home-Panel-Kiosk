const test = require("node:test");
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const vm = require("node:vm");

// The backlight, and the panel's driver.
//
// The driver runs the panel's backlight from a PWM on the main chip. A
// level of zero is not a level of light: the driver switches that PWM
// off instead of running it at no duty, and once it has been switched
// off and on again the panel stops following the levels written to it.
// The screen comes back at a fixed brightness and the brightness
// control is dead until the machine is rebooted.
//
// So the brightness a person sets never reaches zero, and a screen that
// comes back from off is brought back at full for a moment before the
// level it should be at.
//
// These are run against a fake backlight, so what is checked is what
// reaches the driver and in what order, rather than how the code reads.

const root = path.join(__dirname, "..");
const main = fs.readFileSync(path.join(root, "src", "main.js"), "utf8");

// The backlight's own code, taken out of the script.
//
// It is the part between the levels and the screen's off, so it has
// everything that writes the backlight.
function backlightSource() {
  const start = main.indexOf("const BACKLIGHT_FLOOR = 1;");
  const end = main.indexOf("// setBacklightOff turns the screen dark");
  assert.ok(start > 0, "the backlight's levels are not in the script");
  assert.ok(end > start, "the backlight's code is not where it should be");
  return main.slice(start, end);
}

// A backlight to write to.
//
// It is the driver's own shape: a directory with a max_brightness and a
// brightness in it, and a bl_power that says whether the backlight is
// powered. What is written to the brightness is recorded, so a test can
// see the levels in the order they were written.
function harness({ max = 1023, powered = true } = {}) {
  const written = [];
  const delays = [];
  let current = 0;
  let isPowered = powered;

  const fakeFs = {
    readdir: async (dir) => (dir === "/sys/class/backlight" ? ["backlight-lcd0"] : []),
    readFile: async (file) => {
      if (file.endsWith("max_brightness")) return String(max);
      if (file.endsWith("bl_power")) return isPowered ? "0" : "4";
      if (file.endsWith("brightness")) return String(current);
      throw new Error(`no such file: ${file}`);
    },
    writeFile: async (file, content) => {
      if (!file.endsWith("brightness")) throw new Error(`no such file: ${file}`);
      current = Number(content);
      // Writing a level does not switch the PWM back on by itself: the
      // driver only powers it when it is asked to.
      if (current > 0) isPowered = true;
      written.push(current);
    },
  };

  const sandbox = {
    fs: fakeFs,
    fsSync: {
      readdirSync: () => ["backlight-lcd0"],
      readFileSync: (file) => {
        if (file.endsWith("max_brightness")) return String(max);
        if (file.endsWith("bl_power")) return isPowered ? "0" : "4";
        return String(current);
      },
      existsSync: (file) => file.endsWith("bl_power"),
    },
    // The hold before the level is written is recorded rather than
    // waited for, so a test can see how long it was.
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
    // isOff is whether the backlight is believed to be switched off.
    // It is a binding in the script rather than a property of the
    // sandbox, so it is read by running a line of the script.
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

  // The screen's own off writes zero: that is how the screen is turned
  // off, and it is the only thing that is allowed to.
  await backlight.writeBacklightLevel(0);

  assert.deepEqual(backlight.written, [0], "the screen's off did not write zero");
});

test("the driver's own range is read rather than assumed", async () => {
  const backlight = harness({ max: 255 });

  await backlight.writeBacklight(50);

  assert.deepEqual(backlight.written, [128], "the level was not the driver's own");
});

test("a backlight the driver says is off is brought back before the level", async () => {
  // The backlight can be switched off from outside the panel: the
  // display driver does it when the panel is blanked. The brightness
  // file does not show it, because it holds the level that was last
  // asked for. The driver says so in bl_power, and that is what is
  // asked.
  const backlight = harness({ powered: false });

  await backlight.writeBacklight(50);

  assert.deepEqual(backlight.written, [1000, 512], "the light was not brought back at full first");
});

test("a backlight that is on is given the level on its own", async () => {
  const backlight = harness({ powered: true });

  await backlight.writeBacklight(50);

  assert.deepEqual(backlight.written, [512], "the light was brought back when it did not need it");
});
