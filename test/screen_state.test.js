const test = require("node:test");
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const vm = require("node:vm");

// The screen's state.
//
// The screen switch in Home Assistant says whether the screen is on.
// It used to say what the panel last asked for, which is not the same
// thing: the display driver switches the backlight off when the panel
// is blanked, and nothing else the panel does would know.
//
// So the screen's state is watched, and read from the driver, which says
// in bl_power whether the backlight is powered.
//
// The automatic brightness must not turn the screen back on either.
// Writing a level to the backlight turns the PWM on, and a PWM that is
// on lights the screen. So a screen that was turned off would come back
// up every time the room got bright.
//
// These are checked by running the code against a fake backlight, so
// what is checked is what reaches the driver and what is published.

const root = path.join(__dirname, "..");
const main = fs.readFileSync(path.join(root, "src", "main.js"), "utf8");

// screenSource is the part of the script that watches the screen and
// sets the automatic brightness.
//
// The driver's answer about the backlight is in it, because the watcher
// asks for it.
function screenSource() {
  const start = main.indexOf("// backlightIsPowered reads the driver's own answer");
  const end = main.indexOf("// systemVolume is the volume of the mixer.");
  assert.ok(start > 0, "the driver is not asked about the backlight");
  assert.ok(end > start, "the screen's watcher is not where it should be");
  return main.slice(start, end);
}

// automaticSource is the part that sets the automatic brightness.
function automaticSource() {
  const start = main.indexOf("// setAutomaticBrightness sets the backlight to the level the room's");
  const end = main.indexOf("// AUTOMATIC_BRIGHTNESS_SMOOTH is how much of the way to the level the");
  assert.ok(start > 0, "the automatic brightness is not in the script");
  assert.ok(end > start, "the automatic brightness is not where it should be");
  return main.slice(start, end);
}

// harness runs the screen's watcher against a fake backlight.
//
// The backlight answers the way the driver's does: bl_power says
// whether it is powered, and the brightness is the level that was last
// written.
function harness({ powered = true, brightness = 500, level = 100 } = {}) {
  const written = [];
  const states = [];
  let isPowered = powered;
  let current = brightness;

  const sandbox = {
    fsSync: {
      readdirSync: () => ["backlight-lcd0"],
      readFileSync: (file) => {
        if (file.endsWith("max_brightness")) return "1023";
        if (file.endsWith("bl_power")) return isPowered ? "0" : "4";
        if (file.endsWith("brightness")) return String(current);
        return "0";
      },
      existsSync: (file) => file.endsWith("bl_power"),
    },
    fs: {
      writeFile: async (file, content) => {
        if (!file.endsWith("brightness")) return;
        current = Number(content);
        written.push(current);
        // Writing a level turns the PWM on: the light is on afterwards.
        if (current > 0) isPowered = true;
      },
      readFile: async (file) => {
        if (file.endsWith("max_brightness")) return "1023";
        if (file.endsWith("brightness")) return String(current);
        return "";
      },
      readdir: async (dir) => (dir === "/sys/class/backlight" ? ["backlight-lcd0"] : []),
    },
    displayIsOff: false,
    brightnessBeforeOff: null,
    relighting: false,
    sendState: (force) => states.push({ force }),
    setTimeout: (fn, ms) => { fn(); return 0; },
    clearTimeout: () => {},
    console,
    path,
    Math, Number, Boolean, String, Object, Array, Promise,
  };
  sandbox.globalThis = sandbox;
  vm.createContext(sandbox);
  vm.runInContext(screenSource(), sandbox);
  vm.runInContext(automaticSource(), sandbox);

  return {
    ...sandbox,
    written,
    states,
    // The backlight's own answer, for a test to change.
    setPowered: (on) => { isPowered = on; },
    isPowered: () => isPowered,
    brightness: () => current,
    // The panel's own answer. The script's variables live in the
    // sandbox, so they are read and written through it rather than
    // through the object the sandbox was copied into.
    displayIsOff: () => vm.runInContext("displayIsOff", sandbox),
    // The script's own variables live in its scope, so they are set
    // through the context rather than on the sandbox object.
    relighting: (on) => { vm.runInContext(`relighting = ${on};`, sandbox); },
  };
}

