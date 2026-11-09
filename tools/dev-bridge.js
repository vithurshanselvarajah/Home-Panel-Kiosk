// Serves the Home Panel interface to a browser.
//
// The interface is an Electron overlay: a page that talks to the main
// process over IPC. A browser has no IPC, so this serves the same page and
// gives it the same API over a WebSocket instead.
//
// It is for development and demonstration. The real device runs the
// Electron app; this runs the same interface in a tab, which is what
// makes the interface reviewable without a screen.
//
//   npm run dev:ui
//
// The bridge owns the sidecar itself, so the EchoMuse state it reports is
// the real state of a real sidecar talking to a real controller.

const http = require("node:http");
const fs = require("node:fs");
const fsp = require("node:fs/promises");
const path = require("node:path");
const { WebSocketServer } = require("ws");
const { EchoMuseClient, findSidecar } = require("../src/echomuse");
const { editFieldValue } = require("../src/utils");

const root = path.join(__dirname, "..");
const uiDir = path.join(root, "src", "ui");
const port = Number(process.env.DEV_UI_PORT || 8769);

const userData = path.join(process.env.XDG_CONFIG_HOME || path.join(process.env.HOME, ".config"), "homepanel-kiosk");
const stateFile = path.join(userData, "echomuse-state.json");
const configFile = path.join(userData, "echomuse-config.json");
const credentialsDir = path.join(userData, "echomuse");

// The settings the interface reads and writes.
const settingsPath = path.join(userData, "settings.json");

const MIME = {
  ".html": "text/html; charset=utf-8",
  ".js": "text/javascript; charset=utf-8",
  ".css": "text/css; charset=utf-8",
  ".svg": "image/svg+xml",
  ".png": "image/png",
};

// The state the interface is told about. The drawer and the settings panel
// both read it.
let state = {
  mode: "dashboard",
  displayIsOff: false,
  keyboardVisible: false,
  sidePanelOpen: false,
  displayMode: "fullscreen",
  serviceStatus: { mqtt: "unknown", immich: "unknown", dashboard: "unknown" },
  lastActivity: new Date().toISOString(),
};

async function readSettings() {
  try {
    return JSON.parse(await fsp.readFile(settingsPath, "utf8"));
  } catch {
    return {};
  }
}

async function writeSettings(next) {
  const current = await readSettings();
  const merged = { ...current, ...next };
  const temporary = `${settingsPath}.tmp`;
  await fsp.writeFile(temporary, JSON.stringify(merged, null, 2), { mode: 0o600 });
  await fsp.rename(temporary, settingsPath);
  return merged;
}

const sockets = new Set();

function broadcast(channel, payload) {
  const message = JSON.stringify({ channel, payload });
  for (const socket of sockets) {
    if (socket.readyState === 1) socket.send(message);
  }
}

// The sidecar. The interface is shown what it reports, so what is on screen
// is what the device is doing.
const binary = findSidecar(root, root, process.execPath);
const client = new EchoMuseClient({
  binary,
  stateFile,
  configFile,
  credentialsDir,
  deviceId: "test-instance",
  board: "test-instance",
  controllerUrl: process.env.ECHOMUSE_CONTROLLER_URL || "",
  pair: true,
  onState: (echomuseState) => broadcast("app:echomuse", echomuseState),
  onLog: (message) => { if (message) console.log("echomuse:", message); },
});

async function startSidecar() {
  const settings = await readSettings();
  if (settings.echomuseEnabled === false) {
    console.log("dev: the EchoMuse link is off in the settings");
    return;
  }
  fs.mkdirSync(credentialsDir, { recursive: true, mode: 0o700 });
  if (!client.start()) {
    console.log("dev: the sidecar was not started");
    return;
  }
  // The configuration is written before the sidecar reads it, so the
  // settings are in force from the first frame.
  await client.sendConfig({
    board: settings.echomuseBoard || "test-instance",
    wakeThreshold: Number(settings.echomuseWakeThreshold) || undefined,
    wakeWordEnabled: typeof settings.echomuseWakeWordEnabled === "boolean" ? settings.echomuseWakeWordEnabled : undefined,
    audioInput: settings.echomuseAudioInput || "",
    audioOutput: settings.echomuseAudioOutput || "",
  });
}

