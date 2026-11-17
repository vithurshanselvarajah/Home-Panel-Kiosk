const test = require("node:test");
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const vm = require("node:vm");

const root = path.join(__dirname, "..");
const source = fs.readFileSync(path.join(root, "src", "ui", "device-ui.js"), "utf8");

function element(tag, id) {
  const el = {
    tagName: (tag || "div").toUpperCase(),
    id: id || "",
    className: "",
    style: {},
    dataset: {},
    hidden: false,
    textContent: "",
    children: [],
    classList: {
      _names: new Set(),
      add(name) {
        this._names.add(name);
        el.className = [...this._names].join(" ");
      },
      remove(name) {
        this._names.delete(name);
        el.className = [...this._names].join(" ");
      },
      contains(name) {
        return this._names.has(name);
      },
    },
    addEventListener() {},
    querySelector(selector) {
      if (selector === ".voice-overlay__orb") return element("div", "orb");
      if (selector === ".voice-overlay__ring") return element("div", "ring");
      if (selector === ".voice-overlay__state") return element("div", "state");
      return null;
    },
  };
  return el;
}

function voiceUI() {
  const sandbox = {
    window: { addEventListener() {} },
    document: { createElement: () => element("div") },
    setInterval: () => null,
    clearInterval: () => {},
    setTimeout: () => null,
    requestAnimationFrame: (fn) => fn(),
    console,
  };
  sandbox.globalThis = sandbox;
  vm.createContext(sandbox);
  vm.runInContext(source, sandbox);
  return sandbox.window.VoiceUI;
}

function overlayParts() {
  const container = element("div", "voice-overlay");
  const heard = element("p", "voice-overlay-heard");
  const said = element("p", "voice-overlay-said");
  const state = element("span", "voice-overlay-state");
  const orb = element("div", "orb");
  container.querySelector = (selector) => {
    if (selector === "#voice-overlay-heard") return heard;
    if (selector === "#voice-overlay-said") return said;
    if (selector === ".voice-overlay__state") return state;
    if (selector === ".voice-overlay__orb") return orb;
    return null;
  };
  return { container, heard, said, state, orb };
}

function overlay() {
  const { container, heard, said } = overlayParts();
  const ui = new (voiceUI().VoiceOverlay)(container);
  ui.container = container;
  ui.heard = heard;
  ui.said = said;
  return ui;
}

test("the whole answer is shown once the audio has finished", () => {
  const ui = overlay();
  ui.setSaid("10:18 PM");
  ui.setProgress(1);
  ui.revealSaid();
  assert.equal(ui.said.textContent, "10:18 PM", "the answer was cut short");
});

test("the last word of the answer is not lost", () => {
  const ui = overlay();
  const answer = "Sorry, I am not aware of any device called a joke";
  ui.setSaid(answer);
  ui.setProgress(1);
  ui.revealSaid();
  assert.equal(ui.said.textContent, answer, "the answer was cut short");
});

test("the answer is revealed as the audio is played", () => {
  const ui = overlay();
  ui.setSaid("one two three four");
  ui.setProgress(0);
  ui.revealSaid();
  const empty = ui.said.textContent;
  ui.setProgress(0.5);
  ui.revealSaid();
  const half = ui.said.textContent;
  ui.setProgress(1);
  ui.revealSaid();
  const full = ui.said.textContent;

  assert.ok(full === "one two three four", "the answer was not shown in full");
  assert.ok(half.length > empty.length, "the answer did not grow with the audio");
  assert.ok(half.length < full.length, "the whole answer was shown before the audio was over");
});

test("a word is never cut in half", () => {
  const ui = overlay();
  ui.setSaid("one two three four");

  for (let p = 0; p <= 1.0001; p += 0.01) {
    ui.setProgress(p);
    ui.revealSaid();
    const shown = ui.said.textContent;
    const whole = "one two three four";
    if (shown === whole) continue;

    assert.ok(
      whole.startsWith(shown),
      `the text "${shown}" is not a prefix of the answer`,
    );
    assert.ok(shown.endsWith(" "), `the text "${shown}" was cut in the middle of a word`);
  }
});

test("the answer is not shown before it is spoken", () => {
  const ui = overlay();
  ui.setSaid("one two three four");
  ui.setProgress(0.1);
  ui.revealSaid();
  const shown = ui.said.textContent;
  assert.ok(shown.length < "one two three four".length, "the whole answer was shown at once");
});

test("the answer stays on the screen while the overlay is up", () => {
  const ui = overlay();
  ui.setHeard("What is the time");
  ui.setSaid("10:18 PM");
  ui.setProgress(1);
  ui.revealSaid();
  ui.setMode("speaking");
  assert.equal(ui.heard.textContent, "What is the time", "the words heard were lost");
  assert.equal(ui.said.textContent, "10:18 PM", "the answer was lost");
});

test("the answer is shown whole when the reply ends", () => {
  const ui = overlay();
  ui.setSaid("Sorry, I am not aware of any device called a joke");
  ui.setProgress(0.9);
  ui.revealSaid();
  assert.notEqual(ui.said.textContent, "", "nothing was shown at all");

  ui.setProgress(1);
  ui.revealSaid();
  assert.equal(
    ui.said.textContent,
    "Sorry, I am not aware of any device called a joke",
    "the answer stopped short",
  );
});

test("the answer is shown at once when the text arrives late", () => {
  const ui = overlay();
  ui.setProgress(1);
  ui.setSaid("10:18 PM");
  assert.equal(ui.said.textContent, "10:18 PM", "the answer was not shown");
});

test("the answer is shown whole through the whole path", () => {
  const { container, heard, said } = overlayParts();
  const ui = voiceUI().create({
    overlay: container,
    row: element("div", "row"),
    state: element("span", "state"),
    led: element("span", "led"),
  });

  ui.apply({
    connection: "connected",
    listening: false,
    thinking: false,
    speaking: true,
    transcript: "Tell me a joke",
    spoken: "Sorry, I am not aware of any device called a joke",
    spoken_progress: 0.5,
  });
  ui.overlay.revealSaid();
  const partway = said.textContent;
  assert.ok(partway.length > 0, "nothing was shown at all");
  assert.ok(
    partway.length < "Sorry, I am not aware of any device called a joke".length,
    "the whole answer was shown before the audio was over",
  );

  ui.apply({
    connection: "connected",
    listening: false,
    thinking: false,
    speaking: false,
    transcript: "Tell me a joke",
    spoken: "Sorry, I am not aware of any device called a joke",
    spoken_progress: 1,
  });
  ui.overlay.revealSaid();

  assert.equal(
    said.textContent,
    "Sorry, I am not aware of any device called a joke",
    "the answer stopped short",
  );
});
