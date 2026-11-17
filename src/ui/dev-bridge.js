

(function () {
  if (window.homePanel) return;

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

  const listeners = { "app:state": [], "app:device": [], "app:notification": [], "app:dismiss-notification": [] };

  const latest = {};
  const pending = new Map();
  let nextId = 1;

  const socket = new WebSocket(`ws://${location.host}`);
  socket.addEventListener("open", () => {

    send("app:get-settings", null);
  });
  socket.addEventListener("message", (event) => {
    let message;
    try { message = JSON.parse(event.data); } catch { return; }
    const { channel, payload, id } = message;

    if (id !== undefined && pending.has(id)) {
      const resolve = pending.get(id);
      pending.delete(id);
      resolve(payload);
      return;
    }
    latest[channel] = payload;
    for (const fn of listeners[channel] || []) fn(payload);
  });

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
    onVolumePopup: (callback) => on("app:volume-popup", callback),
    onMuteIndicator: (callback) => on("app:mute-indicator", callback),
    setSidePanel: (open) => notify("app:side-panel", open),
    setKeyboard: (visible) => notify("app:keyboard", visible),
    setBrightness: (value) => send("app:set-brightness", value),
    setVolume: (value) => send("app:set-volume", value),
    voiceOverlay: (full) => notify("app:voice-overlay", full),
    transport: (command) => send("app:transport", command),
    keyboardKey: (key) => notify("app:keyboard-key", key),
    editFieldValue: (field, key) => window.__editFieldValue(field, key),
    onState: (callback) => on("app:state", callback),
    onDeviceState: (callback) => on("app:device", callback),
    onNotification: (callback) => on("app:notification", callback),
    onDismissNotification: (callback) => on("app:dismiss-notification", callback),
  };
})();
