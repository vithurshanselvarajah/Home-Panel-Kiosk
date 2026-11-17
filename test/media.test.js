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
  const mediaPanel = element("section", { id: "media-panel", class: "media-panel" });
  const previous = element("button", { id: "media-previous", dataset: { transport: "previous" } });
  const play = element("button", { id: "media-play", dataset: { transport: "play" } });
  const next = element("button", { id: "media-next", dataset: { transport: "next" } });
  const sourceServer = element("button", { id: "media-source-server", dataset: { source: "server" } });
  const sourceHome = element("button", { id: "media-source-home", dataset: { source: "home" } });
  const scrubber = element("input", { id: "media-position" });
  const artwork = element("img", { id: "media-artwork" });
  const title = element("span", { id: "media-title" });
  const artist = element("span", { id: "media-artist" });
  const elapsed = element("span", { id: "media-elapsed" });
  const duration = element("span", { id: "media-duration" });
  mediaPanel.appendChild(previous);
  mediaPanel.appendChild(play);
  mediaPanel.appendChild(next);
  mediaPanel.appendChild(sourceServer);
  mediaPanel.appendChild(sourceHome);
  mediaPanel.appendChild(scrubber);
  mediaPanel.appendChild(artwork);
  mediaPanel.appendChild(title);
  mediaPanel.appendChild(artist);
  mediaPanel.appendChild(elapsed);
  mediaPanel.appendChild(duration);
  root.appendChild(mediaPanel);
  return { root, mediaPanel, previous, play, next, sourceServer, sourceHome, scrubber, artwork, title, artist, elapsed, duration };
}

function media(page, sent) {
  const start = renderer.indexOf('const mediaPanel = $("#media-panel");');
  const end = renderer.indexOf("const statusLabels = {");
  const source = renderer.slice(start, end);

  const timers = [];
  const sandbox = {
    window: {
      homePanel: {
        transport(command) { sent.push(command); },
      },
    },
    document: {
      querySelector: (selector) => find(page.root, selector),
      querySelectorAll: (selector) => findAll(page.root, selector),
      addEventListener() {},
    },

    $: (selector) => find(page.root, selector),
    setInterval() { return 0; },
    setTimeout(fn, ms) { timers.push(fn); return timers.length; },
    clearTimeout() {},
    Date, Math, Number, Boolean, Array, Object,
    console,
  };
  sandbox.globalThis = sandbox;
  vm.createContext(sandbox);
  vm.runInContext(source, sandbox);
  return { sandbox, timers };
}

function press(button) {
  const event = { target: button, preventDefault() {}, stopPropagation() {} };
  for (const fn of button.listeners.click || []) fn(event);
}

function playingState() {
  return {
    music_title: "Aurora",
    music_artist: "Vithusel",
    music_artwork: "",
    music_position: 1000,
    music_duration: 200000,
    music_playing: true,
    music_transport: ["play", "pause", "next", "previous", "seek"],
    media_state: "idle",
  };
}

test("the play button pauses what is playing", () => {
  const page = buildPage();
  const sent = [];
  const { sandbox } = media(page, sent);

  sandbox.paintMedia(playingState());

  assert.ok(
    page.play.innerHTML.includes("<svg"),
    "the button does not show the pause sign",
  );
  assert.equal(page.play.dataset.transport, "pause", "the button does not send pause");

  press(page.play);
  assert.equal(sent.length, 1, "the button sent more than one command");
  assert.equal(sent[0].action, "pause", "the button did not send pause");
  assert.equal(sent[0].source, "server", "the button sent the command to the wrong music");
});

test("the play button plays what is paused", () => {
  const page = buildPage();
  const sent = [];
  const { sandbox } = media(page, sent);

  sandbox.paintMedia({ ...playingState(), music_playing: false });

  assert.ok(page.play.innerHTML.includes("<svg"), "the button does not show the play sign");
  assert.equal(page.play.dataset.transport, "play", "the button does not send play");

  press(page.play);
  assert.equal(sent.length, 1, "the button sent more than one command");
  assert.equal(sent[0].action, "play", "the button did not send play");
  assert.equal(sent[0].source, "server", "the button sent the command to the wrong music");
});

test("the button waits while the music is on its way", () => {
  const page = buildPage();
  const sent = [];
  const { sandbox } = media(page, sent);

  sandbox.paintMedia({ ...playingState(), music_playing: false });
  press(page.play);

  assert.ok(page.play.classList.contains("is-loading"), "the button did not show that it was waiting");
  assert.equal(page.play.textContent, "", "the button still shows a sign while it waits");
});

test("the wait ends when the music starts", () => {
  const page = buildPage();
  const sent = [];
  const { sandbox } = media(page, sent);

  sandbox.paintMedia({ ...playingState(), music_playing: false });
  press(page.play);
  assert.ok(page.play.classList.contains("is-loading"), "the button did not start waiting");

  sandbox.paintMedia(playingState());

  assert.ok(!page.play.classList.contains("is-loading"), "the button is still waiting");
  assert.ok(page.play.innerHTML.includes("<svg"), "the button does not show the pause sign");
});

test("the wait ends by itself when the music never comes", () => {
  const page = buildPage();
  const sent = [];
  const { sandbox, timers } = media(page, sent);

  sandbox.paintMedia({ ...playingState(), music_playing: false });
  press(page.play);
  assert.ok(page.play.classList.contains("is-loading"), "the button did not start waiting");

  timers[timers.length - 1]();

  assert.ok(!page.play.classList.contains("is-loading"), "the button is still waiting for ever");
  assert.ok(page.play.innerHTML.includes("<svg"), "the button did not go back to the play sign");
});

test("the wait does not follow the wrong music", () => {
  const page = buildPage();
  const sent = [];
  const { sandbox } = media(page, sent);

  sandbox.paintMedia(playingState());
  sandbox.mediaSource = "home";

  sandbox.paintMedia({ ...playingState(), media_state: "playing" });

  assert.ok(!page.play.classList.contains("is-loading"), "the wait ended on the wrong music");
});
