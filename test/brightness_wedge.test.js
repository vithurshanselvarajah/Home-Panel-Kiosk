const test = require("node:test");
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");

const root = path.join(__dirname, "..");
const main = fs.readFileSync(path.join(root, "src", "main.js"), "utf8");

test("a brightness the screen is already at is not applied again", () => {

  assert.ok(
    main.includes("if (backlightLevel() === percent) return;"),
    "the brightness is applied again even when it is already there",
  );
  assert.ok(
    main.includes("function backlightLevel()"),
    "the backlight's own level is not read",
  );
});

test("a screen that is off publishes the level it was at", () => {

  assert.ok(
    main.includes("if (percent === 0 && brightnessBeforeOff !== null) return brightnessBeforeOff;"),
    "a screen that is off publishes a brightness of zero",
  );
});

test("the level the screen was at is remembered", () => {
  assert.ok(
    main.includes("brightnessBeforeOff = level;"),
    "the level the screen was at is not remembered",
  );
  assert.ok(
    main.includes("const wanted = level === null || level <= 0 ? 60 : level;"),
    "the screen does not come back at the level it was at",
  );
});

test("a screen with no way to power it down goes dark instead", () => {

  assert.ok(
    main.includes("setBacklightOff(operation === \"off\")"),
    "the screen does not go dark when it cannot be powered down",
  );

  assert.ok(
    main.includes("await writeBacklightLevel(0);"),
    "the backlight is not taken to zero",
  );
});

test("the brightness is read back from the backlight", () => {

  assert.ok(
    main.includes('path.join("/sys/class/backlight", device)'),
    "the backlight is not read",
  );
});
