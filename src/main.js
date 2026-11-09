const { app, BrowserWindow, WebContentsView, ipcMain, screen, safeStorage } = require("electron");
const fs = require("node:fs/promises");
const fsSync = require("node:fs");
const path = require("node:path");
const { execFile } = require("node:child_process");
const os = require("node:os");
const { safeUrl, settingNumber, settingBoolean, displayMode: readDisplayMode, windowBounds, DISPLAY_MODES } = require("./utils");
const { createHealthChecker, probeHttp } = require("./health");
const { EchoMuseClient, findSidecar } = require("./echomuse");

const DEFAULTS = {
  configured: false,
  dashboardUrl: "http://homeassistant.local:8123",
  slideshowUrl: "",
  idleToSlideshowMinutes: 5,
  slideshowToOffMinutes: 15,
  displayMode: "fullscreen",
  touchKeyboardEnabled: true,
  mqttUrl: "",
  mqttUsername: "",
  mqttPassword: "",
  mqttPrefix: "homepanel",
  deviceName: "Home Panel",
  echomuseEnabled: false,
  echomuseControllerUrl: "",
  echomuseDeviceId: "",
  echomuseBoard: "home-panel",
};

let win;
let overlayView;
let settings = { ...DEFAULTS };
let mqttClient;
let mode = "dashboard";
let lastActivity = Date.now();
let idleTimer;
let offTimer;
let displayIsOff = false;
let notificationTimer;
let urlTimer;
let overlayAttached = false;
let overlayExpanded = false;
let sidePanelOpen = false;
let keyboardVisible = false;
let inputBlurTimer;
let keyboardKeySequence = 0;
const serviceStatus = { mqtt: "unknown", immich: "unknown", dashboard: "unknown" };
let lastPublishedMode;
let lastPublishedPower;
let lastPublishedDisplayMode;
let lastPublishedServiceStatus = "";
let lastPublishedAt = 0;
let lastSentState = "";
let displayCommandId = 0;
let requestedDisplayStatus = null;
let legacyCameraNames = [];
let overlayRecycleTimer;
let viewRetryTimer;
let lastPointerMoveAt = 0;
let echomuseClient;
const OVERLAY_RECYCLE_DELAY_MS = 60_000;
const VIEW_RETRY_INTERVAL_MS = 15_000;
const POINTER_MOVE_INTERVAL_MS = 5_000;
const deviceId = `homepanel_${os.hostname().toLowerCase().replace(/[^a-z0-9]/g, "").slice(0, 20) || "display"}`;
const configPath = () => path.join(app.getPath("userData"), "settings.json");

// The dashboard is rendered by the main window itself, so there is no separate
// view to keep track of.
const view = () => win?.webContents;

const healthChecker = createHealthChecker({
  onChange: (name, status) => {
    if (serviceStatus[name] === status) return;
    serviceStatus[name] = status;
    sendState();
  },
});

function probeMqtt() {
  if (!settings.mqttUrl) throw new Error("not configured");
  if (!mqttClient?.connected) throw new Error("disconnected");
}

async function probeHomeAssistant({ timeoutMs } = {}) {
  if (!settings.dashboardUrl) throw new Error("not configured");
  // Use the manifest endpoint rather than /api/, which returns the whole state
  // database and is far heavier to fetch and parse.
  await probeHttp(new URL("/manifest.json", settings.dashboardUrl).toString(), { timeoutMs });
}

async function probeImmich({ timeoutMs } = {}) {
  if (!settings.slideshowUrl) throw new Error("not configured");
  await probeHttp(settings.slideshowUrl, { timeoutMs });
}

function registerHealthProbes() {
  healthChecker.setProbes({
    mqtt: probeMqtt,
    dashboard: probeHomeAssistant,
    immich: probeImmich,
  });
}

// Touch emulation needs the DevTools protocol. Attach, apply the override and
// detach again so the protocol channel is not held open for the session.
async function enableTouchEmulation(webContents) {
  if (!webContents || webContents.isDestroyed()) return;
  try {
    if (!webContents.debugger.isAttached()) webContents.debugger.attach("1.3");
    await webContents.debugger.sendCommand("Emulation.setEmitTouchEventsForMouse", {
      configuration: "mobile",
      enabled: true,
    });
  } catch (error) {
    console.warn("Touch emulation unavailable:", error.message);
  } finally {
    try {
      if (webContents.debugger.isAttached()) webContents.debugger.detach();
    } catch { /* the view may already be gone */ }
  }
}

async function injectScrollbarCSS(webContents) {
  if (!webContents || webContents.isDestroyed()) return;
  try {
    await webContents.insertCSS(
      "html, body { scrollbar-width: none !important; -ms-overflow-style: none !important; overscroll-behavior: contain; touch-action: pan-y; } ::-webkit-scrollbar { display: none !important; width: 0 !important; height: 0 !important; } ::-webkit-scrollbar-thumb { background: transparent !important; }"
    );
  } catch (error) {
    console.warn("Scrollbar CSS injection failed:", error.message);
  }
}

async function decodeSettings(data) {
  if (data.mqttPasswordEncrypted && await safeStorage.isAsyncEncryptionAvailable()) {
    try {
      const { result } = await safeStorage.decryptStringAsync(Buffer.from(data.mqttPasswordEncrypted, "base64"));
      data.mqttPassword = result;
    } catch {}
  }
  delete data.cameras;
  delete data.mqttPasswordEncrypted;
  return { ...DEFAULTS, ...data };
}

function assertOverlaySender(event) {
  if (event.sender !== overlayView?.webContents) throw new Error("Untrusted IPC sender");
}

