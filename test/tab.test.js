const test = require("node:test");
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const vm = require("node:vm");

// The pull out tab.
//
// The tab is the way the drawer is opened. It used to be a swipe across the
// screen, which was easy to miss, so there is a tab instead.
//
// The tab is a page of its own in a view of its own, because a view that is
// over the dashboard swallows the taps meant for the dashboard. That is why
// it cannot simply be part of the overlay page.
//
// These tests run the tab's script and press the tab, rather than looking
// for a line of code: a check for a line of code is satisfied by the line
// being there, whether it works or not.

const root = path.join(__dirname, "..");
const tabHtml = fs.readFileSync(path.join(root, "src", "ui", "tab.html"), "utf8");
const tabScript = fs.readFileSync(path.join(root, "src", "ui", "tab.js"), "utf8");
const tabCss = fs.readFileSync(path.join(root, "src", "ui", "tab.css"), "utf8");
const mainSource = fs.readFileSync(path.join(root, "src", "main.js"), "utf8");

// The tab's own script, run on a stub of the tab.
//
// The script finds the tab, listens for a click on it, and asks the main
// process to open the drawer. The bridge is what it talks to, so the stub
// records what it was asked to do.
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

// press clicks the tab.
function press(tab) {
  for (const fn of tab.listeners.click || []) fn({});
}

test("the tab is a button that opens the drawer", () => {
  const { tab, bridge } = tabScriptRun();
  press(tab);
  assert.equal(bridge.opened, 1, "the tab did not open the drawer");
});

test("the tab is on the middle right of the screen", () => {
  // The tab is on the right, hanging over the edge so that it reads as
  // something to pull out, and it is in the middle of the screen.
  assert.match(tabCss, /\.drawer-tab\s*\{[^}]*right:\s*0/, "the tab is not on the right");
  assert.match(tabCss, /\.drawer-tab\s*\{[^}]*top:\s*50%/, "the tab is not in the middle");
  // The view the tab lives in is put on the right hand edge of the screen
  // and in the middle of it, so that is where the tab is seen.
  assert.match(
    mainSource,
    /x:\s*Math\.max\(0, width - tabWidth\)[\s\S]*?y:\s*Math\.round\(\(height - tabHeight\) \/ 2\)/,
    "the tab's view is not on the middle right of the screen",
  );
});

test("the tab is wide enough to press with a finger", () => {
  // The tab hangs over the right hand edge of the screen, so the part of
  // it that is off the screen cannot be pressed. The part that is on the
  // screen has to be a finger's width, or the tab is a target that is
  // missed.
  //
  // The device is 1280 wide. The view is on the right hand edge and the
  // tab is wider than the view, so the visible part is the tab's own
  // width less the part that hangs over.
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
  // A button is something that is pressed. A div with a click handler is
  // not, as far as a person who cannot see the screen is concerned.
  assert.match(tabHtml, /<button id="drawer-tab"/, "the tab is not a button");
  assert.match(tabHtml, /aria-label="Open quick controls"/, "the tab says nothing about what it does");
});

test("the tab is put away while the drawer is open", () => {
  // The drawer is over the place the tab sits in, so a tab under the drawer
  // would be a tab that cannot be seen or pressed.
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
  // A view that is over the dashboard takes the taps meant for the
  // dashboard. The tab is the only thing allowed to be over it, and it is
  // only as big as the tab.
  assert.match(mainSource, /function createTabView\(\)/, "the tab has no view of its own");
  assert.match(mainSource, /tabView\.setBounds\(\{/, "the tab is not given a place on the screen");
  assert.match(mainSource, /ui", "tab\.html"/, "the tab does not load its own page");
});

test("the tab is put away while the wake word overlay is up", () => {
  // The wake word overlay covers the whole screen, so the tab is under it.
  assert.match(
    mainSource,
    /wanted = !sidePanelOpen && !voiceOverlayFull/,
    "the tab is left showing under the wake word overlay",
  );
});

test("the tab is put away while something is drawn over the dashboard", () => {
  // The settings page and the notification are over the dashboard, and the
  // touch keyboard is along the bottom, which is where the tab is. The tab
  // is only wanted when the dashboard is showing plainly.
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
  // The swipe is replaced by the tab. The gesture is easy to miss, and a
  // gesture that has to be found is a gesture that is not found.
  const remotePreload = fs.readFileSync(path.join(root, "src", "remote-preload.js"), "utf8");
  const renderer = fs.readFileSync(path.join(root, "src", "ui", "renderer.js"), "utf8");

  assert.doesNotMatch(remotePreload, /swipe/i, "the swipe is still in the remote page");
  assert.doesNotMatch(mainSource, /remote:open-side-panel/, "the swipe's channel is still there");
  assert.doesNotMatch(renderer, /SWIPE/i, "the swipe is still in the drawer");
  // The drawer is still dragged shut, which is a different gesture: it
  // starts on the drawer, and it is not how the drawer is opened.
  assert.match(renderer, /endDrawerDrag/, "the drawer cannot be dragged shut");
});

test("the drawer is still dragged shut", () => {
  // The drag of the open drawer stays: it is a gesture on the drawer
  // itself, and it is not the gesture that opened it.
  const renderer = fs.readFileSync(path.join(root, "src", "ui", "renderer.js"), "utf8");
  assert.match(renderer, /dragDrawer\(dragState\.start - dx \/ drawerWidth\(\)\)/, "the drawer does not follow the finger");
  assert.match(renderer, /dragState = \{ start: 1, active: true \}/, "the drawer cannot be dragged shut");
});
