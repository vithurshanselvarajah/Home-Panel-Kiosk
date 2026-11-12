const test = require("node:test");
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const vm = require("node:vm");

// The wake word overlay.
//
// This is the screen the person looks at while the assistant works, so
// the words on it are the whole point of it. The words come from Home
// Assistant in two pieces that do not arrive at the same time: the
// answer's text arrives all at once, and the answer's audio arrives as
// it is spoken.
//
// So the text is revealed in step with the audio. That is where the
// trouble is: revealing text in step with audio means deciding how
// much of it to show, and getting that wrong truncates the answer.
//
// These tests are about that behaviour, not about the markup.

const root = path.join(__dirname, "..");
const source = fs.readFileSync(path.join(root, "src", "ui", "device-ui.js"), "utf8");

// element is the smallest thing the overlay can live in.
//
// The overlay touches the container's class list, style and hidden
// flag, and asks it for the orb, the state and the two text areas.
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

// voiceUI runs the interface in a sandbox and gives it back.
//
// The interface is a page of script, so it is run as one: the markup
// around it is stubbed, but the code is the code that runs in the
// panel.
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

// overlayParts builds the container the overlay is drawn in, and the
// two places it writes the words.
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

// overlay builds a VoiceOverlay over a stub container.
function overlay() {
  const { container, heard, said } = overlayParts();
  const ui = new (voiceUI().VoiceOverlay)(container);
  ui.container = container;
  ui.heard = heard;
  ui.said = said;
  return ui;
}

// The answer is shown in full once the audio has been played.
test("the whole answer is shown once the audio has finished", () => {
  const ui = overlay();
  ui.setSaid("10:18 PM");
  ui.setProgress(1);
  ui.revealSaid();
  assert.equal(ui.said.textContent, "10:18 PM", "the answer was cut short");
});

// The answer is not cut short at the end of a long sentence.
test("the last word of the answer is not lost", () => {
  const ui = overlay();
  const answer = "Sorry, I am not aware of any device called a joke";
  ui.setSaid(answer);
  ui.setProgress(1);
  ui.revealSaid();
  assert.equal(ui.said.textContent, answer, "the answer was cut short");
});

// The words are revealed in step with the audio, so the text and the
// voice stay together.
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

// A word is never cut in half on the screen.
test("a word is never cut in half", () => {
  const ui = overlay();
  ui.setSaid("one two three four");
  // Every progress from nothing to all of it, in small steps.
  for (let p = 0; p <= 1.0001; p += 0.01) {
    ui.setProgress(p);
    ui.revealSaid();
    const shown = ui.said.textContent;
    const whole = "one two three four";
    if (shown === whole) continue;
    // What is shown must end at a word boundary.
    assert.ok(
      whole.startsWith(shown),
      `the text "${shown}" is not a prefix of the answer`,
    );
    assert.ok(shown.endsWith(" "), `the text "${shown}" was cut in the middle of a word`);
  }
});

// The words are not revealed past the audio.
test("the answer is not shown before it is spoken", () => {
  const ui = overlay();
  ui.setSaid("one two three four");
  ui.setProgress(0.1);
  ui.revealSaid();
  const shown = ui.said.textContent;
  assert.ok(shown.length < "one two three four".length, "the whole answer was shown at once");
});

// The overlay holds the answer on the screen while it is up.
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

// The reply ends and the speaking stops at the same moment, so the
// progress that says the answer is complete arrives at the same time
// as the mode goes off.
//
// If the words are given to the overlay after the mode has gone, the
// answer is never shown whole: it stops a word short, because the
// progress that would have finished it never arrived.
test("the answer is shown whole when the reply ends", () => {
  const ui = overlay();
  ui.setSaid("Sorry, I am not aware of any device called a joke");
  ui.setProgress(0.9);
  ui.revealSaid();
  assert.notEqual(ui.said.textContent, "", "nothing was shown at all");

  // The reply has been played out: the progress is all of it.
  ui.setProgress(1);
  ui.revealSaid();
  assert.equal(
    ui.said.textContent,
    "Sorry, I am not aware of any device called a joke",
    "the answer stopped short",
  );
});

// The text arriving after the reply has been played shows at once.
test("the answer is shown at once when the text arrives late", () => {
  const ui = overlay();
  ui.setProgress(1);
  ui.setSaid("10:18 PM");
  assert.equal(ui.said.textContent, "10:18 PM", "the answer was not shown");
});

// The whole path, from the state the device sends to the words on the
// screen.
//
// The reply ends and the speaking stops at the same moment. So the
// state that says the answer is complete arrives with the state that
// says the assistant has finished. If the words are given to the
// overlay after that, the answer stops a word short.
test("the answer is shown whole through the whole path", () => {
  const { container, heard, said } = overlayParts();
  const ui = voiceUI().create({
    overlay: container,
    row: element("div", "row"),
    state: element("span", "state"),
    led: element("span", "led"),
  });

  // The question was asked and the answer is being spoken.
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

  // The reply has been played out. The speaking stops at the same
  // moment, so the assistant has finished too.
  //
  // The text arrives at the same time as the end of the reply, which is
  // what happens when the answer's words are only known once the reply
  // has been said.
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