// Sends to the overlay, tolerating a view that is being torn down.
function sendToOverlay(channel, payload) {
  if (!overlayView?.webContents || overlayView.webContents.isDestroyed()) return;
  try {
    overlayView.webContents.send(channel, payload);
  } catch { /* the frame was disposed between the check and the send */ }
}

function sendState() {
  const state = { mode, displayIsOff, keyboardVisible, sidePanelOpen, displayMode: displayMode(), serviceStatus: { ...serviceStatus }, lastActivity: new Date(lastActivity).toISOString() };
  // Only message the overlay when something it renders has actually changed.
  const fingerprint = `${state.mode}|${state.displayIsOff}|${state.keyboardVisible}|${state.sidePanelOpen}|${state.displayMode}|${JSON.stringify(state.serviceStatus)}`;
  if (fingerprint !== lastSentState) {
    lastSentState = fingerprint;
    sendToOverlay("app:state", state);
  }
  publishState(state);
}

// Sends the EchoMuse state to the overlay.
//
// It is sent on its own channel because it changes on its own schedule, and
// the overlay draws it differently from the rest of the state.
function sendEchoMuseState() {
  if (!echomuseClient) return;
  sendToOverlay("app:echomuse", echomuseClient.state);
}

function publishState(state) {
  if (!mqttClient?.connected) return;
  const currentServiceStatus = JSON.stringify(state.serviceStatus);
  const stateChanged = state.mode !== lastPublishedMode || state.displayIsOff !== lastPublishedPower
    || state.displayMode !== lastPublishedDisplayMode || currentServiceStatus !== lastPublishedServiceStatus;
  if (!stateChanged && Date.now() - lastPublishedAt < 30_000) return;
  lastPublishedMode = state.mode;
  lastPublishedPower = state.displayIsOff;
  lastPublishedDisplayMode = state.displayMode;
  lastPublishedServiceStatus = currentServiceStatus;
  lastPublishedAt = Date.now();
  mqttClient.publish(`${settings.mqttPrefix}/state`, JSON.stringify(state), { qos: 1, retain: true });
}

function showNotice({ title = "Home Panel", message = "", duration = 8000, priority = "normal" } = {}) {
  const safeDuration = Math.max(1000, Math.min(Number(duration) || 8000, 120000));
  ensureOverlayView();
  sendToOverlay("app:notification", {
    title: String(title).slice(0, 100),
    message: String(message).slice(0, 500),
    duration: safeDuration,
    priority: ["low", "normal", "high"].includes(priority) ? priority : "normal",
  });
  clearTimeout(notificationTimer);
  notificationTimer = setTimeout(() => sendToOverlay("app:dismiss-notification"), safeDuration);
}

function cancelIdleTimers() {
  clearTimeout(idleTimer);
  clearTimeout(offTimer);
  idleTimer = undefined;
  offTimer = undefined;
}

function scheduleIdle() {
  cancelIdleTimers();
  const slideMinutes = Math.max(0, Number(settings.idleToSlideshowMinutes) || 0);
  const offMinutes = Math.max(0, Number(settings.slideshowToOffMinutes) || 0);
  const elapsed = Date.now() - lastActivity;
  if (mode === "slideshow") {
    if (offMinutes > 0) offTimer = setTimeout(() => turnDisplayOff(), offMinutes * 60_000);
    return;
  }
  if (slideMinutes > 0 && settings.slideshowUrl) {
    idleTimer = setTimeout(() => openMode("slideshow"), Math.max(0, slideMinutes * 60_000 - elapsed));
  } else if (offMinutes > 0) {
    idleTimer = setTimeout(() => turnDisplayOff(), Math.max(0, offMinutes * 60_000 - elapsed));
  }
}

const EXPLICIT_ACTIVITY_SOURCES = new Set(["mouseDown", "keyDown", "char", "pointer-down", "overlay-key"]);

function recordActivity(source = "local") {
  lastActivity = Date.now();
  if (displayIsOff || requestedDisplayStatus === "off") turnDisplayOn();
  if (mode === "slideshow" && EXPLICIT_ACTIVITY_SOURCES.has(source)) openMode("dashboard");
  scheduleIdle();
  sendState();
}

function openMode(nextMode, target = "") {
  if (nextMode !== "url") {
    clearTimeout(urlTimer);
    urlTimer = undefined;
  }
  clearTimeout(viewRetryTimer);
  viewRetryTimer = undefined;
  const url = nextMode === "dashboard" ? safeUrl(settings.dashboardUrl)
    : nextMode === "slideshow" ? safeUrl(settings.slideshowUrl)
      : nextMode === "url" ? safeUrl(target) : null;
  if (!url) {
    showNotice({ title: "View unavailable", message: nextMode === "slideshow" ? "Set an Immich Kiosk URL in settings." : "The configured URL is invalid." });
    return false;
  }
  mode = nextMode;
  if (mode === "slideshow") setKeyboardVisible(false);
  if (mode === "url") ensureOverlayView();
  resizeOverlay();
  if (nextMode === "url") {
    clearTimeout(urlTimer);
    urlTimer = setTimeout(() => {
      if (mode === "url") openMode("dashboard");
    }, 30_000);
  }
  if (nextMode === "dashboard" || nextMode === "slideshow") {
    const key = nextMode === "dashboard" ? "dashboard" : "immich";
    healthChecker.probe(key).catch(() => {});
  }
  loadView(url);
  scheduleIdle();
  sendState();
  return true;
}

// Reloads the view only when the target actually differs from what is already
// loaded. Home Assistant is heavy to boot, so avoid reloading it needlessly.
function loadView(url) {
  if (!view() || view().isDestroyed()) return;
  const current = view().getURL();
  if (current === url) return;
  view().loadURL(url).catch((error) => console.error("View load failed:", error.message));
}