const server = http.createServer(async (request, response) => {
  const url = new URL(request.url, `http://localhost:${port}`);
  const name = url.pathname === "/" ? "/index.html" : url.pathname;
  // The path is resolved against the interface directory and then checked
  // to be inside it, so a request cannot read a file outside it.
  const file = path.join(uiDir, path.normalize(name));
  if (!file.startsWith(uiDir)) {
    response.writeHead(403).end("Forbidden");
    return;
  }
  try {
    const body = await fsp.readFile(file);
    response.writeHead(200, { "Content-Type": MIME[path.extname(file)] || "application/octet-stream" });
    response.end(body);
  } catch {
    response.writeHead(404).end("Not found");
  }
});

const wss = new WebSocketServer({ server });

wss.on("connection", (socket) => {
  sockets.add(socket);
  socket.on("close", () => sockets.delete(socket));

  // The interface asks for its settings as soon as it loads.
  socket.send(JSON.stringify({ channel: "app:state", payload: state }));
  socket.send(JSON.stringify({ channel: "app:echomuse", payload: client.state }));

  socket.on("message", async (raw) => {
    let message;
    try { message = JSON.parse(String(raw)); } catch { return; }
    const { channel, payload, id } = message;
    // A request that asked for an answer is answered to whoever asked.
    const reply = (value) => socket.send(JSON.stringify({ channel, payload: value, id }));

    if (channel === "app:get-settings") {
      reply(await readSettings());
      return;
    }
    if (channel === "app:save-settings") {
      const saved = await writeSettings(payload || {});
      // The device is told the settings, the same way the application does
      // it, so a saved change reaches the device rather than waiting for
      // it to be restarted.
      await client.sendConfig({
        board: saved.echomuseBoard || "",
        audioInput: saved.echomuseAudioInput || "",
        audioOutput: saved.echomuseAudioOutput || "",
      });
      reply(saved);
      return;
    }
    if (channel === "app:command") {
      const action = payload?.action;
      if (action === "dashboard" || action === "slideshow") {
        state.mode = action === "dashboard" ? "dashboard" : "slideshow";
        broadcast("app:state", state);
      }
      if (action === "dismiss_notification") broadcast("app:dismiss-notification", null);
      reply(true);
      return;
    }
    if (channel === "app:side-panel") {
      state.sidePanelOpen = Boolean(payload);
      broadcast("app:state", state);
      reply(true);
      return;
    }
    if (channel === "app:keyboard") {
      state.keyboardVisible = Boolean(payload);
      broadcast("app:state", state);
      reply(true);
      return;
    }
    if (channel === "app:overlay-expanded" || channel === "app:activity") { reply(true); return; }
    if (channel === "app:keyboard-key") { reply(true); return; }
    if (channel === "app:test-sound") {
      // The counter is what tells the device a new tone was asked for.
      const saved = await readSettings();
      const counter = (saved.echomuseTestSound || 0) + 1;
      await writeSettings({ echomuseTestSound: counter });
      await client.sendConfig({ testSound: counter });
      reply(counter);
      return;
    }
    if (channel === "app:monitor-mic") {
      await writeSettings({ echomuseMonitorMic: Boolean(payload) });
      await client.sendConfig({ monitorMic: Boolean(payload) });
      reply(Boolean(payload));
      return;
    }
    reply(true);
  });
});

server.listen(port, () => {
  console.log(`dev: the interface is at http://localhost:${port}`);
  startSidecar();
});

// The sidecar holds the microphone and the speaker, and it releases them
// when it exits. The exit waits for it, so the next thing to want the
// sound card finds it free.
process.on("exit", () => client.stop());
for (const signal of ["SIGINT", "SIGTERM"]) {
  process.on(signal, () => {
    client.stop().finally(() => process.exit(0));
  });
}
