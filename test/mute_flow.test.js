const test = require("node:test");
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");

// The state the drawer is given carries the mute.
//
// The volume is the system's, and the mute is the system's too, so the
// drawer has to be told about it or the mute button shows the wrong
// thing.

const root = path.join(__dirname, "..");
const main = fs.readFileSync(path.join(root, "src", "main.js"), "utf8");
const preload = fs.readFileSync(path.join(root, "src", "preload.js"), "utf8");
const renderer = fs.readFileSync(path.join(root, "src", "ui", "renderer.js"), "utf8");
const html = fs.readFileSync(path.join(root, "src", "ui", "index.html"), "utf8");

test("the state carries the mute", () => {
  assert.ok(
    main.includes("muted: currentMute()"),
    "the state does not say whether the system is muted",
  );
});

test("the mute can be set from the drawer", () => {
  assert.ok(
    preload.includes("setMute"),
    "the drawer cannot set the mute",
  );
  assert.ok(
    main.includes("app:set-mute"),
    "the main process does not answer the mute",
  );
});

test("the drawer shows the mute", () => {
  assert.ok(
    html.includes('id="mic-mute"'),
    "there is no mute button",
  );
  assert.ok(
    renderer.includes("paintMute(state.muted)"),
    "the mute button is not drawn from the state",
  );
});

test("the mute button has its own colour", () => {
  // The microphone's mute is red when it is muted and green when it is
  // not, so the state is seen rather than read.
  assert.ok(
    html.includes('class="mic-mute"'),
    "the mute button is not styled as one",
  );
});