// Retries the current view after a crash or a failed load. The retry waits for
// the service to answer again, so the display recovers on its own once Home
// Assistant or Immich comes back.
function scheduleViewRetry(delay = VIEW_RETRY_INTERVAL_MS) {
  clearTimeout(viewRetryTimer);
  viewRetryTimer = setTimeout(async () => {
    viewRetryTimer = undefined;
    if (!view() || view().isDestroyed()) return;
    const key = mode === "slideshow" ? "immich" : "dashboard";
    try {
      await healthChecker.probe(key);
    } catch {
      scheduleViewRetry(delay);
      return;
    }
    if (healthChecker.status(key) !== "online") {
      scheduleViewRetry(delay);
      return;
    }
    const url = mode === "slideshow" ? safeUrl(settings.slideshowUrl) : safeUrl(settings.dashboardUrl);
    if (!url) return;
    console.info("Retrying view after the service came back online.");
    view().loadURL(url).catch((error) => console.error("View retry failed:", error.message));
  }, delay);
}

function setSidePanel(open) {
  sidePanelOpen = Boolean(open);
  if (open) ensureOverlayView();
  resizeOverlay();
  sendState();
}

function resizeOverlay() {
  if (!win || win.isDestroyed() || !overlayView) return;
  const { width, height } = win.getContentBounds();
  const homeButtonVisible = mode === "url";
  const needsOverlay = overlayExpanded || sidePanelOpen || keyboardVisible || homeButtonVisible;
  if (!needsOverlay) {
    if (overlayAttached) {
      win.contentView.removeChildView(overlayView);
      overlayAttached = false;
    }
    scheduleOverlayRecycle();
    return;
  }
  clearTimeout(overlayRecycleTimer);
  overlayRecycleTimer = undefined;
  if (!overlayAttached) {
    win.contentView.addChildView(overlayView);
    overlayAttached = true;
  }
  if (overlayExpanded || sidePanelOpen) overlayView.setBounds({ x: 0, y: 0, width, height });
  else if (keyboardVisible) {
    const keyboardHeight = Math.min(300, height);
    overlayView.setBounds({ x: 0, y: height - keyboardHeight, width, height: keyboardHeight });
  } else overlayView.setBounds({ x: Math.max(0, width - 210), y: 0, width: Math.min(210, width), height: Math.min(58, height) });
}

// The overlay is a whole renderer process. Once nothing needs it for a while,
// close it so the memory is released on a small device, and rebuild it on the
// next use.
function scheduleOverlayRecycle() {
  if (overlayRecycleTimer) return;
  overlayRecycleTimer = setTimeout(() => {
    overlayRecycleTimer = undefined;
    if (overlayExpanded || sidePanelOpen || keyboardVisible || mode === "url") return;
    destroyOverlayView();
  }, OVERLAY_RECYCLE_DELAY_MS);
}

function ensureOverlayView() {
  // A pending recycle would close the overlay we are about to use.
  clearTimeout(overlayRecycleTimer);
  overlayRecycleTimer = undefined;
  if (overlayView) return;
  createOverlayView();
  resizeOverlay();
}

function setKeyboardVisible(visible) {
  const next = Boolean(visible) && settings.touchKeyboardEnabled && mode !== "slideshow";
  if (keyboardVisible === next) return;
  keyboardVisible = next;
  if (next) ensureOverlayView();
  resizeOverlay();
  sendState();
}

function sendKeyboardKey(key) {
  if (!view() || view().isDestroyed()) return;
  const namedKeys = { BACKSPACE: "Backspace", ENTER: "Enter", TAB: "Tab", SPACE: " ", SHIFT: "Shift", LEFT: "ArrowLeft", RIGHT: "ArrowRight" };
  const keyCode = namedKeys[key] || key;
  if (!(typeof key === "string" && (key.length === 1 || Object.hasOwn(namedKeys, key)))) return;
  keyboardKeySequence += 1;
  if (key === "SHIFT") return;
  view().sendInputEvent({ type: "keyDown", keyCode });
  if (key === "SPACE" || key.length === 1) view().sendInputEvent({ type: "char", keyCode: key === "SPACE" ? " " : key });
  view().sendInputEvent({ type: "keyUp", keyCode });
}

function runDisplayCommand(operation) {
  const requestId = ++displayCommandId;
  requestedDisplayStatus = operation;
  const candidates = operation === "off"
    ? [["wlopm", ["--off", "*"]], ["kscreen-doctor", ["--dpms", "off"]], ["xset", ["dpms", "force", "off"]]]
    : [["wlopm", ["--on", "*"]], ["kscreen-doctor", ["--dpms", "on"]], ["xset", ["dpms", "force", "on"]]];
  const tryNext = (index) => {
    if (index >= candidates.length) {
      if (requestId === displayCommandId) requestedDisplayStatus = null;
      showNotice({ title: "Display control unavailable", message: "No supported display power command succeeded." });
      return;
    }
    const [command, args] = candidates[index];
    execFile(command, args, { timeout: 5000 }, (error) => {
      if (error) return tryNext(index + 1);
      if (requestId !== displayCommandId) {
        if (operation === "off" && requestedDisplayStatus === "on") runDisplayCommand("on");
        return;
      }
      displayIsOff = operation === "off";
      sendState();
    });
  };
  tryNext(0);
}

function turnDisplayOff() {
  if (displayIsOff) return;
  runDisplayCommand("off");
}

function turnDisplayOn() {
  clearTimeout(offTimer);
  offTimer = undefined;
  if (!displayIsOff && requestedDisplayStatus !== "off") return;
  requestedDisplayStatus = "on";
  runDisplayCommand("on");
}

