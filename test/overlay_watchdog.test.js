const test = require("node:test");
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");

// The overlay watchdog.
//
// The voice overlay is drawn by the renderer, from the device's state.
// The renderer tells the main process when the overlay is up, so that
// the view can be sized to cover the screen.
//
// That telling can be lost. The renderer is a whole process, it is
// built and thrown away, and a state that arrives at a page that is
// not listening yet is a state nobody saw. The overlay then does not
// come up, and the panel sits there saying nothing while the assistant
// is answering.
//
// So there is a watchdog on both sides: the main process sends the
// state again if the overlay has not come up, and the renderer applies
// the state again if the overlay is not there.

const root = path.join(__dirname, "..");
const main = fs.readFileSync(path.join(root, "src", "main.js"), "utf8");
const renderer = fs.readFileSync(path.join(root, "src", "ui", "renderer.js"), "utf8");

test("the device's state is sent when the overlay is built", () => {
  // The overlay is a page that is loaded from scratch. A page that has
  // just been loaded has heard nothing, so the state is sent to it
  // again — the panel's state AND the device's state, because the voice
  // overlay is drawn from the device's state and from nothing else.
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
  // The state is sent again when the overlay has not been reported up.
  assert.ok(
    main.includes("if (overlayWanted(deviceClient?.state)) sendDeviceState();"),
    "the watchdog does not send the state again",
  );
  // The rule is the same one the renderer draws by.
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
  // It puts the overlay up by applying the state again.
  assert.ok(
    renderer.includes("voiceUI.apply(lastDeviceState);"),
    "the watchdog does not apply the state",
  );
});

test("the overlay is made before it is sized", () => {
  // A view that does not exist cannot be sized, and the overlay would
  // be drawn in a strip along the top right rather than over the
  // screen.
  const fn = main.slice(main.indexOf("function setVoiceOverlayFull("));
  assert.ok(
    fn.includes("ensureOverlayView();"),
    "the overlay view is not made before it is sized",
  );
});

test("the overlay's rule is the voice and not the mute", () => {
  // A muted microphone is not talking, so there is no overlay for it.
  assert.ok(
    renderer.includes("if (!state || state.muted) return false;"),
    "the overlay does not take the mute into account",
  );
  assert.ok(
    main.includes("if (!state || state.muted) return false;"),
    "the watchdog does not take the mute into account",
  );
});
