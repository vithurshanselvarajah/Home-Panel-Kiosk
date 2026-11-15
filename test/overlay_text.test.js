const test = require("node:test");
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");

// The overlay does not keep the last answer.
//
// The words are kept while the overlay is up, which is what keeps the
// question on the screen while the assistant thinks about it. But the
// next question comes with the overlay up again and the old answer
// still on it, and the answer is only replaced when the new one
// arrives — so for a moment the panel says the answer to the last
// question while it is listening to this one.
//
// So a turn that starts clears the words, and they come back when the
// new ones arrive.

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
  // The clearing is only for the start of a turn: the text is put back
  // by the state that carries it.
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
