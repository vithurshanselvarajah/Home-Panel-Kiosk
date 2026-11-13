const test = require("node:test");
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const vm = require("node:vm");

// The drawer's buttons.
//
// The drawer closes when an action is picked, so that the panel does
// not linger over the view. But the media panel has buttons of its own,
// and closing the drawer on every one of them makes the music impossible
// to control.
//
// This is checked by running the script and pressing the buttons, rather
// than by looking for a line of code: a check for a line of code is
// satisfied by the line being there, whether it works or not.

// The pieces of the page the drawer needs.
const root = path.join(__dirname, "..");
const html = fs.readFileSync(path.join(root, "src", "ui", "index.html"), "utf8");
const renderer = fs.readFileSync(path.join(root, "src", "ui", "renderer.js"), "utf8");

// element is a small piece of the page.
//
// It is enough of one for the drawer's handler: it knows its parent,
// its children and its own attributes.
function element(tag, attrs = {}, children = []) {
  const el = {
    tagName: tag.toUpperCase(),
    id: attrs.id || "",
    className: attrs.class || "",
    dataset: { ...(attrs.dataset || {}) },
    style: {},
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
    removeAttribute(name) { delete this.attributes[name]; },
    appendChild(child) { child.parent = this; this.children.push(child); return child; },
    querySelector(selector) { return find(this, selector); },
    querySelectorAll(selector) { return findAll(this, selector); },
    closest(selector) {
      let node = this;
      while (node) {
        if (matches(node, selector)) return node;
        node = node.parent;
      }
      return null;
    },
    getBoundingClientRect() { return { width: 330, height: 800, left: 0, top: 0 }; },
    focus() {},
  };
  for (const child of children) el.appendChild(child);
  return el;
}

// find finds the first descendant that matches.
function find(root, selector) {
  for (const child of root.children) {
    if (matches(child, selector)) return child;
    const found = find(child, selector);
    if (found) return found;
  }
  return null;
}

// findAll finds every descendant that matches.
function findAll(root, selector) {
  const out = [];
  for (const child of root.children) {
    if (matches(child, selector)) out.push(child);
    out.push(...findAll(child, selector));
  }
  return out;
}

// matches says whether an element matches a simple selector.
function matches(el, selector) {
  if (selector.startsWith(".")) return el.classList.contains(selector.slice(1));
  if (selector.startsWith("#")) return el.id === selector.slice(1);
  return el.tagName === selector.toUpperCase();
}

// The page, with the drawer's buttons.
//
// It is the drawer's own markup, so the test presses the real buttons.
function page() {
  const drawer = element("aside", { id: "side-panel", class: "side-panel open" });
  const backdrop = element("div", { id: "side-panel-backdrop" });

  // The music panel, with its transport, its source buttons and its
  // scrubber.
  const mediaPanel = element("section", { id: "media-panel", class: "media-panel" });
  const previous = element("button", { id: "media-previous", dataset: { transport: "previous" } });
  const play = element("button", { id: "media-play", dataset: { transport: "play" } });
  const next = element("button", { id: "media-next", dataset: { transport: "next" } });
  const sourceServer = element("button", { id: "media-source-server", dataset: { source: "server" } });
  const sourceHome = element("button", { id: "media-source-home", dataset: { source: "home" } });
  const scrubber = element("input", { id: "media-position" });
  mediaPanel.appendChild(previous);
  mediaPanel.appendChild(play);
  mediaPanel.appendChild(next);
  mediaPanel.appendChild(sourceServer);
  mediaPanel.appendChild(sourceHome);
  mediaPanel.appendChild(scrubber);

  // The quick controls, with the volume's mute button.
  const quick = element("section", { class: "quick-controls" });
  const mute = element("button", { id: "volume-mute" });
  quick.appendChild(mute);

  // The drawer's own actions.
  const dashboard = element("button", { id: "dashboard-button", dataset: { action: "dashboard" } });
  const settings = element("button", { id: "settings-button" });
  const displayMode = element("button", { id: "display-mode-button" });
  const exit = element("button", { id: "exit-button" });
  drawer.appendChild(mediaPanel);
  drawer.appendChild(quick);
  drawer.appendChild(dashboard);
  drawer.appendChild(settings);
  drawer.appendChild(displayMode);
  drawer.appendChild(exit);

  return { drawer, backdrop, mediaPanel, previous, play, next, sourceServer, sourceHome, scrubber, quick, mute, dashboard, settings, displayMode, exit };
}

