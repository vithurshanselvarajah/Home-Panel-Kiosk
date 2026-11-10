// The browser's stand-in for the Electron preload bridge.
//
// The real bridge is `src/preload.js`, which talks to the main process
// over IPC. A browser has no IPC, so this talks to the development bridge
// over a WebSocket and offers the same API.
//
// It is loaded only by the development bridge, never by the packaged app:
// the interface asks for it by name and falls back to the real bridge when
// it is not there.

(function () {
  if (window.homePanel) return;

  // The keyboard's editing rules, the same ones the preload bridge offers.
  // They are here rather than shared with the preload because the preload
  // is CommonJS and this is a page, and a rule that exists twice is a rule
  // that drifts.
  const NAMED_KEYS = new Set(["BACKSPACE", "SPACE", "ENTER", "SHIFT", "TAB", "LEFT", "RIGHT"]);
  function caretStart(field, current) {
    if (Number.isInteger(field?.selectionStart)) return field.selectionStart;
    return current.length;
  }
  function insertText(field, current, text) {
    const start = caretStart(field, current);
    const end = Number.isInteger(field?.selectionEnd) ? field.selectionEnd : start;
    return { value: current.slice(0, start) + text + current.slice(end), caret: start + text.length };
  }
  window.__editFieldValue = function editFieldValue(field, key) {
    const current = typeof field?.value === "string" ? field.value : "";
    const type = field?.type || "text";
    if (type === "checkbox" || type === "radio") return null;
    if (key === "BACKSPACE") {
      const start = caretStart(field, current);
      const end = Number.isInteger(field?.selectionEnd) ? field.selectionEnd : start;
      const from = start === end ? Math.max(0, start - 1) : start;
      return { value: current.slice(0, from) + current.slice(end), caret: from };
    }
    if (key === "SPACE") return insertText(field, current, " ");
    if (NAMED_KEYS.has(key)) return null;
    if (typeof key !== "string" || !key) return null;
    if (type === "number" && !/^\d+$/.test(key)) return { value: current, caret: caretStart(field, current), rejected: true };
    return insertText(field, current, key);
  };

  const listeners = { "app:state": [], "app:echomuse": [], "app:notification": [], "app:dismiss-notification": [] };
  // The bridge sends the state as soon as it connects, which is before the
  // interface has registered its listeners. The last message of each kind is
  // kept, so a listener that arrives late still gets the current state
  // rather than nothing.
  const latest = {};
  const pending = new Map();
  let nextId = 1;

  const socket = new WebSocket(`ws://${location.host}`);
  socket.addEventListener("open", () => {
    // The interface asks for its settings as soon as it loads, so they are
    // asked for here too. The answer is matched by the request's id.
    send("app:get-settings", null);
  });
  socket.addEventListener("message", (event) => {
    let message;
    try { message = JSON.parse(event.data); } catch { return; }
    const { channel, payload, id } = message;
    // A request that asked for an answer is answered by id. Everything
    // else is a broadcast.
    if (id !== undefined && pending.has(id)) {
      const resolve = pending.get(id);
      pending.delete(id);
      resolve(payload);
      return;
    }
    latest[channel] = payload;
    for (const fn of listeners[channel] || []) fn(payload);
  });

  // send asks the bridge for something and waits for the answer.
  function send(channel, payload) {
    return new Promise((resolve) => {
      const id = nextId++;
      pending.set(id, resolve);
      socket.send(JSON.stringify({ channel, payload, id }));
    });
  }

  function notify(channel, payload) {
    if (socket.readyState === 1) socket.send(JSON.stringify({ channel, payload }));
  }

  function on(channel, callback) {
    if (!listeners[channel]) listeners[channel] = [];
    listeners[channel].push(callback);
    // A listener that arrives after the state was sent still gets it, so
    // the interface does not have to ask for the current state itself.
    if (latest[channel] !== undefined) callback(latest[channel]);
  }

  window.homePanel = {
    getSettings: () => send("app:get-settings", null),
    saveSettings: (settings) => send("app:save-settings", settings),
    command: (command) => send("app:command", command),
    setDisplayMode: (mode) => send("app:command", { action: "display_mode", mode }),
    quit: () => send("app:command", { action: "exit" }),
    testSound: () => send("app:test-sound", null),
    monitorMic: (on) => send("app:monitor-mic", on),
    activity: (source) => notify("app:activity", source),
    setOverlayExpanded: (expanded) => notify("app:overlay-expanded", expanded),
    setSidePanel: (open) => notify("app:side-panel", open),
    setKeyboard: (visible) => notify("app:keyboard", visible),
    setBrightness: (value) => send("app:set-brightness", value),
    setVolume: (value) => send("app:set-volume", value),
    keyboardKey: (key) => notify("app:keyboard-key", key),
    editFieldValue: (field, key) => window.__editFieldValue(field, key),
    onState: (callback) => on("app:state", callback),
    onEchoMuseState: (callback) => on("app:echomuse", callback),
    onNotification: (callback) => on("app:notification", callback),
    onDismissNotification: (callback) => on("app:dismiss-notification", callback),
  };
})();