async function setBrightness(value) {
  const percent = Math.max(0, Math.min(100, Number(value)));
  if (!Number.isFinite(percent)) return;
  try {
    const devices = await fs.readdir("/sys/class/backlight");
    for (const device of devices) {
      const root = path.join("/sys/class/backlight", device);
      const maximum = Number(await fs.readFile(path.join(root, "max_brightness"), "utf8"));
      if (!maximum) continue;
      await fs.writeFile(path.join(root, "brightness"), String(Math.round(maximum * percent / 100)));
      if (mqttClient?.connected) mqttClient.publish(`${settings.mqttPrefix}/brightness`, String(percent), { qos: 1, retain: true });
      return;
    }
  } catch (error) {
    console.warn("Backlight control unavailable:", error.message);
  }
  showNotice({ title: "Brightness control unavailable", message: "No writable Linux backlight device was found." });
}

function handleCommand(command) {
  if (typeof command === "string") {
    try { command = JSON.parse(command); } catch { command = { action: command }; }
  }
  if (!command || typeof command !== "object" || typeof command.action !== "string") return;
  const action = command.action;
  switch (action) {
    case "wake": {
      const wasOff = displayIsOff || requestedDisplayStatus === "off";
      recordActivity("home-assistant");
      if (!wasOff) runDisplayCommand("on");
      break;
    }
    case "dashboard": recordActivity("home-assistant"); openMode("dashboard"); break;
    case "slideshow": recordActivity("home-assistant"); openMode("slideshow"); break;
    case "screen_off": cancelIdleTimers(); turnDisplayOff(); break;
    case "open_url":
    case "url":
      recordActivity("home-assistant");
      openMode("url", command.url || command.target || command.value || "");
      clearOpenUrlText();
      break;
    case "brightness": recordActivity("home-assistant"); setBrightness(command.value); break;
    case "notification": recordActivity("home-assistant"); showNotice(command); break;
    case "dismiss_notification": sendToOverlay("app:dismiss-notification"); break;
    case "set_mode":
      recordActivity("home-assistant");
      if (["dashboard", "slideshow"].includes(command.mode)) openMode(command.mode);
      break;
    case "display_mode":
      recordActivity("home-assistant");
      setDisplayMode(command.mode);
      break;
    case "exit":
      recordActivity("home-assistant");
      quitApp();
      break;
    default: console.warn("Ignored unknown MQTT action:", action);
  }
}

function clearOpenUrlText() {
  if (!mqttClient?.connected) return;
  // Keep a retained blank-equivalent value so Home Assistant restores this
  // text entity as empty after a reload instead of losing its state topic.
  mqttClient.publish(`${settings.mqttPrefix}/open_url/state`, " ", { qos: 1, retain: true });
}

function setServiceStatus(name, status) {
  if (serviceStatus[name] === status) return;
  serviceStatus[name] = status;
  sendState();
}

function connectMqtt() {
  if (!settings.mqttUrl) {
    setServiceStatus("mqtt", "offline");
    return;
  }
  try {
    // Loaded on demand so installs without MQTT do not parse the client.
    const mqtt = require("mqtt");
    mqttClient = mqtt.connect(settings.mqttUrl, {
      username: settings.mqttUsername || undefined,
      password: settings.mqttPassword || undefined,
      clientId: deviceId,
      reconnectPeriod: 10_000,
      connectTimeout: 10_000,
      clean: true,
      will: { topic: `${settings.mqttPrefix}/availability`, payload: "offline", retain: true, qos: 1 },
    });
    mqttClient.on("connect", () => {
      console.info("MQTT connected");
      mqttClient.publish(`${settings.mqttPrefix}/availability`, "online", { retain: true, qos: 1 });
      mqttClient.subscribe(`${settings.mqttPrefix}/command`, { qos: 1 });
      mqttClient.subscribe("homeassistant/status", { qos: 1 });
      publishDiscovery();
      healthChecker.probe("mqtt").catch(() => {});
      sendState();
    });
    mqttClient.on("message", (topic, payload) => {
      if (topic === "homeassistant/status") { if (payload.toString() === "online") publishDiscovery(); return; }
      handleCommand(payload.toString());
    });
    mqttClient.on("close", () => { healthChecker.probe("mqtt").catch(() => {}); });
    mqttClient.on("error", () => { healthChecker.probe("mqtt").catch(() => {}); });
  } catch (error) {
    setServiceStatus("mqtt", "offline");
    console.error("MQTT setup failed:", error.message);
  }
}

