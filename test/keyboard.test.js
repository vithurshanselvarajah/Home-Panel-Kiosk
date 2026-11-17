const test = require("node:test");
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const vm = require("node:vm");

const root = path.join(__dirname, "..");
const renderer = fs.readFileSync(path.join(root, "src", "ui", "renderer.js"), "utf8");

function typingCode() {
  const start = renderer.indexOf("function applyToSettingsField(value) {");
  const end = renderer.indexOf("function syncEncryptionKeyField() {");
  assert.ok(start > 0, "the typing code is not in the script");
  assert.ok(end > start, "the typing code is not in the script");
  return renderer.slice(start, end);
}

function keyboardCode() {
  const start = renderer.indexOf("function renderKeyboard() {");
  const end = renderer.indexOf("function setKeyboard(");
  assert.ok(start > 0, "the keyboard code is not in the script");
  assert.ok(end > start, "the keyboard code is not in the script");
  return renderer.slice(start, end);
}

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

    press() {

      let cancelled = false;
      for (const fn of this.listeners.mousedown || []) fn({ preventDefault() { cancelled = true; } });
      this.focusTaken = !cancelled;
      for (const fn of this.listeners.click || []) fn({});
    },
  };
  return el;
}

function element(tag, className = "") {
  const el = button("");
  el.tagName = String(tag).toUpperCase();
  el.className = className;
  el.focusTaken = false;
  return el;
}

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

function run() {
  const p = page();
  const typed = [];
  let activeElement = p.field;
  const sandbox = {
    window: {
      homePanel: {
        editFieldValue(field, key) {

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

  vm.runInContext(`
    globalThis.__type = (value) => { keyboardTarget = formEl.children[0]; return applyToSettingsField(value); };
    globalThis.__render = () => renderKeyboard();
  `, sandbox);

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

  const p = run();
  p.sandbox.__render();
  const key = p.findKey("b");
  key.press();
  assert.equal(key.focusTaken, false, "the key took the focus off the field");
});

test("the keyboard keeps its keys and does not close when a key is pressed", () => {

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

  const p = run();
  p.sandbox.__render();
  const key = p.findKey("z");
  key.press();

  p.sandbox.document.activeElement = key;
  p.sandbox.__type("z");
  assert.equal(p.field.value, "zz", "the key did not go to the field being edited");
});
