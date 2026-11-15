const test = require("node:test");
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");

// The volume popup.
//
// It comes down from the top when the level changes and goes away again
// on its own, the way a phone does it.
//
// It is here because the level is changed with the buttons on the unit,
// and a person holding the device cannot see the drawer. The drawer
// still has the slider; the popup is the one that says what just
// happened.
//
// These are checked by looking at the code, because what matters is
// that the popup is wired to the level and that it is drawn.

const root = path.join(__dirname, "..");
const renderer = fs.readFileSync(path.join(root, "src", "ui", "renderer.js"), "utf8");
const html = fs.readFileSync(path.join(root, "src", "ui", "index.html"), "utf8");
const css = fs.readFileSync(path.join(root, "src", "ui", "style.css"), "utf8");
const main = fs.readFileSync(path.join(root, "src", "main.js"), "utf8");

test("the popup is on the screen", () => {
  assert.ok(html.includes('id="volume-popup"'), "there is no volume popup");
  assert.ok(html.includes('id="volume-popup-fill"'), "the popup has no level to show");
  assert.ok(html.includes('id="volume-popup-value"'), "the popup has no number to show");
  // Across the top, not in a corner.
  assert.ok(
    css.includes(".volume-popup{position:absolute;top:0;left:0;right:0;"),
    "the popup is not across the top",
  );
});

test("the popup is raised by a change of level", () => {
  // The device program's state carries the level, and it is what the
  // buttons on the unit change.
  assert.ok(
    renderer.includes("showVolumePopup(state.volume)"),
    "a level from the device program does not raise the popup",
  );
  // And the panel's own slider raises it too.
  assert.ok(
    renderer.includes("showVolumePopup(value);"),
    "the drawer slider does not raise the popup",
  );
});

test("the popup goes away again", () => {
  // A popup that never goes away is a popup that is always there.
  assert.ok(
    renderer.includes("setTimeout(hideVolumePopup, 2200)"),
    "the popup does not go away",
  );
  assert.ok(
    renderer.includes("volumePopup.classList.remove(\"is-visible\")"),
    "the popup is not hidden",
  );
});

test("the popup shows the level", () => {
  assert.ok(
    renderer.includes("volumePopupFill.style.width = `${percent}%`"),
    "the popup does not show the level",
  );
  assert.ok(
    renderer.includes("volumePopupValue.textContent = String(percent)"),
    "the popup does not show the number",
  );
});

test("the level from the buttons reaches the panel", () => {
  // The device program owns the level and tells the panel where it is,
  // so the buttons on the unit and the popup are the same thing.
  assert.ok(
    main.includes("followDeviceVolume(command.volume)"),
    "the device program's level does not reach the panel",
  );
});
