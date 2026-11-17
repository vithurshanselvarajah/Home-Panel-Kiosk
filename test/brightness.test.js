const test = require("node:test");
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const vm = require("node:vm");

const root = path.join(__dirname, "..");
const renderer = fs.readFileSync(path.join(root, "src", "ui", "renderer.js"), "utf8");

function element(tag, attrs = {}, children = []) {
  const el = {
    tagName: tag.toUpperCase(),
    id: attrs.id || "",
    className: attrs.class || "",
    dataset: { ...(attrs.dataset || {}) },
    style: { setProperty() {} },
    hidden: false,
    disabled: false,
    value: attrs.value || "0",
    textContent: "",
    children,
    attributes: attrs,
    parent: null,
    listeners: {},
    classList: {
      _names: new Set((attrs.class || "").split(" ").filter(Boolean)),
      add(name) { this._names.add(name); },
      remove(name) { this._names.delete(name); },
      contains(name) { return this._names.has(name); },
      toggle(name, on) { if (on) this.add(name); else this.remove(name); },
    },
    addEventListener(name, fn) { (this.listeners[name] ||= []).push(fn); },
    removeEventListener() {},
    setAttribute(name, value) { this.attributes[name] = value; },
    getAttribute(name) { return this.attributes[name]; },
    removeAttribute() {},
    appendChild(child) { child.parent = this; this.children.push(child); return child; },
    querySelector(selector) { return find(this, selector); },
    querySelectorAll(selector) { return findAll(this, selector); },
    closest() { return null; },
    getBoundingClientRect() { return { width: 330, height: 800, left: 0, top: 0 }; },
    focus() {},
  };
  for (const child of children) el.appendChild(child);
  return el;
}

function find(root, selector) {
  for (const child of root.children) {
    if (matches(child, selector)) return child;
    const found = find(child, selector);
    if (found) return found;
  }
  return null;
}

function findAll(root, selector) {
  const out = [];
  for (const child of root.children) {
    if (matches(child, selector)) out.push(child);
    out.push(...findAll(child, selector));
  }
  return out;
}

