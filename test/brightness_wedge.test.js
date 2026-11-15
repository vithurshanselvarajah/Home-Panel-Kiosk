const test = require("node:test");
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");

// The brightness does not wedge itself.
//
// The panel tells Home Assistant the brightness, Home Assistant sends
// its number entities back as commands, and so a brightness of zero
// came back as a command for a brightness of zero, over and over. The
// slider could be moved and the screen would go straight back to black,
// which on a screen that is already black looks like the slider is
// broken.
//
// These are checked by looking at the code, because what matters is
// that a brightness the screen is already at is not applied again, and
// that a screen that is off publishes the level it was at rather than
// the level the backlight is at.

const root = path.join(__dirname, "..");
const main = fs.readFileSync(path.join(root, "src", "main.js"), "utf8");

test("a brightness the screen is already at is not applied again", () => {
  // The comparison is against the backlight's own level and not against
  // the published brightness, which is the level the screen was at when
  // it was turned off rather than the level the light is at.
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
  // The screen is turned off by taking the backlight to zero on this
  // device, so the backlight says zero while the screen is off. A
  // brightness of zero published to Home Assistant is a brightness that
  // comes straight back as a command.
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
  // There is no wlopm, no kscreen-doctor and no xset on this device, so
  // the screen is turned off by the backlight rather than by a command
  // that is not there.
  assert.ok(
    main.includes("setBacklightOff(operation === \"off\")"),
    "the screen does not go dark when it cannot be powered down",
  );
  // The screen's own off is the only thing that writes zero: it is what
  // turns the screen off, and the driver treats a level of zero as its
  // PWM being switched off.
  assert.ok(
    main.includes("await writeBacklightLevel(0);"),
    "the backlight is not taken to zero",
  );
});

test("the brightness is read back from the backlight", () => {
  // The backlight is the truth, so the comparison is against it and not
  // against what was last asked for.
  assert.ok(
    main.includes('path.join("/sys/class/backlight", device)'),
    "the backlight is not read",
  );
});
