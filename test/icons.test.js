const test = require("node:test");
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");

const root = path.join(__dirname, "..");
const html = fs.readFileSync(path.join(root, "src", "ui", "index.html"), "utf8");
const renderer = fs.readFileSync(path.join(root, "src", "ui", "renderer.js"), "utf8");

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

  assert.ok(html.includes("<svg"), "there are no drawings in the page");

  assert.ok(html.includes('id="media-previous"'), "there is no previous button");
  assert.ok(html.includes('id="media-play"'), "there is no play button");
  assert.ok(html.includes('id="media-next"'), "there is no next button");

  assert.ok(html.includes('id="display-mode-button"'), "there is no window mode button");
  assert.ok(html.includes('id="exit-button"'), "there is no exit button");
});

test("the play button swaps between the two signs", () => {

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

  const icons = html.match(/<svg /g) || [];
  assert.ok(icons.length >= 8, `there are only ${icons.length} drawings in the page`);
});