function publishDiscovery() {
  const device = {
    identifiers: [deviceId], name: settings.deviceName || "Home Panel",
    manufacturer: "Vithurshan Selvarajah", model: "Home Panel", sw_version: app.getVersion(),
  };
  const common = { availability_topic: `${settings.mqttPrefix}/availability`, device };
  const publishEntity = (component, objectId, config) => mqttClient.publish(
    `homeassistant/${component}/${deviceId}/${objectId}/config`, JSON.stringify({ ...common, ...config, unique_id: `${deviceId}_${objectId}` }), { qos: 1, retain: true });
  const removeEntity = (component, objectId) => mqttClient.publish(`homeassistant/${component}/${deviceId}/${objectId}/config`, "", { qos: 1, retain: true });
  const commandAvailable = (name) => String(process.env.PATH || "").split(path.delimiter).some((dir) => {
    try { fsSync.accessSync(path.join(dir, name), fsSync.constants.X_OK); return true; } catch { return false; }
  });
  const canControlDisplay = Boolean(process.env.WAYLAND_DISPLAY && ["wlopm", "kscreen-doctor"].some(commandAvailable))
    || Boolean(process.env.DISPLAY && commandAvailable("xset"));
  const hasBacklight = (() => {
    try { return fsSync.readdirSync("/sys/class/backlight").some((entry) => {
      const brightnessPath = path.join("/sys/class/backlight", entry, "brightness");
      const maxPath = path.join("/sys/class/backlight", entry, "max_brightness");
      fsSync.accessSync(brightnessPath, fsSync.constants.W_OK);
      return Number(fsSync.readFileSync(maxPath, "utf8")) > 0;
    }); } catch { return false; }
  })();
  const config = {
    name: "Mode", unique_id: `${deviceId}_mode`, state_topic: `${settings.mqttPrefix}/state`,
    value_template: "{{ value_json.mode }}", availability_topic: `${settings.mqttPrefix}/availability`,
    device, icon: "mdi:tablet-dashboard",
  };
  mqttClient.publish(`homeassistant/sensor/${deviceId}/mode/config`, JSON.stringify(config), { qos: 1, retain: true });
  if (settings.slideshowUrl) {
    publishEntity("select", "view", { name: "View", options: ["dashboard", "slideshow"], command_topic: `${settings.mqttPrefix}/command`, command_template: '{"action":"set_mode","mode":"{{ value }}"}', state_topic: `${settings.mqttPrefix}/state`, value_template: "{{ value_json.mode if value_json.mode in ['dashboard', 'slideshow'] else 'dashboard' }}", optimistic: false, qos: 1, icon: "mdi:monitor-dashboard" });
    publishEntity("button", "slideshow", { name: "Open slideshow", command_topic: `${settings.mqttPrefix}/command`, payload_press: '{"action":"slideshow"}', icon: "mdi:image-multiple" });
  } else { removeEntity("select", "view"); removeEntity("button", "slideshow"); }
  if (canControlDisplay) {
    publishEntity("switch", "screen", { name: "Screen", command_topic: `${settings.mqttPrefix}/command`, payload_on: '{"action":"wake"}', payload_off: '{"action":"screen_off"}', state_topic: `${settings.mqttPrefix}/state`, value_template: "{{ 'OFF' if value_json.displayIsOff else 'ON' }}", state_on: "ON", state_off: "OFF", icon: "mdi:monitor" });
    publishEntity("button", "wake", { name: "Wake screen", command_topic: `${settings.mqttPrefix}/command`, payload_press: '{"action":"wake"}', icon: "mdi:monitor" });
    publishEntity("button", "screen_off", { name: "Turn screen off", command_topic: `${settings.mqttPrefix}/command`, payload_press: '{"action":"screen_off"}', icon: "mdi:monitor-off" });
  } else { removeEntity("switch", "screen"); removeEntity("button", "wake"); removeEntity("button", "screen_off"); }
  if (hasBacklight) publishEntity("number", "brightness", { name: "Brightness", command_topic: `${settings.mqttPrefix}/command`, command_template: '{"action":"brightness","value":{{ value | int }}}', state_topic: `${settings.mqttPrefix}/brightness`, min: 0, max: 100, step: 1, mode: "slider", unit_of_measurement: "%", icon: "mdi:brightness-6" });
  else removeEntity("number", "brightness");
  removeEntity("switch", "keyboard");
  publishEntity("text", "notification", { name: "Send notification", command_topic: `${settings.mqttPrefix}/command`, command_template: '{{ {"action":"notification","message":value} | tojson }}', min: 1, max: 255, mode: "text", icon: "mdi:message-text" });
  publishEntity("text", "open_url", { name: "Open display URL", command_topic: `${settings.mqttPrefix}/command`, command_template: '{{ {"action":"open_url","url":value} | tojson }}', state_topic: `${settings.mqttPrefix}/open_url/state`, value_template: "{{ value | trim }}", optimistic: false, min: 8, max: 255, mode: "text", icon: "mdi:link-variant" });
  clearOpenUrlText();
  removeEntity("button", "exit_view"); removeEntity("button", "keyboard_toggle");
  publishEntity("button", "dashboard", { name: "Return to Home", command_topic: `${settings.mqttPrefix}/command`, payload_press: '{"action":"dashboard"}', icon: "mdi:home" });
  // The display mode is a select rather than a switch, because there are
  // two modes and neither is the absence of the other.
  publishEntity("select", "display_mode", { name: "Display mode", options: ["fullscreen", "window"], command_topic: `${settings.mqttPrefix}/command`, command_template: '{"action":"display_mode","mode":"{{ value }}"}', state_topic: `${settings.mqttPrefix}/state`, value_template: "{{ value_json.displayMode }}", optimistic: false, qos: 1, icon: "mdi:monitor" });
  publishEntity("button", "exit", { name: "Exit", command_topic: `${settings.mqttPrefix}/command`, payload_press: '{"action":"exit"}', icon: "mdi:power" });
  // The camera picker has been removed, so clear any entity it left behind.
  removeEntity("select", "camera");
  for (const name of legacyCameraNames) {
    removeEntity("button", `camera_${String(name).toLowerCase().replace(/[^a-z0-9]+/g, "_").replace(/^_|_$/g, "").slice(0, 48)}`);
  }
  legacyCameraNames = [];
}

async function loadSettings() {
  try {
    settings = await decodeSettings(JSON.parse(await fs.readFile(configPath(), "utf8")));
    return;
  } catch { /* fall through to the backup */ }
  try {
    settings = await decodeSettings(JSON.parse(await fs.readFile(`${configPath()}.bak`, "utf8")));
    console.warn("Recovered settings from the backup copy.");
  } catch { settings = { ...DEFAULTS }; }
}

