const test = require("node:test");
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const vm = require("node:vm");

const root = path.join(__dirname, "..");
const tabHtml = fs.readFileSync(path.join(root, "src", "ui", "tab.html"), "utf8");
const tabScript = fs.readFileSync(path.join(root, "src", "ui", "tab.js"), "utf8");
const tabCss = fs.readFileSync(path.join(root, "src", "ui", "tab.css"), "utf8");
const mainSource = fs.readFileSync(path.join(root, "src", "main.js"), "utf8");

function tabScriptRun({ open = true } = {}) {
  const tab = {
    id: "drawer-tab",
    hidden: false,
    attributes: { "aria-expanded": "false" },
    listeners: {},
    setAttribute(name, value) { this.attributes[name] = value; },
    getAttribute(name) { return this.attributes[name]; },
    addEventListener(name, fn) { (this.listeners[name] ||= []).push(fn); },
  };
  const bridge = {
    opened: 0,
    states: [],
    openDrawer() { this.opened += 1; },
    onDrawerState(callback) { this.states.push(callback); },
  };
  const sandbox = {
    window: { homePanelTab: bridge },
    document: { querySelector: (selector) => (selector === "#drawer-tab" ? tab : null) },
    console,
  };
  sandbox.globalThis = sandbox;
  vm.createContext(sandbox);
  vm.runInContext(tabScript, sandbox);
  return { tab, bridge };
}

function press(tab) {
  for (const fn of tab.listeners.click || []) fn({});
}

test("the tab is a button that opens the drawer", () => {
  const { tab, bridge } = tabScriptRun();
  press(tab);
  assert.equal(bridge.opened, 1, "the tab did not open the drawer");
});

test("the tab is on the middle right of the screen", () => {

  assert.match(tabCss, /\.drawer-tab\s*\{[^}]*right:\s*0/, "the tab is not on the right");
  assert.match(tabCss, /\.drawer-tab\s*\{[^}]*top:\s*50%/, "the tab is not in the middle");

  assert.match(
    mainSource,
    /x:\s*Math\.max\(0, width - tabWidth\)[\s\S]*?y:\s*Math\.round\(\(height - tabHeight\) \/ 2\)/,
    "the tab's view is not on the middle right of the screen",
  );
});

test("the tab is wide enough to press with a finger", () => {

  const width = 1280;
  const viewWidth = Math.min(56, width);
  const tabWidth = Number(/width:\s*(\d+)px/.exec(tabCss)?.[1]);
  assert.ok(tabWidth, "the tab has no width");
  const viewLeft = width - viewWidth;
  const tabLeft = viewLeft + viewWidth - tabWidth;
  const visible = Math.min(width, tabLeft + tabWidth) - Math.max(0, tabLeft);
  assert.ok(
    visible >= 40,
    `the tab is only ${visible}px wide on the screen, which is too narrow to press`,
  );
});

test("the tab is a button in the page", () => {

  assert.match(tabHtml, /<button id="drawer-tab"/, "the tab is not a button");
  assert.match(tabHtml, /aria-label="Open quick controls"/, "the tab says nothing about what it does");
});

test("the tab is put away while the drawer is open", () => {

  const { tab, bridge } = tabScriptRun();
  for (const state of bridge.states) state(true);
  assert.equal(tab.hidden, true, "the tab was left showing under the drawer");
  assert.equal(tab.getAttribute("aria-expanded"), "false");
});

test("the tab comes back when the drawer is shut", () => {
  const { tab, bridge } = tabScriptRun();
  for (const state of bridge.states) state(true);
  for (const state of bridge.states) state(false);
  assert.equal(tab.hidden, false, "the tab did not come back");
  assert.equal(tab.getAttribute("aria-expanded"), "true");
});

test("the tab is a view of its own, so it does not swallow the dashboard's taps", () => {

  assert.match(mainSource, /function createTabView\(\)/, "the tab has no view of its own");
  assert.match(mainSource, /tabView\.setBounds\(\{/, "the tab is not given a place on the screen");
  assert.match(mainSource, /ui", "tab\.html"/, "the tab does not load its own page");
});

test("the tab is put away while the wake word overlay is up", () => {

  assert.match(
    mainSource,
    /wanted = !sidePanelOpen && !voiceOverlayFull/,
    "the tab is left showing under the wake word overlay",
  );
});

test("the tab is put away while something is drawn over the dashboard", () => {

  assert.match(
    mainSource,
    /!sidePanelOpen && !voiceOverlayFull && !overlayExpanded && !keyboardVisible/,
    "the tab is left showing over the settings page or the keyboard",
  );
  assert.match(
    mainSource,
    /removeChildView\(tabView\)/,
    "the tab is never taken off the screen",
  );
});

test("the edge swipe that opened the drawer is gone", () => {

  const remotePreload = fs.readFileSync(path.join(root, "src", "remote-preload.js"), "utf8");
  const renderer = fs.readFileSync(path.join(root, "src", "ui", "renderer.js"), "utf8");

  assert.doesNotMatch(remotePreload, /swipe/i, "the swipe is still in the remote page");
  assert.doesNotMatch(mainSource, /remote:open-side-panel/, "the swipe's channel is still there");
  assert.doesNotMatch(renderer, /SWIPE/i, "the swipe is still in the drawer");

  assert.match(renderer, /endDrawerDrag/, "the drawer cannot be dragged shut");
});

test("the drawer is still dragged shut", () => {

  const renderer = fs.readFileSync(path.join(root, "src", "ui", "renderer.js"), "utf8");
  assert.match(renderer, /dragDrawer\(dragState\.start - dx \/ drawerWidth\(\)\)/, "the drawer does not follow the finger");
  assert.match(renderer, /dragState = \{ start: 1, active: true \}/, "the drawer cannot be dragged shut");
});