// press clicks a button.
//
// The click travels up the tree, the way a real click does, so a
// listener on the drawer hears a press on a button inside it.
function press(button) {
  const event = { target: button, preventDefault() {}, stopPropagation() {} };
  let node = button;
  while (node) {
    for (const fn of node.listeners.click || []) fn(event);
    node = node.parent;
  }
}

// The drawer handler, run on its own.
//
// The script is one file of several hundred lines, and it draws the
// whole page, so the handler is taken out of it and run on the page
// above. The handler is the part that decides whether the drawer
// closes, so that is the part that matters.
function handler(page) {
  // The part of the script that closes the drawer.
  const source = renderer.slice(
    renderer.indexOf("drawer.addEventListener(\"click\""),
    renderer.indexOf('$("#notice").addEventListener("click"'),
  );
  const closed = [];
  const setDrawer = (open) => {
    if (!open) closed.push(true);
    page.drawer.classList.toggle("open", open);
  };
  const sandbox = {
    window: {
      homePanel: {
        quit() {}, setDisplayMode() {}, command() {}, transport() {},
        setBrightness() {}, setVolume() {},
      },
    },
    document: {
      querySelector: (selector) => find(page, selector),
      querySelectorAll: (selector) => findAll(page, selector),
      addEventListener() {},
    },
    setInterval() { return 0; },
    setTimeout() { return 0; },
    clearInterval() {},
    console,
  };
  sandbox.globalThis = sandbox;
  vm.createContext(sandbox);
  // The script's own variables, which the handler uses.
  sandbox.bindings = {
    drawer: page.drawer,
    drawerBackdrop: page.backdrop,
    mediaPanel: page.mediaPanel,
    mediaSourceServer: page.sourceServer,
    mediaSourceHome: page.sourceHome,
    mediaPosition: page.scrubber,
    mediaPrevious: page.previous,
    mediaPlay: page.play,
    mediaNext: page.next,
    setDrawer,
    openSettings: () => {},
  };
  const names = Object.keys(sandbox.bindings).join(", ");
  vm.runInContext(
    `const { ${names} } = bindings; const displayMode = "window"; ${source}`,
    sandbox,
  );
  return { closed, setDrawer };
}

test("the drawer's own actions close the drawer", () => {
  const page = build();
  const { closed } = handler(page);
  press(page.dashboard);
  assert.equal(closed.length, 1, "the drawer did not close when an action was picked");
});

test("the music's transport buttons leave the drawer open", () => {
  const page = build();
  const { closed } = handler(page);
  press(page.play);
  assert.equal(closed.length, 0, "the drawer closed when the music was played");
});

test("the music's source buttons leave the drawer open", () => {
  const page = build();
  const { closed } = handler(page);
  press(page.sourceServer);
  assert.equal(closed.length, 0, "the drawer closed when the music source was chosen");
  press(page.sourceHome);
  assert.equal(closed.length, 0, "the drawer closed when the music source was chosen");
});

test("the music's scrubber leaves the drawer open", () => {
  const page = build();
  const { closed } = handler(page);
  // The scrubber is an input, so the drawer's handler does not see it as
  // a button at all.
  for (const fn of page.scrubber.parent.listeners.click || []) {
    fn({ target: page.scrubber, preventDefault() {}, stopPropagation() {} });
  }
  assert.equal(closed.length, 0, "the drawer closed when the music was scrubbed");
});

test("the mute button leaves the drawer open", () => {
  // The mute is a button inside the drawer, and the drawer used to
  // close on every button.
  const page = build();
  const { closed } = handler(page);
  press(page.mute);
  assert.equal(closed.length, 0, "the drawer closed when the sound was muted");
});

test("the settings button closes the drawer", () => {
  // The settings panel is drawn over the drawer, so the drawer is
  // closed when it opens.
  const page = build();
  const { closed } = handler(page);
  press(page.settings);
  assert.equal(closed.length, 1, "the drawer stayed open when the settings were opened");
});

// build makes the page the handler runs on.
function build() {
  const page = element("div");
  Object.assign(page, page0());
  return page;
}

// page0 makes the drawer's parts.
function page0() {
  const parts = page();
  const root = element("div");
  root.appendChild(parts.drawer);
  root.appendChild(parts.backdrop);
  return { ...parts, root };
}
