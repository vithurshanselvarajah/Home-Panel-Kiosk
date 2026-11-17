const test = require("node:test");
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");

const root = path.join(__dirname, "..");
const main = fs.readFileSync(path.join(root, "src", "main.js"), "utf8");
const renderer = fs.readFileSync(path.join(root, "src", "ui", "renderer.js"), "utf8");

test("the device's state is sent when the overlay is built", () => {

  const load = main.indexOf('webContents.on("did-finish-load"');
  assert.notEqual(load, -1, "the overlay has no load handler");
  const handler = main.slice(load, load + 1400);
  assert.ok(
    handler.includes("sendState()"),
    "the panel's state is not sent to the new overlay",
  );
  assert.ok(
    handler.includes("sendDeviceState()"),
    "the device's state is not sent to the new overlay",
  );
});

test("the main process watches for an overlay that did not come up", () => {
  assert.ok(
    main.includes("function watchTheOverlay("),
    "there is no watchdog in the main process",
  );

  assert.ok(
    main.includes("if (overlayWanted(deviceClient?.state)) sendDeviceState();"),
    "the watchdog does not send the state again",
  );

  assert.ok(
    main.includes("function overlayWanted(state)"),
    "the watchdog does not know what the overlay is for",
  );
});

test("the renderer watches for an overlay that did not come up", () => {
  assert.ok(
    renderer.includes("function checkOverlay("),
    "there is no watchdog in the renderer",
  );
  assert.ok(
    renderer.includes("function startOverlayWatchdog("),
    "the watchdog is never started",
  );
  assert.ok(
    renderer.includes("if (voiceUI) startOverlayWatchdog();"),
    "the watchdog does not start with the page",
  );

  assert.ok(
    renderer.includes("voiceUI.apply(lastDeviceState);"),
    "the watchdog does not apply the state",
  );
});

test("the overlay is made before it is sized", () => {

  const fn = main.slice(main.indexOf("function setVoiceOverlayFull("));
  assert.ok(
    fn.includes("ensureOverlayView();"),
    "the overlay view is not made before it is sized",
  );
});

test("the overlay's rule is the voice and not the mute", () => {

  assert.ok(
    renderer.includes("if (!state || state.muted) return false;"),
    "the overlay does not take the mute into account",
  );
  assert.ok(
    main.includes("if (!state || state.muted) return false;"),
    "the watchdog does not take the mute into account",
  );
});
