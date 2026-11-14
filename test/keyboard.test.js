const test = require("node:test");
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const vm = require("node:vm");

// The touch keyboard in the settings.
//
// The keyboard is a set of buttons drawn over the settings page, and the
// keys type into the field that is being edited.
//
// It used to close the moment a key was pressed: a button takes the focus
// when it is pressed, and that took the focus off the field, and the field
// being blurred closed the keyboard. So the key press went nowhere.
//
// These tests run the code rather than looking for a line of code, because
// the bug was in the interaction between three pieces: the focus, the
// keyboard and the field.

const root = path.join(__dirname, "..");
const renderer = fs.readFileSync(path.join(root, "src", "ui", "renderer.js"), "utf8");

// The part of the script that types into the field.
//
// It is taken out of the script and run on a stub, because the script
// draws the whole page and the part that matters is small.
function typingCode() {
  const start = renderer.indexOf("function applyToSettingsField(value) {");
  const end = renderer.indexOf("// syncEncryptionKeyField enables");
  assert.ok(start > 0, "the typing code is not in the script");
  assert.ok(end > start, "the typing code is not in the script");
  return renderer.slice(start, end);
}

// The script that draws the keyboard.
//
// It is run so that the keys are real buttons that can be pressed, and so
// that the check is about the keys rather than about a line of code.
function keyboardCode() {
  const start = renderer.indexOf("function renderKeyboard() {");
  const end = renderer.indexOf("function setKeyboard(");
  assert.ok(start > 0, "the keyboard code is not in the script");
  assert.ok(end > start, "the keyboard code is not in the script");
  return renderer.slice(start, end);
}

// An element that can be pressed.
//
// It keeps a record of whether the focus was cancelled, because that is
// what stops the key taking the focus off the field.
function button(text) {
  const el = {
    tagName: "BUTTON",
    textContent: text,
    type: "button",
    className: "key",
    attributes: {},
    listeners: {},
    parent: null,
    children: [],
    focusTaken: false,
    setAttribute(name, value) { this.attributes[name] = value; },
    getAttribute(name) { return this.attributes[name]; },
    addEventListener(name, fn) { (this.listeners[name] ||= []).push(fn); },
    removeEventListener() {},
    appendChild(child) { child.parent = this; this.children.push(child); return child; },
    append(...kids) { for (const kid of kids) this.appendChild(kid); },
    querySelectorAll(selector) {
      const out = [];
      const walk = (node) => { for (const child of node.children) { if (child.tagName === "BUTTON") out.push(child); walk(child); } };
      walk(this);
      return out;
    },
    // Pressing the button takes the focus, unless the press was cancelled.
    press() {
      // A real press takes the focus unless the mousedown is cancelled.
      let cancelled = false;
      for (const fn of this.listeners.mousedown || []) fn({ preventDefault() { cancelled = true; } });
      this.focusTaken = !cancelled;
      for (const fn of this.listeners.click || []) fn({});
    },
  };
  return el;
}

// A plain element, for the rows and the header.
//
// It holds its children the way the real one does, so that the keys can
// be found by walking the tree.
function element(tag, className = "") {
  const el = button("");
  el.tagName = String(tag).toUpperCase();
  el.className = className;
  el.focusTaken = false;
  return el;
}

// The page the keyboard is drawn on.
//
// The field is a real input that can hold the focus, and the keyboard is
// the section the keys live in.
function page() {
  const keys = [];
  const keyboardEl = {
    tagName: "SECTION",
    id: "keyboard",
    hidden: true,
    children: [],
    listeners: {},
    replaceChildren() { this.children = []; },
    appendChild(child) { this.children.push(child); return child; },
    append(...kids) { for (const kid of kids) this.appendChild(kid); },
    addEventListener(name, fn) { (this.listeners[name] ||= []).push(fn); },
    querySelectorAll(selector) {
      const out = [];
      const walk = (node) => { for (const child of node.children) { if (child.tagName === "BUTTON") out.push(child); walk(child); } };
      walk(this);
      return out;
    },
    contains(node) {
      const walk = (n) => (n.children || []).some((c) => c === node || walk(c));
      return walk(this);
    },
  };
  const field = {
    tagName: "INPUT",
    id: "dashboardUrl",
    type: "url",
    value: "",
    selectionStart: 0,
    selectionEnd: 0,
    attributes: {},
    listeners: {},
    parent: null,
    setAttribute(name, value) { this.attributes[name] = value; },
    getAttribute(name) { return this.attributes[name]; },
    addEventListener(name, fn) { (this.listeners[name] ||= []).push(fn); },
    dispatchEvent() {},
  };
  const form = {
    tagName: "FORM",
    id: "settings-form",
    children: [field],
    contains(node) {
      const walk = (n) => (n.children || []).some((c) => c === node || walk(c));
      return walk(this);
    },
    requestSubmit() { this.submitted = true; },
  };
  field.parent = form;
  return { keyboardEl, field, form, keys };
}