async function saveSettings(next) {
  const clean = { ...settings, ...next };
  clean.configured = true;
  clean.touchKeyboardEnabled = Boolean(clean.touchKeyboardEnabled);
  if (next.mqttPassword === "••••••••" || next.mqttPassword === "") clean.mqttPassword = settings.mqttPassword;
  clean.dashboardUrl = safeUrl(clean.dashboardUrl) || DEFAULTS.dashboardUrl;
  clean.slideshowUrl = clean.slideshowUrl ? safeUrl(clean.slideshowUrl) || "" : "";
  clean.echomuseEnabled = Boolean(clean.echomuseEnabled);
  clean.echomuseControllerUrl = clean.echomuseControllerUrl ? safeUrl(clean.echomuseControllerUrl) || "" : "";
  clean.echomuseDeviceId = String(clean.echomuseDeviceId || "").trim();
  clean.echomuseBoard = String(clean.echomuseBoard || DEFAULTS.echomuseBoard).trim() || DEFAULTS.echomuseBoard;
  // The display mode is a setting, so a device that was set up before it
  // existed has none, and one with a nonsense value falls back to the
  // mode it was running in.
  clean.displayMode = DISPLAY_MODES.includes(clean.displayMode) ? clean.displayMode : displayMode();
  const persisted = { ...clean };
  if (clean.mqttPassword && await safeStorage.isAsyncEncryptionAvailable()) {
    persisted.mqttPasswordEncrypted = (await safeStorage.encryptStringAsync(clean.mqttPassword)).toString("base64");
    delete persisted.mqttPassword;
  }
  await fs.mkdir(path.dirname(configPath()), { recursive: true });
  await writeSettingsFile(persisted);
  const previousDisplayMode = displayMode();
  settings = { ...clean };
  // The mode is applied only when it changed, so saving the other settings
  // does not move the window the user is looking at.
  if (settings.displayMode !== previousDisplayMode) applyDisplayMode();
  if (!settings.touchKeyboardEnabled) setKeyboardVisible(false);
  registerHealthProbes();
  if (mqttClient) { mqttClient.end(true); mqttClient = null; }
  connectMqtt();
  openMode(mode === "slideshow" ? "slideshow" : "dashboard");
  startEchoMuse();
  // The EchoMuse settings are pushed to the controller as its configuration,
  // so the controller and the device agree on how the device listens.
  if (echomuseClient && settings.echomuseEnabled) sendEchoMuseConfig();
  return publicSettings();
}

// Sends the EchoMuse configuration to the controller.
//
// The controller owns the wake word and the audio settings, so the device
// tells it what it is and the controller pushes back what it wants.
//
// Only the settings that were actually chosen are sent. A setting that was
// never touched is left out, so the sidecar keeps what it had rather than
// being handed a default that was not the user's.
function sendEchoMuseConfig() {
  if (!echomuseClient) return;
  echomuseClient.sendConfig({
    board: settings.echomuseBoard,
    wakeThreshold: settingNumber(settings.echomuseWakeThreshold),
    wakeWordEnabled: settingBoolean(settings.echomuseWakeWordEnabled),
    bargeIn: settingBoolean(settings.echomuseBargeIn),
    startupVolume: settingNumber(settings.echomuseStartupVolume),
    micGainDb: settingNumber(settings.echomuseMicGainDb),
    vadThreshold: settingNumber(settings.echomuseVadThreshold),
    vadSpeechMs: settingNumber(settings.echomuseVadSpeechMs),
    vadSilenceMs: settingNumber(settings.echomuseVadSilenceMs),
    volume: settingNumber(settings.echomuseVolume),
    duckDb: settingNumber(settings.echomuseDuckDb),
    muteButton: settingBoolean(settings.echomuseMuteButton),
    volumeButtonSound: settingBoolean(settings.echomuseVolumeButtonSound),
    wakeMode: settings.echomuseWakeMode || "on",
    wakeSound: settings.echomuseWakeSound !== "off",
  });
}

// Writes the settings file atomically so a power cut cannot leave a half
// written config behind, and keeps the previous file as a backup.
async function writeSettingsFile(data) {
  const target = configPath();
  const temporary = `${target}.tmp`;
  const serialized = JSON.stringify(data, null, 2);
  try {
    await fs.rename(target, `${target}.bak`);
  } catch { /* no previous settings to back up */ }
  await fs.writeFile(temporary, serialized, { mode: 0o600 });
  await fs.rename(temporary, target);
}

function publicSettings() {
  return { ...settings, mqttPassword: settings.mqttPassword ? "••••••••" : "" };
}

// Starts the EchoMuse sidecar, if the device is set up to use one.
//
// The sidecar owns the controller link and the audio. It reports what the
// device is doing, and the overlay draws it.
function startEchoMuse() {
  if (echomuseClient) {
    echomuseClient.stop();
    echomuseClient = null;
  }
  if (!settings.echomuseEnabled) return;

  const binary = findSidecar(app.getAppPath(), process.resourcesPath);
  const stateFile = path.join(app.getPath("userData"), "echomuse-state.json");
  const configFile = path.join(app.getPath("userData"), "echomuse-config.json");
  const credentialsDir = path.join(app.getPath("userData"), "echomuse");

  echomuseClient = new EchoMuseClient({
    binary,
    stateFile,
    configFile,
    credentialsDir,
    deviceId: settings.echomuseDeviceId || deviceId,
    board: settings.echomuseBoard || "home-panel",
    controllerUrl: settings.echomuseControllerUrl || "",
    onState: () => sendEchoMuseState(),
    onLog: (message) => { if (message) console.info("echomuse:", message); },
  });

  try {
    fsSync.mkdirSync(credentialsDir, { recursive: true, mode: 0o700 });
  } catch (error) {
    console.warn("could not create the EchoMuse credential directory:", error.message);
  }

  echomuseClient.start();

  // The configuration is written before the sidecar starts, so the sidecar
  // reads the settings on its first pass rather than waiting for the next
  // change. Otherwise a device that was configured yesterday would run
  // today with the defaults.
  sendEchoMuseConfig();
}

