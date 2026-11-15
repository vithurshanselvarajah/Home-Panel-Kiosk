const test = require("node:test");
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");

// The icons.
//
// The device has DejaVu's fonts and nothing else. DejaVu has no pause
// sign (U+23F8), no transport arrows (U+23ED, U+23EE), no full-screen
// sign (U+26F6), no power sign (U+23FB) and no microphone emoji
// (U+1F399). A character the font does not have is a blank box, or
// nothing at all — which is what the buttons looked like.
//
// So every icon is drawn rather than typed. That is the only way it
// looks the same on every machine.
//
// The characters that ARE typed are checked here, so that a new one
// cannot creep in and break on the device.

const root = path.join(__dirname, "..");
const html = fs.readFileSync(path.join(root, "src", "ui", "index.html"), "utf8");
const renderer = fs.readFileSync(path.join(root, "src", "ui", "renderer.js"), "utf8");

// The characters the device's font does not have.
const missing = {
  "U+1F399": "🎙",
  "U+26F6": "⛶",
  "U+23FB": "⏻",
  "U+23EE": "⏮",
  "U+23ED": "⏭",
  "U+23F8": "⏸",
};

test("no icon is a character the device's font does not have", () => {
  for (const [code, character] of Object.entries(missing)) {
    assert.ok(
      !html.includes(character),
      `${code} is typed in the page, and the device's font does not have it`,
    );
    assert.ok(
      !renderer.includes(character),
      `${code} is typed in the script, and the device's font does not have it`,
    );
  }
});

test("the icons are drawn", () => {
  // Every icon in the page is a drawing.
  assert.ok(html.includes("<svg"), "there are no drawings in the page");
  // The transport, which is the set that was broken.
  assert.ok(html.includes('id="media-previous"'), "there is no previous button");
  assert.ok(html.includes('id="media-play"'), "there is no play button");
  assert.ok(html.includes('id="media-next"'), "there is no next button");
  // The window mode and the exit, which were broken too.
  assert.ok(html.includes('id="display-mode-button"'), "there is no window mode button");
  assert.ok(html.includes('id="exit-button"'), "there is no exit button");
});

test("the play button swaps between the two signs", () => {
  // The button shows what it will do, so the sign has to change with
  // the state. Both signs are drawings, because the pause sign is one
  // of the characters the device's font does not have.
  assert.ok(
    renderer.includes("const playIcon = '<svg"),
    "there is no play sign",
  );
  assert.ok(
    renderer.includes("const pauseIcon = '<svg"),
    "there is no pause sign",
  );
  assert.ok(
    renderer.includes("mediaPlay.innerHTML = shown.playing ? pauseIcon : playIcon;"),
    "the play button does not swap its sign",
  );
});

test("the drawer's icons are drawn too", () => {
  // The house, the slideshow, the settings, the brightness and the
  // volume. They were characters, and they were the ones that looked
  // different on different machines.
  const icons = html.match(/<svg /g) || [];
  assert.ok(icons.length >= 8, `there are only ${icons.length} drawings in the page`);
});
