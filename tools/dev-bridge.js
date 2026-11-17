

const http = require("node:http");
const fs = require("node:fs");
const fsp = require("node:fs/promises");
const path = require("node:path");
const os = require("node:os");
const { WebSocketServer } = require("ws");
const { DeviceClient, findSidecar } = require("../src/device");
const { systemVolumePercent, setSystemVolume } = require("../src/volume");
const { editFieldValue } = require("../src/utils");

const root = path.join(__dirname, "..");
const uiDir = path.join(root, "src", "ui");
const port = Number(process.env.DEV_UI_PORT || 8769);
const hostName = (os.hostname() || "home-panel").trim();

const userData = path.join(process.env.XDG_CONFIG_HOME || path.join(process.env.HOME, ".config"), "homepanel-kiosk");
const configFile = path.join(userData, "device-config.json");

const dataDirectory = path.join(userData, "device");

const settingsPath = path.join(userData, "settings.json");

const MIME = {
  ".html": "text/html; charset=utf-8",
  ".js": "text/javascript; charset=utf-8",
  ".css": "text/css; charset=utf-8",
  ".svg": "image/svg+xml",
  ".png": "image/png",
};

let state = {
  mode: "dashboard",
  displayIsOff: false,
  keyboardVisible: false,
  sidePanelOpen: false,
  displayMode: "fullscreen",
  brightness: 60,
  volume: 60,
  version: "0.0.3",
  serviceStatus: { control: "unknown", immich: "unknown", dashboard: "unknown" },
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

const binary = findSidecar(root, root, process.execPath);
const client = new DeviceClient({
  binary,
  stateFile: "",
  levelFile: "",
  configFile,
  dataDirectory,
  deviceId: "",
  board: "",
  onState: (deviceState) => broadcast("app:device", deviceState),
  onLog: (message) => { if (message) console.log("device:", message); },
});

async function startSidecar() {
  const settings = await readSettings();

  client.deviceId = hostName;
  client.board = hostName;

  const runtimeDir = path.join("/dev/shm/homepanel", client.deviceId.replace(/[^a-zA-Z0-9_-]/g, "-") || "device");
  client.stateFile = path.join(runtimeDir, "state.json");
  client.levelFile = path.join(runtimeDir, "level");
  fs.mkdirSync(dataDirectory, { recursive: true, mode: 0o700 });
  if (!client.start()) {
    console.log("dev: the sidecar was not started");
    return;
  }

  await client.sendConfig({
    board: client.board,
    wakeThreshold: Number(settings.voiceWakeThreshold) || undefined,
    wakeWordEnabled: typeof settings.voiceWakeWordEnabled === "boolean" ? settings.voiceWakeWordEnabled : undefined,
    audioInput: settings.voiceAudioInput || "",
    audioOutput: settings.voiceAudioOutput || "",
  });
}

const server = http.createServer(async (request, response) => {
  const url = new URL(request.url, `http://localhost:${port}`);
  const name = url.pathname === "/" ? "/index.html" : url.pathname;

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

  socket.send(JSON.stringify({ channel: "app:state", payload: state }));
  socket.send(JSON.stringify({ channel: "app:device", payload: client.state }));

  socket.on("message", async (raw) => {
    let message;
    try { message = JSON.parse(String(raw)); } catch { return; }
    const { channel, payload, id } = message;

    const reply = (value) => socket.send(JSON.stringify({ channel, payload: value, id }));

    if (channel === "app:get-settings") {
      reply(await readSettings());
      return;
    }
    if (channel === "app:save-settings") {
      const saved = await writeSettings(payload || {});

      await client.sendConfig({
        board: hostName,
        audioInput: saved.voiceAudioInput || "",
        audioOutput: saved.voiceAudioOutput || "",
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
    if (channel === "app:set-brightness") {

      state.brightness = Math.max(0, Math.min(100, Math.round(Number(payload) || 0)));
      broadcast("app:state", state);
      reply(state.brightness);
      return;
    }
    if (channel === "app:set-volume") {

      const volume = Math.max(0, Math.min(100, Math.round(Number(payload) || 0)));
      await setSystemVolume(volume);
      state.volume = volume;
      broadcast("app:state", state);
      reply(volume);
      return;
    }
    if (channel === "app:overlay-expanded" || channel === "app:activity") { reply(true); return; }
    if (channel === "app:transport") {

      reply(true);
      return;
    }
    if (channel === "app:keyboard-key") { reply(true); return; }
    if (channel === "app:test-sound") {

      const saved = await readSettings();
      const counter = (saved.voiceTestSound || 0) + 1;
      await writeSettings({ voiceTestSound: counter });
      await client.sendConfig({ testSound: counter });
      reply(counter);
      return;
    }
    if (channel === "app:monitor-mic") {
      await writeSettings({ voiceMonitorMic: Boolean(payload) });
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

process.on("exit", () => client.stop());
for (const signal of ["SIGINT", "SIGTERM"]) {
  process.on(signal, () => {
    client.stop().finally(() => process.exit(0));
  });
}
