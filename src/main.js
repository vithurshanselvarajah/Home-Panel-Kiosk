const { app, BrowserWindow, WebContentsView, ipcMain, screen, safeStorage } = require("electron");
const fs = require("node:fs/promises");
const fsSync = require("node:fs");
const path = require("node:path");
const { execFile } = require("node:child_process");
const os = require("node:os");
const mqtt = require("mqtt");
const { safeUrl, cameraObjectId } = require("./utils");
const { createHealthChecker, probeHttp } = require("./health");

const DEFAULTS = {
  configured: false,
  dashboardUrl: "http://homeassistant.local:8123",
  slideshowUrl: "",
  idleToSlideshowMinutes: 5,
  slideshowToOffMinutes: 15,
  startInKiosk: true,
  touchKeyboardEnabled: true,
  mqttUrl: "",
  mqttUsername: "",
  mqttPassword: "",
  mqttPrefix: "homepanel",
  deviceName: "Home Panel",
};

let win;
let remoteView;
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
let cameraTimer;
let overlayAttached = false;
let overlayExpanded = false;
let sidePanelOpen = false;
let keyboardVisible = false;
let inputBlurTimer;
let keyboardKeySequence = 0;
const serviceStatus = { mqtt: "unknown", immich: "unknown", dashboard: "unknown" };
let lastPublishedMode;
let lastPublishedPower;
let lastPublishedServiceStatus = "";
let lastPublishedAt = 0;
let displayCommandId = 0;
let requestedDisplayStatus = null;
let cameraSelection = "Select camera";
let lastPublishedCameraSelection;
let homeAssistantCameras = [];
let legacyCameraNames = [];
let cameraRefreshTimer;
let wasFullscreenBeforeCamera = null;
const deviceId = `homepanel_${os.hostname().toLowerCase().replace(/[^a-z0-9]/g, "").slice(0, 20) || "display"}`;
const configPath = () => path.join(app.getPath("userData"), "settings.json");

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
  await probeHttp(new URL("/api/", settings.dashboardUrl).toString(), { timeoutMs });
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