test("a screen that is off is published as off", () => {
  const screen = harness({ powered: false });

  screen.watchScreen();

  assert.equal(screen.displayIsOff(), true, "the screen is off, but the panel says it is on");
  assert.equal(screen.states.length, 1, "the state was not published");
});

test("a screen that is on is published as on", () => {
  const screen = harness({ powered: true });

  screen.watchScreen();

  assert.equal(screen.displayIsOff(), false, "the screen is on, but the panel says it is off");
  assert.equal(screen.states.length, 0, "the state was published when nothing had changed");
});

test("a screen that goes off on its own is noticed", () => {
  const screen = harness({ powered: true });
  screen.watchScreen();

  // Something else turned the screen off: the display driver blanking
  // the panel, or the automatic brightness, or a person.
  screen.setPowered(false);
  screen.watchScreen();

  assert.equal(screen.displayIsOff(), true, "the screen went off and the panel did not notice");
});

test("the screen's state is watched for as long as the panel runs", () => {
  assert.ok(
    main.includes("watchScreenState();"),
    "the screen's state is never watched",
  );
  assert.ok(
    main.includes("stopWatchingScreen();"),
    "the watcher is never stopped",
  );
});

test("the automatic brightness does not turn the screen back on", async () => {
  // The screen is off, and the room gets bright.
  const screen = harness({ powered: false });

  await screen.setAutomaticBrightness(900);

  assert.equal(screen.written.length, 0, "the automatic brightness wrote to a screen that was off");
});

test("the automatic brightness takes over when the screen is on", async () => {
  const screen = harness({ powered: true, brightness: 500 });

  await screen.setAutomaticBrightness(900);

  assert.equal(screen.written.length, 1, "the automatic brightness did not set the level");
  // A quarter of the way from where it was to where it should be. The
  // level is the driver's own, not a percentage: the automatic
  // brightness is given a level and writes a level.
  assert.equal(screen.written[0], 600, "the level did not move a quarter of the way");
});

test("the automatic brightness waits while the light is being brought back", async () => {
  // The light is at full for a moment before the level it should be
  // at. The automatic brightness must not take the level over from under
  // the relight.
  const screen = harness({ powered: true, brightness: 500 });
  screen.relighting(true);

  await screen.setAutomaticBrightness(900);

  assert.equal(screen.written.length, 0, "the automatic brightness wrote during the relight");
});

test("the watcher waits while the light is being brought back", () => {
  const screen = harness({ powered: true });
  screen.relighting(true);

  screen.watchScreen();

  assert.equal(screen.states.length, 0, "the state was published during the relight");
});

test("a wake word turns the screen on", () => {
  // The device program says a turn has started.
  assert.ok(
    main.includes("followDeviceTurn();"),
    "a turn does not turn the screen on",
  );
  assert.ok(
    main.includes("function followDeviceTurn()"),
    "there is nothing that turns the screen on for a turn",
  );
  // The turn is the device program saying the assistant is listening,
  // thinking or speaking.
  assert.ok(
    main.includes("state.listening || state.thinking || state.speaking"),
    "the panel does not know when a turn is running",
  );
});

test("a notification turns the screen on", () => {
  // The notice is put on the screen, so the screen is turned on first.
  const start = main.indexOf("function showNotice(");
  const body = main.slice(start, start + 600);
  assert.ok(
    body.includes("turnDisplayOn();"),
    "a notice arrives on a screen that is dark",
  );
});

test("the screen comes back with the relight the panel needs", () => {
  // The screen is turned on through the backlight on this panel, and the
  // backlight has to be brought back at full for a moment or the panel
  // does not follow levels again.
  assert.ok(
    main.includes("await relight(Math.round((await backlightMaximum()) * wanted / 100));"),
    "the screen does not come back with a relight",
  );
});