function matches(el, selector) {
  if (selector.startsWith(".")) return el.classList.contains(selector.slice(1));
  if (selector.startsWith("#")) return el.id === selector.slice(1);
  const attribute = selector.match(/^\[([\w-]+)(?:="([^"]*)")?\]$/);
  if (attribute) {
    const name = attribute[1].replace(/^data-/, "");
    const value = attribute[2];
    const actual = name in el.dataset ? el.dataset[name] : el.getAttribute(attribute[1]);
    if (value === undefined) return actual !== undefined && actual !== null;
    return actual === value;
  }
  return el.tagName === selector.toUpperCase();
}

function buildPage() {
  const root = element("div", { id: "root" });
  const brightnessControl = element("section", { id: "brightness-control" });
  const volumeControl = element("section", { id: "volume-control" });
  const slider = element("input", { id: "brightness-slider" });
  brightnessControl.appendChild(slider);
  root.appendChild(brightnessControl);
  root.appendChild(volumeControl);

  for (const id of [
    "mode-label", "version-label", "dashboard-action-label", "return-home",
    "slideshow-action", "display-mode-label", "control-state", "immich-state",
    "dashboard-state", "keyboard", "panel", "notice", "notice-title",
    "notice-message", "settings-panel", "settings-form",
    "brightness-value", "volume-value",
    "mute-indicator", "volume-popup", "volume-popup-fill", "volume-popup-value",
  ]) {
    root.appendChild(element("div", { id }));
  }
  for (const service of ["control", "immich", "dashboard"]) {
    root.appendChild(element("span", { id: `${service}-state` }));
    root.appendChild(element("span", { dataset: { service } }));
  }
  return { root, brightnessControl, volumeControl, slider };
}

function stateHandler(page) {

  const start = renderer.indexOf("function paintBrightness(value) {");

  const handlerStart = renderer.indexOf('window.homePanel.onState((state) => {');
  const end = renderer.indexOf("\n});", handlerStart) + "\n});".length;
  const source = renderer.slice(start, end);

  let onState = null;

  const brightnessSlider = find(page.root, "#brightness-slider");
  const volumeSlider = find(page.root, "#volume-slider");
  const sandbox = {
    window: { homePanel: { onState(callback) { onState = callback; } } },
    document: {
      querySelector: (selector) => find(page.root, selector),
      querySelectorAll: (selector) => findAll(page.root, selector),
      addEventListener() {},

      body: { classList: { toggle() {}, contains() { return false; } } },
    },
    $: (selector) => find(page.root, selector),
    setInterval() { return 0; },
    setTimeout() { return 0; },
    clearTimeout() {},
    clearInterval() {},

    requestAnimationFrame: (fn) => fn(),
    Date, Math, Number, Boolean, Array, Object,
    console,
  };
  sandbox.globalThis = sandbox;
  sandbox.bindings = {
    brightnessSlider,
    volumeSlider,
    brightnessControl: page.brightnessControl,
    volumeControl: page.volumeControl,
    brightnessValue: find(page.root, "#brightness-value"),
    volumeValue: find(page.root, "#volume-value"),
    volumePopup: find(page.root, "#volume-popup"),
    volumePopupFill: find(page.root, "#volume-popup-fill"),
    volumePopupValue: find(page.root, "#volume-popup-value"),
    muteIndicator: find(page.root, "#mute-indicator"),

    paintSlider: (slider, percent) => {
      if (!slider) return;
      slider.value = String(percent);
      slider.style.setProperty("--fill", `${percent}%`);
    },
  };
  vm.createContext(sandbox);
  vm.runInContext(
    `const brightnessSlider = bindings.brightnessSlider;` +
    `const volumeSlider = bindings.volumeSlider;` +
    `const brightnessValue = bindings.brightnessValue;` +
    `const volumeValue = bindings.volumeValue;` +
    `const volumePopup = bindings.volumePopup;` +
    `const volumePopupFill = bindings.volumePopupFill;` +
    `const volumePopupValue = bindings.volumePopupValue;` +
    `const muteIndicator = bindings.muteIndicator;` +
    `const paintSlider = bindings.paintSlider;` +
    `const brightnessControl = bindings.brightnessControl;` +
    `const volumeControl = bindings.volumeControl;` +
    `let loadedSettings = null;` +
    `let settingsKeyboardActive = false;` +
    `const syncSettingsLayout = () => {};` +
    `const syncDrawerLayout = () => {};` +
    `let displayMode = "window";` +
    `let keyboardVisible = false;` +
    `let mode = "dashboard";` +
    `let panel = { hidden: true };` +
    `let keyboard = { hidden: true };` +
    `const drawer = { classList: { toggle() {}, contains() { return false; } }, setAttribute() {} };` +
    `const drawerBackdrop = { hidden: true };` +
    source,
    sandbox,
  );
  return { ...sandbox, onState: (state) => onState(state) };
}

function stateWithBrightness() {
  return {
    mode: "dashboard",
    displayIsOff: false,
    keyboardVisible: false,
    sidePanelOpen: true,
    displayMode: "fullscreen",
    brightness: 42,
    volume: 50,
    muted: false,
    version: "0.0.2",
    serviceStatus: { control: "online", immich: "unknown", dashboard: "unknown" },
    lastActivity: new Date().toISOString(),
  };
}

function stateWithoutBrightness() {
  const state = stateWithBrightness();
  delete state.brightness;
  return state;
}

test("the brightness control is shown when the system has a backlight", () => {
  const page = buildPage();
  const sandbox = stateHandler(page);

  sandbox.onState(stateWithBrightness());

  assert.equal(page.brightnessControl.hidden, false, "the brightness control is hidden on a machine with a backlight");
});

test("the brightness control is hidden when the system has no backlight", () => {
  const page = buildPage();
  const sandbox = stateHandler(page);

  sandbox.onState(stateWithoutBrightness());

  assert.equal(page.brightnessControl.hidden, true, "the brightness control is shown on a machine with no backlight");
});

test("the brightness control goes away when the backlight goes", () => {
  const page = buildPage();
  const sandbox = stateHandler(page);

  sandbox.onState(stateWithBrightness());
  assert.equal(page.brightnessControl.hidden, false, "the brightness control was hidden on a machine with a backlight");

  sandbox.onState(stateWithoutBrightness());
  assert.equal(page.brightnessControl.hidden, true, "the brightness control stayed when the backlight went");
});
