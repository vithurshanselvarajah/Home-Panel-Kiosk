const test = require("node:test");
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");

const root = path.join(__dirname, "..");
const renderer = fs.readFileSync(path.join(root, "src", "ui", "renderer.js"), "utf8");
const html = fs.readFileSync(path.join(root, "src", "ui", "index.html"), "utf8");
const css = fs.readFileSync(path.join(root, "src", "ui", "style.css"), "utf8");
const main = fs.readFileSync(path.join(root, "src", "main.js"), "utf8");

test("the popup is on the screen", () => {
  assert.ok(html.includes('id="volume-popup"'), "there is no volume popup");
  assert.ok(html.includes('id="volume-popup-fill"'), "the popup has no level to show");
  assert.ok(html.includes('id="volume-popup-value"'), "the popup has no number to show");

  assert.ok(
    css.includes(".volume-popup{position:absolute;top:0;left:0;right:0;"),
    "the popup is not across the top",
  );
});

test("the popup is not raised by something that is not the volume", () => {

  assert.ok(
    renderer.includes("if (volumePopup && state.volume !== lastPopupVolume) {"),
    "the popup is raised whatever the state says",
  );
  assert.ok(
    !renderer.includes("if (volumePopup && volumePopup.hidden) showVolumePopup(state.volume);"),
    "the popup is raised whenever it happens to be down",
  );
});

test("the popup is on the main screen and not only in the drawer", () => {

  assert.ok(
    main.includes("showVolumePopup(value);"),
    "the main process does not raise the popup when the level changes",
  );
  assert.ok(
    main.includes("ensureOverlayView();"),
    "the overlay view is not made for the popup",
  );
  assert.ok(
    main.includes("|| overlayNeeded()"),
    "the overlay view is not kept on the screen for the popup",
  );
  assert.ok(
    main.includes("overlayExpanded || sidePanelOpen || voiceOverlayFull || overlayNeeded() || displayIsOff) {"),
    "the overlay view is not the size of the screen for the popup",
  );

  assert.ok(
    main.includes('sendToOverlay("app:volume-popup"'),
    "the renderer is not told about the popup",
  );
  assert.ok(
    renderer.includes("window.homePanel.onVolumePopup("),
    "the renderer does not listen for the popup",
  );
});

test("the mute indicator is on the main screen too", () => {

  assert.ok(
    main.includes("showMuteIndicator(muted);"),
    "the main process does not raise the mute indicator",
  );
  assert.ok(
    main.includes('sendToOverlay("app:mute-indicator"'),
    "the renderer is not told about the mute indicator",
  );
  assert.ok(
    renderer.includes("window.homePanel.onMuteIndicator("),
    "the renderer does not listen for the mute indicator",
  );
});

test("the popup and the indicator do not take each other down", () => {

  assert.ok(
    main.includes("let overlayPopup = false;"),
    "the popup's flag is gone",
  );
  assert.ok(
    main.includes("let overlayMute = false;"),
    "the indicator's flag is gone",
  );
  assert.ok(
    main.includes("return overlayPopup || overlayMute;"),
    "the two are not kept apart",
  );
});

test("the popup shows the level every time it changes", () => {

  assert.ok(
    main.includes("showVolumePopup(value);"),
    "the popup is not raised again when the level changes",
  );
  assert.ok(
    renderer.includes("if (volumePopupFill) volumePopupFill.style.width = `${percent}%`;"),
    "the popup does not show the level",
  );
  assert.ok(
    renderer.includes("if (volumePopupValue) volumePopupValue.textContent = String(percent);"),
    "the popup does not show the number",
  );
});

test("the popup is raised by a change of level", () => {

  assert.ok(
    renderer.includes("showVolumePopup(state.volume)"),
    "a level from the device program does not raise the popup",
  );

  assert.ok(
    renderer.includes("showVolumePopup(value);"),
    "the drawer slider does not raise the popup",
  );
});

test("the popup goes away again", () => {

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

  assert.ok(
    main.includes("followDeviceVolume(command.volume)"),
    "the device program's level does not reach the panel",
  );
});
