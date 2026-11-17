const test = require("node:test");
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const vm = require("node:vm");

const root = path.join(__dirname, "..");
const main = fs.readFileSync(path.join(root, "src", "main.js"), "utf8");

function screenSource() {
  const start = main.indexOf("function backlightIsPowered() {");
  const end = main.indexOf("let systemVolume = null;");
  assert.ok(start > 0, "the backlight's state is not read");
  assert.ok(end > start, "the screen's watcher is not where it should be");
  return main.slice(start, end);
}

function automaticSource() {
  const start = main.indexOf("async function setAutomaticBrightness(level) {");
  const end = main.indexOf("const AUTOMATIC_BRIGHTNESS_SMOOTH = 0.25;");
  assert.ok(start > 0, "the automatic brightness is not in the script");
  assert.ok(end > start, "the automatic brightness is not where it should be");
  return main.slice(start, end);
}

function harness({ powered = true, brightness = 500, level = 100 } = {}) {
  const written = [];
  const states = [];
  let isPowered = powered;
  let current = brightness;

  if (!powered) current = 0;

  const sandbox = {
    fsSync: {
      readdirSync: () => ["backlight-lcd0"],
      readFileSync: (file) => {
        if (file.endsWith("max_brightness")) return "1023";
        if (file.endsWith("brightness")) return String(current);
        return "0";
      },
    },
    fs: {
      writeFile: async (file, content) => {
        if (!file.endsWith("brightness")) return;
        current = Number(content);
        written.push(current);

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

    ensureOverlayView: () => {},
    resizeOverlay: () => {},
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

    setPowered: (on) => { isPowered = on; current = on ? current || 500 : 0; },
    isPowered: () => isPowered,
    brightness: () => current,

    displayIsOff: () => vm.runInContext("displayIsOff", sandbox),

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

  screen.setPowered(false);
  screen.watchScreen();

  assert.equal(screen.displayIsOff(), true, "the screen went off and the panel did not notice");
});

test("a backlight at zero is a screen that is off", () => {

  const code = main.replace(/\/\/[^\n]*/g, "");
  assert.ok(
    !code.includes("bl_power"),
    "the screen's state is read from an attribute that does not say",
  );
  assert.ok(
    main.includes("const level = backlightRawLevel();"),
    "the screen's state is not read from the level",
  );
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

  const screen = harness({ powered: false });

  await screen.setAutomaticBrightness(900);

  assert.equal(screen.written.length, 0, "the automatic brightness wrote to a screen that was off");
});

test("the automatic brightness takes over when the screen is on", async () => {
  const screen = harness({ powered: true, brightness: 500 });

  await screen.setAutomaticBrightness(900);

  assert.equal(screen.written.length, 1, "the automatic brightness did not set the level");

  assert.equal(screen.written[0], 600, "the level did not move a quarter of the way");
});

test("the automatic brightness waits while the light is being brought back", async () => {

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

  assert.ok(
    main.includes("followDeviceTurn();"),
    "a turn does not turn the screen on",
  );
  assert.ok(
    main.includes("function followDeviceTurn()"),
    "there is nothing that turns the screen on for a turn",
  );

  assert.ok(
    main.includes("state.listening || state.thinking || state.speaking"),
    "the panel does not know when a turn is running",
  );
});

test("a notification turns the screen on", () => {

  const start = main.indexOf("function showNotice(");
  const body = main.slice(start, start + 600);
  assert.ok(
    body.includes("wakeScreen();"),
    "a notice arrives on a screen that is dark",
  );
});

test("a tap turns the screen on when it is off", () => {

  assert.ok(
    main.includes("function wakeOnTouch()"),
    "there is nothing that turns the screen on for a tap",
  );
});

test("a tap wakes the screen the way the switch in Home Assistant does", () => {

  const tap = main.indexOf("function wakeOnTouch()");
  const tapBody = main.slice(tap, tap + 400);
  assert.ok(
    tapBody.includes("wakeScreen();"),
    "a tap does not use the screen's own way of coming on",
  );

  const homeAssistant = main.indexOf('case "wake": {');
  const command = main.slice(homeAssistant, homeAssistant + 200);
  assert.ok(
    command.includes("wakeScreen();"),
    "the switch in Home Assistant does not use the screen's own way of coming on",
  );
});

test("the screen is only woken when it is off", () => {

  const start = main.indexOf("function wakeScreen()");
  const body = main.slice(start, start + 500);
  assert.ok(
    body.includes("if (!off) return;"),
    "the screen is woken even when it is already on",
  );
});

test("every view that can be on top wakes the screen", () => {

  const wakes = main.match(/if \(input\.type === "mouseDown"\) wakeOnTouch\(\);/g) || [];
  assert.equal(wakes.length, 3, "the screen is not woken from every view that takes a touch");
});

test("the overlay is on the screen while the screen is off", () => {

  assert.ok(
    main.includes("|| displayIsOff;"),
    "the overlay is not on the screen while the screen is off",
  );

  const renderer = fs.readFileSync(path.join(root, "src", "ui", "renderer.js"), "utf8");
  assert.ok(
    renderer.includes('document.body.classList.toggle("screen-off"'),
    "the page does not know the screen is off",
  );
  const css = fs.readFileSync(path.join(root, "src", "ui", "style.css"), "utf8");
  assert.ok(
    css.includes("body.screen-off{pointer-events:auto}"),
    "a screen that is off lets the touch through to the dashboard",
  );
});

test("the overlay is put back when the screen comes on", () => {

  const start = main.indexOf("async function setBacklightOff");
  const end = main.indexOf("function wakeOnTouch() {");
  const body = main.slice(start, end);
  const on = body.lastIndexOf("displayIsOff = false;");
  assert.ok(on > 0, "the screen's own off does not say the screen is on");
  const after = body.slice(on, on + 300);
  assert.ok(
    after.includes("resizeOverlay();"),
    "the overlay is left over the whole screen when the screen comes on",
  );
});

test("the screen comes back with the relight the panel needs", () => {

  assert.ok(
    main.includes("await relight(Math.round((await backlightMaximum()) * wanted / 100));"),
    "the screen does not come back with a relight",
  );
});