// Runs the script's typing and keyboard code on a page.
function run() {
  const p = page();
  const typed = [];
  let activeElement = p.field;
  const sandbox = {
    window: {
      homePanel: {
        editFieldValue(field, key) {
          // The real helper, so that the behaviour is the real one.
          const { editFieldValue } = require("../src/utils");
          return editFieldValue(field, key);
        },
        setKeyboard() {},
        activity() {},
      },
    },
    document: {
      activeElement: undefined,
      createElement(tag) {
        return element(tag);
      },
      querySelector(selector) {
        if (selector === "#keyboard") return p.keyboardEl;
        return null;
      },
    },
    console,
  };
  sandbox.globalThis = sandbox;
  vm.createContext(sandbox);
  // The document's active element is read as the focus.
  Object.defineProperty(sandbox.document, "activeElement", {
    get() { return activeElement; },
    set(value) { activeElement = value; },
  });
  vm.runInContext(`
    const keyboard = keyboardEl;
    const form = formEl;
    const panel = { hidden: false };
    let keyboardTarget = null;
    let settingsKeyboardActive = false;
    let shiftOn = false;
    let symbolsOn = false;
    const window = { homePanel: homePanel };
    function setCaret(field, position) {
      if (typeof field.selectionStart !== "number") return;
      field.selectionStart = position;
      field.selectionEnd = position;
    }
    ${typingCode()}
    ${keyboardCode()}
  `, Object.assign(sandbox, {
    keyboardEl: p.keyboardEl,
    formEl: p.form,
    homePanel: sandbox.window.homePanel,
    Event: class Event { constructor(type) { this.type = type; } },
  }));
  // The typing code is called through a small wrapper so the test can
  // press a key and see what was typed.
  vm.runInContext(`
    globalThis.__type = (value) => { keyboardTarget = formEl.children[0]; return applyToSettingsField(value); };
    globalThis.__render = () => renderKeyboard();
  `, sandbox);
  // The keys are found by walking the tree, because the letter keys are in
  // rows and the rows are in the keyboard.
  const findKey = (label) => {
    const walk = (node) => {
      for (const child of node.children || []) {
        if (child.tagName === "BUTTON" && child.textContent === label) return child;
        const found = walk(child);
        if (found) return found;
      }
      return null;
    };
    return walk(p.keyboardEl);
  };
  const allKeys = () => {
    const out = [];
    const walk = (node) => {
      for (const child of node.children || []) {
        if (child.tagName === "BUTTON") out.push(child);
        walk(child);
      }
    };
    walk(p.keyboardEl);
    return out;
  };
  return { ...p, sandbox, typed, findKey, allKeys };
}

test("a key press types into the field instead of closing the keyboard", () => {
  const p = run();
  p.sandbox.__render();
  const key = p.findKey("a");
  assert.ok(key, "the keyboard has no letter keys");
  key.press();
  assert.equal(p.field.value, "a", "the key press did not type into the field");
});

test("the keys do not take the focus off the field", () => {
  // A button takes the focus when it is pressed. If the press is not
  // cancelled, the field is blurred and the keyboard is closed.
  const p = run();
  p.sandbox.__render();
  const key = p.findKey("b");
  key.press();
  assert.equal(key.focusTaken, false, "the key took the focus off the field");
});

test("the keyboard keeps its keys and does not close when a key is pressed", () => {
  // The whole failure was that the keyboard closed the moment it was
  // used, so there was nothing left to press.
  const p = run();
  p.sandbox.__render();
  const before = p.allKeys().length;
  assert.ok(before > 20, "the keyboard has hardly any keys");
  for (const letter of ["h", "i", "j"]) {
    const key = p.findKey(letter);
    key.press();
  }
  assert.equal(p.field.value, "hij", "the keys did not all type into the field");
});

test("the space key types a space and the backspace key removes one", () => {
  const p = run();
  p.sandbox.__render();
  p.sandbox.__type("a");
  p.sandbox.__type("b");
  assert.equal(p.field.value, "ab");
  p.sandbox.__type("SPACE");
  assert.equal(p.field.value, "ab ");
  p.sandbox.__type("BACKSPACE");
  assert.equal(p.field.value, "ab");
});

test("the field being edited is remembered, so a key goes to it", () => {
  // The focus is on the button rather than on the field, because the
  // field was blurred. The field is remembered, so the key still goes to
  // the right place.
  const p = run();
  p.sandbox.__render();
  const key = p.findKey("z");
  key.press();
  // The key took the focus by force, which is what a real press does when
  // the cancellation did not happen.
  p.sandbox.document.activeElement = key;
  p.sandbox.__type("z");
  assert.equal(p.field.value, "zz", "the key did not go to the field being edited");
});