function createOverlayView() {
  overlayView = new WebContentsView({ webPreferences: {
    // The overlay loads a bundled local page, so it is trusted enough to run
    // unsandboxed and share the field editing helper with the main process.
    preload: path.join(__dirname, "preload.js"), contextIsolation: true, nodeIntegration: false, sandbox: false,
  } });
  overlayView.setBackgroundColor("#00000000");
  overlayView.webContents.loadFile(path.join(__dirname, "ui", "index.html"));
  overlayView.webContents.on("did-finish-load", sendState);
}

function destroyOverlayView() {
  if (!overlayView) return;
  if (overlayAttached && win && !win.isDestroyed()) {
    win.contentView.removeChildView(overlayView);
  }
  overlayAttached = false;
  overlayView.webContents.close();
  overlayView = null;
  // Force the next sendState to reach the rebuilt overlay.
  lastSentState = "";
}

// The display modes the device can be in.
//
// A kiosk covers the screen and takes the keyboard, which is what a wall
// display wants and what a developer at a desk does not. The mode is a
// setting rather than a command line switch alone, so a device that was
// installed as a kiosk can be turned into a window without being reinstalled.

// Reads the display mode from the settings, falling back to fullscreen.
//
// A device that was set up before the setting existed has no value for it,
// and fullscreen is what it was running in.
function displayMode() {
  return readDisplayMode(settings.displayMode);
}

// Puts the window into the mode it is supposed to be in.
//
// The mode is applied to the whole screen in fullscreen and to the window
// itself in windowed mode. In windowed mode the window is sized to a
// sensible fraction of the screen rather than the whole of it, because a
// window that fills the screen is a fullscreen window with the title bar
// hidden.
function applyDisplayMode() {
  if (!win || win.isDestroyed()) return;
  const mode = displayMode();

  if (mode === "fullscreen") {
    if (!win.isFullScreen()) win.setFullScreen(true);
    return;
  }

  if (win.isFullScreen()) win.setFullScreen(false);
  const primary = screen.getPrimaryDisplay();
  win.setBounds(windowBounds(primary.workArea || primary.bounds));
  win.show();
}

// Switches the device between the modes.
//
// The window is resized rather than recreated, so the view keeps whatever it
// was showing and there is no reload.
function setDisplayMode(mode) {
  if (!DISPLAY_MODES.includes(mode)) return false;
  settings.displayMode = mode;
  applyDisplayMode();
  sendState();
  // The mode is a setting, so it is written straight away. A device that is
  // switched to a window and then loses power should come back in a window.
  writeSettingsFile({ ...settings }).catch((error) => console.warn("The display mode could not be saved:", error.message));
  return true;
}

function createWindow() {
  const primary = screen.getPrimaryDisplay();
  win = new BrowserWindow({
    x: primary.bounds.x, y: primary.bounds.y, width: primary.bounds.width, height: primary.bounds.height,
    autoHideMenuBar: true, backgroundColor: "#080d15",
    webPreferences: { preload: path.join(__dirname, "remote-preload.js"), contextIsolation: true, nodeIntegration: false, sandbox: true },
  });
  // The mode is applied after the window exists, because leaving
  // fullscreen needs a window that is already on screen to leave.
  applyDisplayMode();
  // Use the BrowserWindow's own webContents for the dashboard: avoid a second,
  // hidden blank renderer process just to host the window.
  createOverlayView();
  view().setWindowOpenHandler(() => ({ action: "deny" }));
  view().on("will-navigate", (event, url) => {
    if (!safeUrl(url)) event.preventDefault();
  });
  view().on("did-finish-load", () => {
    enableTouchEmulation(view());
    injectScrollbarCSS(view()).catch(() => {});
    sendState();
  });
  view().on("did-fail-load", (_event, errorCode, errorDescription, validatedURL, isMainFrame) => {
    if (!isMainFrame) return;
    // A failed load is usually the service being down rather than a bad URL,
    // so keep retrying quietly instead of showing an error the user cannot act on.
    console.warn(`View load failed (${errorCode}): ${errorDescription} ${validatedURL}`);
    scheduleViewRetry();
    sendState();
  });
  view().on("render-process-gone", (_event, details) => {
    // clean-exit means the page was closed on purpose, so only a genuine
    // crash or an out of memory kill is worth recovering from.
    if (details.reason === "clean-exit") return;
    console.error("View renderer gone:", details.reason);
    showNotice({
      title: "Display reloading",
      message: "The dashboard stopped responding and is being restarted.",
      duration: 6000,
    });
    scheduleViewRetry();
    sendState();
  });
  view().on("before-input-event", (_event, input) => {
    if (["mouseDown", "keyDown", "char"].includes(input.type)) recordActivity(input.type);
  });
  view().on("before-mouse-event", (_event, input) => {
    if (input.type === "mouseDown") {
      if (keyboardVisible) setKeyboardVisible(false);
      recordActivity("pointer-down");
    }
    if (input.type === "mouseMove") {
      // Throttle here so the idle timers are not reset on every mouse move.
      if (Date.now() - lastPointerMoveAt > POINTER_MOVE_INTERVAL_MS) {
        lastPointerMoveAt = Date.now();
        recordActivity("pointer-move");
      }
    }
  });
  const resize = () => resizeOverlay();
  win.webContents.setWindowOpenHandler(() => ({ action: "deny" }));
  win.webContents.on("will-navigate", (event, url) => {
    if (!safeUrl(url)) event.preventDefault();
  });
  win.on("resize", resize);
  win.on("enter-full-screen", resize);
  win.on("leave-full-screen", resize);
  win.on("closed", () => { cancelIdleTimers(); clearTimeout(urlTimer); clearTimeout(viewRetryTimer); });
  resize();
  view().loadURL(safeUrl(settings.dashboardUrl) || DEFAULTS.dashboardUrl);
  mode = "dashboard";
  scheduleIdle();
}

