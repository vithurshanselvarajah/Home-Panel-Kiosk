const test = require("node:test");
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");

const root = path.join(__dirname, "..");
const deviceUi = fs.readFileSync(path.join(root, "src", "ui", "device-ui.js"), "utf8");

test("a new turn clears the last answer", () => {
  assert.ok(
    deviceUi.includes('if (mode === "listening" && overlay.mode !== "listening") {'),
    "a new turn does not clear the last answer",
  );
  assert.ok(
    deviceUi.includes('overlay.setHeard("");'),
    "the question of the last turn is not cleared",
  );
  assert.ok(
    deviceUi.includes('overlay.setSaid("");'),
    "the answer of the last turn is not cleared",
  );
});

test("the words come back when the new ones arrive", () => {

  assert.ok(
    deviceUi.includes("if (transcript && transcript !== overlay.heardText) {"),
    "the question is never put back",
  );
  assert.ok(
    deviceUi.includes("if (spoken && spoken !== overlay.saidText) {"),
    "the answer is never put back",
  );
});

test("the overlay clears the words when it closes", () => {
  const close = deviceUi.slice(deviceUi.indexOf("    close() {"));
  const body = close.slice(0, close.indexOf("\n    }"));
  assert.ok(body.includes('this.heardText = "";'), "the question is kept when the overlay closes");
  assert.ok(body.includes('this.saidText = "";'), "the answer is kept when the overlay closes");
});