function enableTouchEmulation(webContents) {
  if (!webContents || webContents.isDestroyed()) return;
  try {
    if (!webContents.debugger.isAttached()) webContents.debugger.attach("1.3");
    webContents.debugger.sendCommand("Emulation.setEmitTouchEventsForMouse", {
      configuration: "mobile",
      enabled: true,
    }).catch(() => {});
  } catch (error) {
    console.warn("Touch emulation unavailable:", error.message);
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

function decodeSettings(data) {
  if (data.mqttPasswordEncrypted && safeStorage.isEncryptionAvailable()) {
    try { data.mqttPassword = safeStorage.decryptString(Buffer.from(data.mqttPasswordEncrypted, "base64")); } catch {}
  }
  legacyCameraNames = Array.isArray(data.cameras) ? data.cameras.map((camera) => camera?.name).filter(Boolean) : [];
  delete data.cameras;
  delete data.mqttPasswordEncrypted;
  return { ...DEFAULTS, ...data };
}

function assertOverlaySender(event) {
  if (event.sender !== overlayView?.webContents) throw new Error("Untrusted IPC sender");
}

function sendState() {
  const state = { mode, cameraSelection, displayIsOff, keyboardVisible, sidePanelOpen, serviceStatus: { ...serviceStatus }, lastActivity: new Date(lastActivity).toISOString() };
  if (!overlayView?.webContents.isDestroyed()) overlayView.webContents.send("app:state", state);
  publishState(state);
}

function publishState(state) {
  if (!mqttClient?.connected) return;
  const currentServiceStatus = JSON.stringify(state.serviceStatus);
  const stateChanged = state.mode !== lastPublishedMode || state.cameraSelection !== lastPublishedCameraSelection || state.displayIsOff !== lastPublishedPower || currentServiceStatus !== lastPublishedServiceStatus;
  if (!stateChanged && Date.now() - lastPublishedAt < 30_000) return;
  lastPublishedMode = state.mode;
  lastPublishedPower = state.displayIsOff;
  lastPublishedCameraSelection = state.cameraSelection;
  lastPublishedServiceStatus = currentServiceStatus;
  lastPublishedAt = Date.now();
  mqttClient.publish(`${settings.mqttPrefix}/state`, JSON.stringify(state), { qos: 1, retain: true });
}

function showNotice({ title = "Home Panel", message = "", duration = 8000, priority = "normal" } = {}) {
  const safeDuration = Math.max(1000, Math.min(Number(duration) || 8000, 120000));
  overlayView?.webContents.send("app:notification", {
    title: String(title).slice(0, 100),
    message: String(message).slice(0, 500),
    duration: safeDuration,
    priority: ["low", "normal", "high"].includes(priority) ? priority : "normal",
  });
  clearTimeout(notificationTimer);
  notificationTimer = setTimeout(() => overlayView?.webContents.send("app:dismiss-notification"), safeDuration);
}

function cancelIdleTimers() {
  clearTimeout(idleTimer);
  clearTimeout(offTimer);
  idleTimer = undefined;
  offTimer = undefined;
}

function scheduleIdle() {
  cancelIdleTimers();
  if (mode === "camera") return;
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
  clearTimeout(cameraTimer);
  cameraTimer = undefined;
  const url = nextMode === "dashboard" ? safeUrl(settings.dashboardUrl)
    : nextMode === "slideshow" ? safeUrl(settings.slideshowUrl)
      : ["camera", "url"].includes(nextMode) ? safeUrl(target) : null;
  if (!url) {
    showNotice({ title: "View unavailable", message: nextMode === "slideshow" ? "Set an Immich Kiosk URL in settings." : "The configured URL is invalid." });
    return false;
  }
  if (nextMode === "camera" && mode !== "camera" && win && !win.isDestroyed()) {
    wasFullscreenBeforeCamera = win.isFullScreen();
    if (!wasFullscreenBeforeCamera) win.setFullScreen(true);
  } else if (mode === "camera" && nextMode !== "camera" && wasFullscreenBeforeCamera !== null) {
    if (win && !win.isDestroyed() && !wasFullscreenBeforeCamera) win.setFullScreen(false);
    wasFullscreenBeforeCamera = null;
  }
  if (mode === "camera" && nextMode !== "camera") cameraSelection = "Select camera";
  mode = nextMode;
  if (mode === "slideshow") setKeyboardVisible(false);
  resizeOverlay();
  if (nextMode === "url") {
    clearTimeout(urlTimer);
    urlTimer = setTimeout(() => {
      if (mode === "url") openMode("dashboard");
    }, 30_000);
  }
  if (nextMode === "camera") {
    cameraTimer = setTimeout(() => {
      if (mode === "camera") openMode("dashboard");
    }, 30_000);
  }
  if (nextMode === "dashboard" || nextMode === "slideshow") {
    const key = nextMode === "dashboard" ? "dashboard" : "immich";
    healthChecker.probe(key).catch(() => {});
  }
  remoteView.webContents.loadURL(url).catch((error) => console.error("View load failed:", error.message));
  if (nextMode === "camera") {
    cancelIdleTimers();
  } else {
    scheduleIdle();
  }
  sendState();
  return true;
}

function setSidePanel(open) {
  sidePanelOpen = Boolean(open);
  resizeOverlay();
  sendState();
}

function resizeOverlay() {
  if (!win || win.isDestroyed() || !overlayView) return;
  const { width, height } = win.getContentBounds();
  const homeButtonVisible = ["url", "camera"].includes(mode);
  const needsOverlay = overlayExpanded || sidePanelOpen || keyboardVisible || homeButtonVisible;
  if (!needsOverlay) {
    if (overlayAttached) {
      win.contentView.removeChildView(overlayView);
      overlayAttached = false;
    }
    return;
  }
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

function setKeyboardVisible(visible) {
  const next = Boolean(visible) && settings.touchKeyboardEnabled && mode !== "slideshow";
  if (keyboardVisible === next) return;
  keyboardVisible = next;
  resizeOverlay();
  sendState();
}

function sendKeyboardKey(key) {
  if (!remoteView?.webContents || remoteView.webContents.isDestroyed()) return;
  const namedKeys = { BACKSPACE: "Backspace", ENTER: "Enter", TAB: "Tab", SPACE: " ", SHIFT: "Shift", LEFT: "ArrowLeft", RIGHT: "ArrowRight" };
  const keyCode = namedKeys[key] || key;
  if (!(typeof key === "string" && (key.length === 1 || Object.hasOwn(namedKeys, key)))) return;
  keyboardKeySequence += 1;
  if (key === "SHIFT") return;
  remoteView.webContents.sendInputEvent({ type: "keyDown", keyCode });
  if (key === "SPACE" || key.length === 1) remoteView.webContents.sendInputEvent({ type: "char", keyCode: key === "SPACE" ? " " : key });
  remoteView.webContents.sendInputEvent({ type: "keyUp", keyCode });
}

function expandCameraDialog(attempt = 0) {
  if (mode !== "camera" || !remoteView?.webContents || remoteView.webContents.isDestroyed()) return;
  remoteView.webContents.executeJavaScript(`(() => {
    const findDeep = (root, selector) => {
      const direct = root.querySelector?.(selector);
      if (direct) return direct;
      for (const element of root.querySelectorAll?.("*") || []) {
        if (element.shadowRoot) {
          const found = findDeep(element.shadowRoot, selector);
          if (found) return found;
        }
      }
      return null;
    };
    const moreInfo = findDeep(document, "ha-more-info-dialog");
    const dialog = moreInfo && findDeep(moreInfo.shadowRoot, "ha-dialog");
    if (!dialog) return false;
    dialog.dispatchEvent(new CustomEvent("dialog-set-fullscreen", { detail: true, bubbles: true, composed: true }));
    return true;
  })()`, true).then((expanded) => {
    if (!expanded && attempt < 40 && mode === "camera") setTimeout(() => expandCameraDialog(attempt + 1), 250);
  }).catch((error) => console.warn("Could not expand camera stream:", error.message));
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

function openCamera(name) {
  const camera = homeAssistantCameras.find((item) => item.option === name || item.entityId === name);
  if (!camera) {
    showNotice({ title: "Camera unavailable", message: "No matching Home Assistant camera entity is available." });
    return;
  }
  cameraSelection = camera.option;
  const cameraUrl = new URL(settings.dashboardUrl);
  cameraUrl.searchParams.set("more-info-entity-id", camera.entityId);
  openMode("camera", cameraUrl.toString());
}

async function refreshHomeAssistantCameras() {
  if (!remoteView?.webContents || !["dashboard", "camera"].includes(mode)) return true;
  const dashboard = safeUrl(settings.dashboardUrl);
  const current = safeUrl(remoteView.webContents.getURL());
  if (!dashboard || !current || new URL(dashboard).origin !== new URL(current).origin) return true;
  try {
    const result = await remoteView.webContents.executeJavaScript(`(() => {
      const hass = document.querySelector("home-assistant")?.hass;
      if (!hass?.states || !hass.connection?.connected) return null;
      return Object.entries(hass.states)
        .filter(([entityId]) => entityId.startsWith("camera."))
        .map(([entityId, state]) => ({ entityId, name: state.attributes?.friendly_name || entityId }));
    })()`, true);
    if (!Array.isArray(result)) return false;
    const entities = result;
    const counts = new Map();
    for (const entity of entities) counts.set(entity.name, (counts.get(entity.name) || 0) + 1);
    const next = entities.map(({ entityId, name }) => ({
      entityId,
      option: counts.get(name) > 1 ? `${name} (${entityId})` : name,
    })).sort((left, right) => left.option.localeCompare(right.option));
    if (JSON.stringify(next) === JSON.stringify(homeAssistantCameras)) return true;
    homeAssistantCameras = next;
    if (!homeAssistantCameras.some((camera) => camera.option === cameraSelection)) cameraSelection = "Select camera";
    if (mqttClient?.connected) publishDiscovery();
    sendState();
    return true;
  } catch (error) {
    console.warn("Could not read Home Assistant cameras:", error.message);
    return false;
  }
}

function scheduleCameraRefresh(delay = 1200) {
  clearTimeout(cameraRefreshTimer);
  cameraRefreshTimer = setTimeout(async () => {
    const ready = await refreshHomeAssistantCameras();
    scheduleCameraRefresh(ready ? 60_000 : 3_000);
  }, delay);
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
    case "camera": {
      if (command.name === "Select camera") break;
      recordActivity("home-assistant");
      openCamera(command.name);
      break;
    }
    case "notification": recordActivity("home-assistant"); showNotice(command); break;
    case "dismiss_notification": overlayView?.webContents.send("app:dismiss-notification"); break;
    case "set_mode":
      recordActivity("home-assistant");
      if (["dashboard", "slideshow"].includes(command.mode)) openMode(command.mode);
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
    manufacturer: "Home Panel", model: "Electron kiosk", sw_version: app.getVersion(),
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
  if (homeAssistantCameras.length) {
    publishEntity("select", "camera", { name: "Camera", options: ["Select camera", ...homeAssistantCameras.map((camera) => camera.option)], command_topic: `${settings.mqttPrefix}/command`, command_template: '{{ {"action":"camera","name":value} | tojson }}', state_topic: `${settings.mqttPrefix}/state`, value_template: "{{ value_json.cameraSelection }}", optimistic: false, qos: 1, icon: "mdi:video" });
  } else removeEntity("select", "camera");
  for (const name of legacyCameraNames) {
    removeEntity("button", cameraObjectId(name));
  }
  legacyCameraNames = [];
}

async function loadSettings() {
  try { settings = decodeSettings(JSON.parse(await fs.readFile(configPath(), "utf8"))); }
  catch { settings = { ...DEFAULTS }; }
}

async function saveSettings(next) {
  const clean = { ...settings, ...next };
  clean.configured = true;
  clean.touchKeyboardEnabled = Boolean(clean.touchKeyboardEnabled);
  if (next.mqttPassword === "••••••••" || next.mqttPassword === "") clean.mqttPassword = settings.mqttPassword;
  clean.dashboardUrl = safeUrl(clean.dashboardUrl) || DEFAULTS.dashboardUrl;
  clean.slideshowUrl = clean.slideshowUrl ? safeUrl(clean.slideshowUrl) || "" : "";
  const persisted = { ...clean };
  if (safeStorage.isEncryptionAvailable() && clean.mqttPassword) {
    persisted.mqttPasswordEncrypted = safeStorage.encryptString(clean.mqttPassword).toString("base64");
    delete persisted.mqttPassword;
  }
  await fs.mkdir(path.dirname(configPath()), { recursive: true });
  await fs.writeFile(configPath(), JSON.stringify(persisted, null, 2), { mode: 0o600 });
  settings = { ...clean };
  if (!settings.touchKeyboardEnabled) setKeyboardVisible(false);
  registerHealthProbes();
  if (mqttClient) { mqttClient.end(true); mqttClient = null; }
  connectMqtt();
  openMode(mode === "slideshow" ? "slideshow" : "dashboard");
  return publicSettings();
}

function publicSettings() {
  return { ...settings, mqttPassword: settings.mqttPassword ? "••••••••" : "" };
}

function createWindow() {
  const primary = screen.getPrimaryDisplay();
  win = new BrowserWindow({
    x: primary.bounds.x, y: primary.bounds.y, width: primary.bounds.width, height: primary.bounds.height,
    fullscreen: settings.startInKiosk, autoHideMenuBar: true, backgroundColor: "#080d15",
    webPreferences: { preload: path.join(__dirname, "remote-preload.js"), contextIsolation: true, nodeIntegration: false, sandbox: true },
  });
  // Use the BrowserWindow's own webContents for the dashboard: avoid a second,
  // hidden blank renderer process just to host the window.
  remoteView = { webContents: win.webContents };
  overlayView = new WebContentsView({ webPreferences: {
    preload: path.join(__dirname, "preload.js"), contextIsolation: true, nodeIntegration: false, sandbox: true,
  } });
  overlayView.setBackgroundColor("#00000000");
  overlayView.webContents.loadFile(path.join(__dirname, "ui", "index.html"));
  remoteView.webContents.setWindowOpenHandler(() => ({ action: "deny" }));
  remoteView.webContents.on("will-navigate", (event, url) => {
    if (!safeUrl(url)) event.preventDefault();
  });
  remoteView.webContents.on("did-finish-load", () => {
    enableTouchEmulation(remoteView.webContents);
    injectScrollbarCSS(remoteView.webContents).catch(() => {});
    if (mode === "camera") setTimeout(() => expandCameraDialog(), 250);
    sendState();
    scheduleCameraRefresh();
  });
  remoteView.webContents.on("did-fail-load", () => {
    sendState();
  });
  remoteView.webContents.on("before-input-event", (_event, input) => {
    if (["mouseDown", "keyDown", "char"].includes(input.type)) recordActivity(input.type);
  });
  remoteView.webContents.on("before-mouse-event", (_event, input) => {
    if (input.type === "mouseDown") {
      if (keyboardVisible) setKeyboardVisible(false);
      recordActivity("pointer-down");
    }
    if (input.type === "mouseMove") {
      if (Date.now() - lastActivity > 5000) recordActivity("pointer-move");
    }
  });
  overlayView.webContents.on("did-finish-load", sendState);
  const resize = () => resizeOverlay();
  win.webContents.setWindowOpenHandler(() => ({ action: "deny" }));
  win.webContents.on("will-navigate", (event, url) => {
    if (!safeUrl(url)) event.preventDefault();
  });
  win.on("resize", resize);
  win.on("enter-full-screen", resize);
  win.on("leave-full-screen", resize);
  win.on("closed", () => { cancelIdleTimers(); clearTimeout(cameraRefreshTimer); clearTimeout(urlTimer); clearTimeout(cameraTimer); });
  resize();
  remoteView.webContents.loadURL(safeUrl(settings.dashboardUrl) || DEFAULTS.dashboardUrl);
  mode = "dashboard";
  scheduleIdle();
}

function registerIpc() {
  ipcMain.handle("app:get-settings", (event) => { assertOverlaySender(event); return publicSettings(); });
  ipcMain.handle("app:save-settings", async (event, value) => { assertOverlaySender(event); return saveSettings(value); });
  ipcMain.handle("app:command", (event, command) => {
    assertOverlaySender(event);
    if (!command || typeof command.action !== "string") return false;
    handleCommand(command); return true;
  });
  ipcMain.on("app:activity", (event, source) => { assertOverlaySender(event); recordActivity(source === "keydown" ? "overlay-key" : "overlay"); });
  ipcMain.on("app:overlay-expanded", (event, expanded) => {
    assertOverlaySender(event);
    overlayExpanded = Boolean(expanded);
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

app.whenReady().then(async () => {
  if (!hasLock) return;
  await loadSettings();
  registerHealthProbes();
  healthChecker.start();
  registerIpc();
  createWindow();
  connectMqtt();
  app.on("activate", () => { if (!BrowserWindow.getAllWindows().length) createWindow(); });
  app.on("second-instance", () => {
    if (!win || win.isDestroyed()) return;
    if (win.isMinimized()) win.restore();
    win.focus();
  });
});

app.on("before-quit", () => {
  healthChecker.stop();
  cancelIdleTimers(); clearTimeout(cameraRefreshTimer); clearTimeout(urlTimer); clearTimeout(cameraTimer); clearTimeout(notificationTimer);
  if (mqttClient) {
    mqttClient.publish(`${settings.mqttPrefix}/availability`, "offline", { retain: true, qos: 1 });
    mqttClient.end();
  }
});
app.on("window-all-closed", () => { if (process.platform !== "darwin") app.quit(); });