// Shuts the device down.
//
// The app quits through the normal path, so everything is cleaned up by the
// same code whichever way the app is closed — the exit button, the window
// manager, or a signal.
function quitApp() {
  console.info("Shutting down at the user's request.");
  app.quit();
}

function registerIpc() {
  ipcMain.handle("app:get-settings", (event) => { assertOverlaySender(event); return publicSettings(); });
  ipcMain.handle("app:save-settings", async (event, value) => { assertOverlaySender(event); return saveSettings(value); });
  ipcMain.handle("app:command", (event, command) => {
    assertOverlaySender(event);
    if (!command || typeof command.action !== "string") return false;
    handleCommand(command); return true;
  });
  ipcMain.handle("app:set-display-mode", (event, mode) => {
    assertOverlaySender(event);
    return setDisplayMode(mode);
  });
  ipcMain.handle("app:quit", (event) => {
    assertOverlaySender(event);
    quitApp();
    return true;
  });
  ipcMain.on("app:activity", (event, source) => { assertOverlaySender(event); recordActivity(source === "keydown" ? "overlay-key" : "overlay"); });
  ipcMain.on("app:overlay-expanded", (event, expanded) => {
    assertOverlaySender(event);
    overlayExpanded = Boolean(expanded);
    ensureOverlayView();
    resizeOverlay();
  });
  ipcMain.on("app:side-panel", (event, open) => { assertOverlaySender(event); setSidePanel(open); });
  ipcMain.on("app:keyboard", (event, visible) => { assertOverlaySender(event); setKeyboardVisible(visible); });
  ipcMain.on("app:keyboard-key", (event, key) => { assertOverlaySender(event); sendKeyboardKey(key); });
  ipcMain.on("remote:text-input", (event, focused) => {
    if (event.sender !== win?.webContents) return;
    clearTimeout(inputBlurTimer);
    if (focused) { sidePanelOpen = false; resizeOverlay(); setKeyboardVisible(true); }
    else {
      const keySequenceAtBlur = keyboardKeySequence;
      inputBlurTimer = setTimeout(() => {
        if (keyboardKeySequence === keySequenceAtBlur) setKeyboardVisible(false);
      }, 350);
    }
  });
  ipcMain.on("remote:open-side-panel", (event) => {
    if (event.sender === win?.webContents && !sidePanelOpen) setSidePanel(true);
  });
}

const hasLock = app.requestSingleInstanceLock();
if (!hasLock) app.quit();

// The command line overrides the saved display mode for this run.
//
// The mode is a setting so a device can be changed without being reinstalled,
// but a flag is useful when the settings file is not reachable or when the
// device is being run by hand. The setting is not written, so the device goes
// back to what it was configured to do when it is started again.
//
//   --window       run in a window
//   --fullscreen   run fullscreen
//
// Electron consumes its own flags, so only the ones the app owns are read.
function readDisplayModeFlag() {
  const args = process.argv.slice(1);
  if (args.includes("--window")) return "window";
  if (args.includes("--fullscreen")) return "fullscreen";
  return null;
}

// Trim Chromium features a kiosk never uses. Hardware acceleration is kept
// because the target device has a GPU.
app.commandLine.appendSwitch("disable-features", "Translate,MediaRouter,OptimizationHints,CalculateNativeWinOcclusion,InterestFeedContentSuggestions,BackForwardCache,AcceptCHFrame,AutofillServerCommunication,CertificateTransparencyComponentUpdater,DialMediaRouteProvider,GlobalMediaControls,PrivacySandboxSettings4,DiskCacheBackendInMemory");
// Keep Chromium's disk caches out of the eMMC: they are rewritten on every
// page load and are the main source of write wear on the target device.
app.commandLine.appendSwitch("disk-cache-size", "0");
app.commandLine.appendSwitch("media-cache-size", "0");
app.commandLine.appendSwitch("disable-gpu-shader-disk-cache");
app.commandLine.appendSwitch("password-store", "basic");
app.commandLine.appendSwitch("disable-background-networking");
app.commandLine.appendSwitch("disable-component-update");
app.commandLine.appendSwitch("disable-domain-reliability");
app.commandLine.appendSwitch("disable-sync");
app.commandLine.appendSwitch("no-pings");
app.commandLine.appendSwitch("no-default-browser-check");

app.whenReady().then(async () => {
  if (!hasLock) return;
  await loadSettings();
  // The flag wins over the saved setting, but only for this run.
  const flagMode = readDisplayModeFlag();
  if (flagMode) settings.displayMode = flagMode;
  registerHealthProbes();
  healthChecker.start();
  registerIpc();
  createWindow();
  connectMqtt();
  startEchoMuse();
  app.on("activate", () => { if (!BrowserWindow.getAllWindows().length) createWindow(); });
  app.on("second-instance", () => {
    if (!win || win.isDestroyed()) return;
    if (win.isMinimized()) win.restore();
    win.focus();
  });
});

app.on("before-quit", () => {
  healthChecker.stop();
  cancelIdleTimers(); clearTimeout(urlTimer); clearTimeout(viewRetryTimer); clearTimeout(notificationTimer);
  // The sidecar holds the microphone and the speaker, so it is stopped
  // before the process that owns it goes away. Otherwise the system waits
  // for it to time out, and the audio devices stay held open.
  if (echomuseClient) {
    echomuseClient.stop();
    echomuseClient = null;
  }
  if (mqttClient) {
    mqttClient.publish(`${settings.mqttPrefix}/availability`, "offline", { retain: true, qos: 1 });
    mqttClient.end();
  }
});
app.on("window-all-closed", () => { if (process.platform !== "darwin") app.quit(); });
