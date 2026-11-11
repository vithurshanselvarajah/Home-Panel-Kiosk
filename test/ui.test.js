const test = require("node:test");
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");

const root = path.join(__dirname, "..");
const html = fs.readFileSync(path.join(root, "src", "ui", "index.html"), "utf8");
const renderer = fs.readFileSync(path.join(root, "src", "ui", "renderer.js"), "utf8");

// The interface is a page with a drawer, a settings panel and a touch
// keyboard. It is one file of markup and one of script, and nothing
// checks that the two agree with each other.
//
// An edit that replaces a file with a piece of it looks fine in a diff
// until the thing is opened: the drawer goes, and the only sign is that
// the page is blank. So the page is checked for the pieces it must have,
// and the two files are checked to name the same things.

test("the interface page is a whole page", () => {
  assert.ok(html.length < 100000, "the page is implausibly large, so it has probably been duplicated");
  assert.ok(html.startsWith("<!doctype html>"), "the page does not start with a doctype");
  assert.ok(html.includes("</html>"), "the page is not closed");
});

test("the drawer has its parts", () => {
  for (const id of [
    "side-panel",
    "side-panel-backdrop",
    "brightness-control",
    "volume-control",
    "control-state",
    "immich-state",
    "dashboard-state",
    "voice-row",
    "settings-button",
    "display-mode-button",
    "exit-button",
    "mode-label",
    "version-label",
  ]) {
    assert.ok(html.includes(`id="${id}"`), `the drawer has no ${id}`);
  }
});

test("the settings panel has its parts", () => {
  for (const id of [
    "settings-panel",
    "settings-form",
    "tab-device",
    "tab-voice",
    "settings-view-device",
    "settings-view-voice",
    "voice-form",
    "form-error",
  ]) {
    assert.ok(html.includes(`id="${id}"`), `the settings panel has no ${id}`);
  }
});

test("the page loads the scripts it needs", () => {
  for (const source of ["./dev-bridge.js", "./device-ui.js", "./renderer.js", "./style.css"]) {
    assert.ok(html.includes(source), `the page does not load ${source}`);
  }
});

test("the drawer and the script name the same things", () => {
  // The script looks the drawer up by id, so an id in one and not the
  // other is a part of the drawer that will never be filled in.
  const ids = [...renderer.matchAll(/\$\("#([A-Za-z0-9_-]+)"\)/g)].map((match) => match[1]);
  assert.ok(ids.length > 10, "the script does not look up enough of the page");
  for (const id of ids) {
    assert.ok(html.includes(`id="${id}"`), `the script looks up ${id}, which the page does not have`);
  }
});

test("the voice row is in the drawer and not the settings", () => {
  // The row belongs to the drawer: it is one of the services a person
  // sees without opening the settings.
  const drawer = html.slice(html.indexOf('id="side-panel"'), html.indexOf('id="settings-panel"'));
  assert.ok(drawer.includes('id="voice-row"'), "the voice row is not in the drawer");
  assert.ok(!drawer.includes('id="voice-form"'), "the voice form is in the drawer");
});
