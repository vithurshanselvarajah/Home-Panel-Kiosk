const { app, BrowserWindow, WebContentsView, ipcMain, screen, safeStorage, session, clipboard } = require("electron");
const fs = require("node:fs/promises");
const fsSync = require("node:fs");
const path = require("node:path");
const { execFile } = require("node:child_process");
const os = require("node:os");
const { safeUrl, settingBoolean, displayMode: readDisplayMode, windowBounds, hostIdentity, DISPLAY_MODES } = require("./utils");
const { generateEncryptionKey } = require("./encryption");
const QRCode = require("qrcode");
const { createHealthChecker, probeHttp } = require("./health");
const { DeviceClient, findSidecar } = require("./device");
const { ControlClient, findControlSocket } = require("./control");
const { moveCachesToMemory } = require("./caches");

const HOSTNAME = hostIdentity(os.hostname());

if (process.env.WAYLAND_DISPLAY && !process.argv.includes("--ozone-platform=wayland")) {
  process.argv.push("--ozone-platform=wayland");
}

const DEFAULTS = {
  configured: false,
  dashboardUrl: "http://homeassistant.local:8123",
  slideshowUrl: "",
  idleToSlideshowMinutes: 5,
  slideshowToOffMinutes: 15,
  displayMode: "fullscreen",
  touchKeyboardEnabled: true,
  deviceName: HOSTNAME,
  esphomeEncryptionEnabled: false,
  esphomeEncryptionKey: "",
  esphomeDeviceName: "",

  voiceAudioInput: "",

  speakerTuning: true,
  speakerGainDb: 0,
  speakerStereo: false,
  speakerBassDb: 0,
  speakerTrebleDb: 0,
  clearerSpeech: false,
  louderTop: false,
  micCancel: true,
  micLeveling: true,
  micDenoise: false,
  micSensitivityDb: 8,
  voiceAudioOutput: "",

  musicEnabled: true,
  musicName: HOSTNAME,
  musicUnpaired: false,
  cameraEnabled: false,
  cameraPassword: "",
};

let voiceMonitorMic = false;
let voiceTestSound = 0;

let win;
let overlayView;
let settings = { ...DEFAULTS };
let controlClient;
let mode = "dashboard";
let lastActivity = Date.now();
let idleTimer;
let offTimer;
let displayIsOff = false;
let notificationTimer;
let urlTimer;
let overlayAttached = false;
let overlayExpanded = false;

let overlayPopup = false;
let overlayMute = false;

function overlayNeeded() {
  return overlayPopup || overlayMute;
}

let voiceOverlayFull = false;

let sidePanelOpen = false;

let tabView;
let tabAttached = false;
let keyboardVisible = false;
let inputBlurTimer;
let keyboardKeySequence = 0;

const serviceStatus = { control: "unknown", immich: "unknown", dashboard: "unknown" };
let lastSentState = "";
let displayCommandId = 0;
let requestedDisplayStatus = null;
let legacyCameraNames = [];
let overlayRecycleTimer;
let viewRetryTimer;
let lastPointerMoveAt = 0;
let deviceClient;

let quitting = false;
const OVERLAY_RECYCLE_DELAY_MS = 60_000;
const VIEW_RETRY_INTERVAL_MS = 15_000;
const POINTER_MOVE_INTERVAL_MS = 5_000;
const deviceId = HOSTNAME;
const configPath = () => path.join(app.getPath("userData"), "settings.json");

const view = () => win?.webContents;

const healthChecker = createHealthChecker({
  onChange: (name, status) => {
    if (serviceStatus[name] === status) return;
    serviceStatus[name] = status;
    sendState();
  },
});

function probeControl() {
  if (!controlClient?.connected) throw new Error("disconnected");
}

async function probeHomeAssistant({ timeoutMs } = {}) {
  if (!settings.dashboardUrl) throw new Error("not configured");

  await probeHttp(new URL("/manifest.json", settings.dashboardUrl).toString(), { timeoutMs });
}

async function probeImmich({ timeoutMs } = {}) {
  if (!settings.slideshowUrl) throw new Error("not configured");
  await probeHttp(settings.slideshowUrl, { timeoutMs });
}

function registerHealthProbes() {
  healthChecker.setProbes({
    device: probeControl,
    dashboard: probeHomeAssistant,
    immich: probeImmich,
  });
}

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
    } catch {  }
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
  if (data.esphomeEncryptionKeyEncrypted && await safeStorage.isAsyncEncryptionAvailable()) {
    try {
      const { result } = await safeStorage.decryptStringAsync(Buffer.from(data.esphomeEncryptionKeyEncrypted, "base64"));
      data.esphomeEncryptionKey = result;
    } catch (error) {
      console.warn("The saved encryption key could not be read:", error.message);
      delete data.esphomeEncryptionKey;
    }
  }
  if (data.cameraPasswordEncrypted && await safeStorage.isAsyncEncryptionAvailable()) {
    try {
      const { result } = await safeStorage.decryptStringAsync(Buffer.from(data.cameraPasswordEncrypted, "base64"));
      data.cameraPassword = result;
    } catch (error) {
      console.warn("The saved camera password could not be read.", error.message);
      delete data.cameraPassword;
    }
  }
  delete data.esphomeEncryptionKeyEncrypted;
  delete data.cameraPasswordEncrypted;
  delete data.cameras;
  delete data.voiceEnabled;
  delete data.voiceDeviceId;
  delete data.voiceVolume;
  delete data.voiceDuckDb;
  delete data.voiceMuteButton;
  return {
    ...DEFAULTS,
    ...data,

    deviceName: HOSTNAME,
    esphomeDeviceName: HOSTNAME,
    musicName: HOSTNAME,
  };
}

function assertOverlaySender(event) {
  if (event.sender !== overlayView?.webContents) throw new Error("Untrusted IPC sender");
}

function assertTabSender(event) {
  if (event.sender !== tabView?.webContents) throw new Error("Untrusted IPC sender");
}

function sendToOverlay(channel, payload) {
  if (!overlayView?.webContents || overlayView.webContents.isDestroyed()) return;
  try {
    overlayView.webContents.send(channel, payload);
  } catch {  }
}

function sendState(force = false) {
  const state = { mode, displayIsOff, keyboardVisible, sidePanelOpen, displayMode: displayMode(), brightness: currentBrightness(), volume: currentVolume(), muted: currentMute(), version: app.getVersion(), serviceStatus: { ...serviceStatus }, lastActivity: new Date(lastActivity).toISOString() };
  const fingerprint = `${state.mode}|${state.displayIsOff}|${state.keyboardVisible}|${state.sidePanelOpen}|${state.displayMode}|${state.brightness}|${state.volume}|${JSON.stringify(state.serviceStatus)}`;
  if (force || fingerprint !== lastSentState) {
    lastSentState = fingerprint;
    sendToOverlay("app:state", state);
  }
  publishState(state);
}

function sendDeviceState() {
  if (!deviceClient) return;
  const state = deviceClient.state;
  sendToOverlay("app:device", state);
  watchTheOverlay(state);
}

const OVERLAY_WATCHDOG_MS = 400;

let overlayWatchdogTimer = null;

function watchTheOverlay(state) {
  if (!overlayWanted(state)) {
    clearTimeout(overlayWatchdogTimer);
    overlayWatchdogTimer = null;
    return;
  }

  if (voiceOverlayFull) {
    clearTimeout(overlayWatchdogTimer);
    overlayWatchdogTimer = null;
    return;
  }
  if (overlayWatchdogTimer) return;
  overlayWatchdogTimer = setTimeout(() => {
    overlayWatchdogTimer = null;

    if (overlayWanted(deviceClient?.state)) sendDeviceState();
  }, OVERLAY_WATCHDOG_MS);
}

function overlayWanted(state) {
  if (!state || state.muted) return false;
  return Boolean(state.speaking || state.thinking || state.listening);
}

function publishState(state) {
  if (!controlClient?.connected) return;

  const payload = {
    mode: state.mode,
    displayIsOff: state.displayIsOff,
    keyboardVisible: state.keyboardVisible,
    sidePanelOpen: state.sidePanelOpen,
    displayMode: state.displayMode,
    brightness: state.brightness,
    lastActivity: state.lastActivity,
    version: state.version,
    idleToSlideshowMinutes: Math.max(0, Number(settings.idleToSlideshowMinutes) || 0),
    slideshowToOffMinutes: Math.max(0, Number(settings.slideshowToOffMinutes) || 0),
    touchKeyboardEnabled: Boolean(settings.touchKeyboardEnabled),
    musicEnabled: Boolean(settings.musicEnabled),
    musicUnpaired: Boolean(settings.musicUnpaired),
  };
  if (Number.isFinite(state.volume)) payload.volume = state.volume;
  controlClient.sendState(payload);
}

function sendTransport(command) {
  if (!command || typeof command.action !== "string") return false;
  if (!controlClient?.connected) return false;
  const payload = { action: command.action };

  if (command.source === "home" || command.source === "server") {
    payload.source = command.source;
  }
  if (Number.isFinite(Number(command.positionMs))) {
    payload.positionMs = Math.max(0, Math.round(Number(command.positionMs)));
  }
  return controlClient.sendTransport(payload);
}

function showNotice({ title = "Home Panel", message = "", duration = 8000, priority = "normal" } = {}) {
  const safeDuration = Math.max(1000, Math.min(Number(duration) || 8000, 120000));
  wakeScreen();
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

function openMode(nextMode, target = "", urlDuration) {
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

    const seconds = Number(urlDuration) > 0 ? Math.round(Number(urlDuration) / 1000) : 30;
    urlTimer = setTimeout(() => {
      if (mode === "url") openMode("dashboard");
    }, seconds * 1000);
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

function loadView(url) {
  if (!view() || view().isDestroyed()) return;
  const current = view().getURL();
  if (current === url) return;
  view().loadURL(url).catch((error) => console.error("View load failed:", error.message));
}

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

  const next = Boolean(open);
  if (sidePanelOpen === next) return;
  sidePanelOpen = next;
  if (open) ensureOverlayView();
  resizeOverlay();

  resizeTabView();
  sendTabState();
  sendState();
}

function resizeOverlay() {
  if (!win || win.isDestroyed() || !overlayView) return;
  const { width, height } = win.getContentBounds();
  const homeButtonVisible = mode === "url";

  const needsOverlay = overlayExpanded || sidePanelOpen || keyboardVisible || homeButtonVisible || voiceOverlayFull || overlayNeeded() || displayIsOff;

  if (needsOverlay && !overlayView) ensureOverlayView();
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
  if (overlayExpanded || sidePanelOpen || voiceOverlayFull || overlayNeeded() || displayIsOff) {

    overlayView.setBounds({ x: 0, y: 0, width, height });
  } else if (keyboardVisible) {
    const keyboardHeight = Math.min(300, height);
    overlayView.setBounds({ x: 0, y: height - keyboardHeight, width, height: keyboardHeight });
  } else {

    const stripWidth = Math.min(210, width);
    overlayView.setBounds({ x: Math.max(0, width - stripWidth), y: 0, width: stripWidth, height: Math.min(58, height) });
  }
}

function setVoiceOverlayFull(needed) {
  const next = Boolean(needed);
  if (voiceOverlayFull === next) return;
  voiceOverlayFull = next;
  if (next) ensureOverlayView();
  resizeOverlay();

  resizeTabView();
}

function scheduleOverlayRecycle() {
  if (overlayRecycleTimer) return;
  overlayRecycleTimer = setTimeout(() => {
    overlayRecycleTimer = undefined;
    if (overlayExpanded || sidePanelOpen || keyboardVisible || mode === "url" || voiceOverlayFull || overlayNeeded()) return;
    destroyOverlayView();
  }, OVERLAY_RECYCLE_DELAY_MS);
}

function ensureOverlayView() {

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

  resizeTabView();
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

let brightnessBeforeOff = null;

function runDisplayCommand(operation) {
  const requestId = ++displayCommandId;
  requestedDisplayStatus = operation;
  const candidates = operation === "off"
    ? [["wlopm", ["--off", "*"]], ["kscreen-doctor", ["--dpms", "off"]], ["xset", ["dpms", "force", "off"]]]
    : [["wlopm", ["--on", "*"]], ["kscreen-doctor", ["--dpms", "on"]], ["xset", ["dpms", "force", "on"]]];
  const tryNext = (index) => {
    if (index >= candidates.length) {

      if (requestId === displayCommandId) {
        requestedDisplayStatus = null;
        setBacklightOff(operation === "off");
      }
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

const BACKLIGHT_FLOOR = 1;

const RELIGHT_LEVEL = 1000;
const RELIGHT_HOLD_MS = 10;

let backlightIsOff = true;

function backlightIsPowered() {

  const level = backlightRawLevel();
  if (level !== null) return level > 0;
  return !backlightIsOff;
}

let backlightQueue = Promise.resolve();

function queueBacklight(action) {
  const done = backlightQueue.then(action, action);
  backlightQueue = done.then(() => {}, () => {});
  return done;
}

async function writeBacklightLevel(level) {
  try {
    const devices = await fs.readdir("/sys/class/backlight");
    for (const device of devices) {
      const root = path.join("/sys/class/backlight", device);
      const maximum = Number(await fs.readFile(path.join(root, "max_brightness"), "utf8"));
      if (!maximum) continue;
      await fs.writeFile(path.join(root, "brightness"), String(Math.max(0, Math.min(maximum, level))));
      return true;
    }
  } catch (error) {
    console.warn("Backlight control unavailable:", error.message);
  }
  return false;
}

async function setAutomaticBrightness(level) {
  const wanted = Math.max(0, Math.min(1023, Math.round(Number(level))));
  if (!Number.isFinite(wanted)) return;

  if (!backlightIsPowered()) return;

  if (relighting) return;

  const current = backlightRawLevel();
  if (current === null || current === 0) return;
  const next = Math.round(current + (wanted - current) * AUTOMATIC_BRIGHTNESS_SMOOTH);

  const settled = Math.abs(wanted - next) < 0.5 ? wanted : next;
  if (settled === current) return;
  await writeBacklightLevel(settled);
}

const AUTOMATIC_BRIGHTNESS_SMOOTH = 0.25;

async function writeBacklight(percent) {
  const maximum = await backlightMaximum();
  if (!maximum) return false;
  const level = Math.round(maximum * Math.max(0, Math.min(100, percent)) / 100);
  if (!backlightIsPowered()) {
    await relight(level);
    return true;
  }
  const written = await writeBacklightLevel(Math.max(BACKLIGHT_FLOOR, level));
  if (written) backlightIsOff = false;
  return written;
}

async function backlightMaximum() {
  try {
    const devices = await fs.readdir("/sys/class/backlight");
    for (const device of devices) {
      const maximum = Number(await fs.readFile(path.join("/sys/class/backlight", device, "max_brightness"), "utf8"));
      if (maximum) return maximum;
    }
  } catch (error) {
    console.warn("Backlight control unavailable:", error.message);
  }
  return 0;
}

let relighting = false;

async function relight(level) {
  relighting = true;
  try {
    await writeBacklightLevel(RELIGHT_LEVEL);
    await new Promise((resolve) => setTimeout(resolve, RELIGHT_HOLD_MS));
    await writeBacklightLevel(Math.max(BACKLIGHT_FLOOR, level));
  } finally {
    relighting = false;
  }
  backlightIsOff = false;
}

async function setBacklightOff(off) {
  if (off) {
    const level = currentBrightness();
    if (level === null || level === 0) {

      displayIsOff = true;
      sendState();
      return;
    }
    brightnessBeforeOff = level;
    await queueBacklight(async () => {
      await writeBacklightLevel(0);
      backlightIsOff = true;
    });
    displayIsOff = true;

    ensureOverlayView();
    resizeOverlay();
    sendState();
    return;
  }
  const level = brightnessBeforeOff;
  brightnessBeforeOff = null;

  const wanted = level === null || level <= 0 ? 60 : level;
  await queueBacklight(async () => {
    await relight(Math.round((await backlightMaximum()) * wanted / 100));
  });
  displayIsOff = false;

  resizeOverlay();
  sendState();
}

function turnDisplayOff() {
  if (displayIsOff) return;
  runDisplayCommand("off");
}

function wakeScreen() {
  const off = displayIsOff || requestedDisplayStatus === "off" || !backlightIsPowered();
  if (!off) return;
  turnDisplayOn();
}

function wakeOnTouch() {
  wakeScreen();
}

function turnDisplayOn() {
  clearTimeout(offTimer);
  offTimer = undefined;
  const off = displayIsOff || requestedDisplayStatus === "off" || !backlightIsPowered();
  if (!off) return;
  requestedDisplayStatus = "on";
  runDisplayCommand("on");
}

const screenWatchInterval = 400;

function watchScreen() {

  if (relighting) return;
  const powered = backlightIsPowered();
  const off = !powered;
  if (off === displayIsOff) return;
  displayIsOff = off;
  if (off) {

    brightnessBeforeOff = currentBrightness();

    ensureOverlayView();
    resizeOverlay();
  } else {
    brightnessBeforeOff = null;
  }
  sendState(true);
}

function backlightLevel() {
  try {
    for (const device of fsSync.readdirSync("/sys/class/backlight")) {
      const root = path.join("/sys/class/backlight", device);
      const maximum = Number(fsSync.readFileSync(path.join(root, "max_brightness"), "utf8"));
      const value = Number(fsSync.readFileSync(path.join(root, "brightness"), "utf8"));
      if (maximum && Number.isFinite(value)) return Math.round((value / maximum) * 100);
    }
  } catch {  }
  return null;
}

function backlightRawLevel() {
  try {
    for (const device of fsSync.readdirSync("/sys/class/backlight")) {
      const root = path.join("/sys/class/backlight", device);
      const value = Number(fsSync.readFileSync(path.join(root, "brightness"), "utf8"));
      if (Number.isFinite(value)) return value;
    }
  } catch {  }
  return null;
}

function currentBrightness() {
  const percent = backlightLevel();
  if (percent === null) return null;
  if (percent === 0 && brightnessBeforeOff !== null) return brightnessBeforeOff;
  return percent;
}

let systemVolume = null;

let micMuted = null;

function currentVolume() {
  return systemVolume;
}

function currentMute() {
  return micMuted;
}

async function refreshVolume() {

  return withMuteLock(async () => {
    const muted = deviceClient?.state?.muted;
    if (typeof muted !== "boolean") return false;
    micMuted = muted;

    sendState(true);
    return true;
  });
}

let volumeWatcher = null;

function watchVolume() {
  stopWatchingVolume();
  volumeWatcher = setInterval(watchTick, volumeWatchInterval);

  if (typeof volumeWatcher.unref === "function") volumeWatcher.unref();
}

let screenWatcher = null;

function watchScreenState() {
  stopWatchingScreen();
  screenWatcher = setInterval(watchScreen, screenWatchInterval);

  if (typeof screenWatcher.unref === "function") screenWatcher.unref();
}

function stopWatchingScreen() {
  if (screenWatcher !== null) {
    clearInterval(screenWatcher);
    screenWatcher = null;
  }
}

async function watchTick() {

    const muted = deviceClient?.state?.muted;
    if (typeof muted !== "boolean") return;
    if (muted === micMuted) return;
    micMuted = muted;
    sendState(true);
}

function stopWatchingVolume() {

  if (volumeWatcher !== null) {
    clearInterval(volumeWatcher);
    volumeWatcher = null;
  }
}

const volumeWatchInterval = 1000;

const BRIGHTNESS_FLOOR_PERCENT = 1;

async function setBrightness(value) {
  const percent = Math.max(BRIGHTNESS_FLOOR_PERCENT, Math.min(100, Math.round(Number(value))));
  if (!Number.isFinite(percent)) return;

  if (backlightLevel() === percent) return;
  if (displayIsOff) {

    brightnessBeforeOff = percent;
    sendState();
    return;
  }
  const written = await queueBacklight(() => writeBacklight(percent));
  if (!written) {
    showNotice({ title: "Brightness control unavailable", message: "No writable Linux backlight device was found." });
    return;
  }

  sendState();
}

async function setVolume(value) {
  const percent = Math.max(0, Math.min(100, Number(value)));
  if (!Number.isFinite(percent)) return false;
  return withMuteLock(async () => {
    if (!await sendDeviceVolume(percent)) return false;

    systemVolume = percent;
    sendState(true);
    return true;
  });
}

function followDeviceVolume(percent) {
  const value = Math.max(0, Math.min(100, Math.round(Number(percent))));
  if (!Number.isFinite(value)) return;
  if (value === systemVolume) return;
  systemVolume = value;

  showVolumePopup(value);
  sendState(true);
}

function deviceConfig() {
  return {
    sendspinEnabled: settingBoolean(settings.musicEnabled),
    sendspinName: settings.musicName || "",
    sendspinUnpaired: settingBoolean(settings.musicUnpaired),
    cameraEnabled: settingBoolean(settings.cameraEnabled),
    audioInput: settings.voiceAudioInput || "",
    audioOutput: settings.voiceAudioOutput || "",
    monitorMic: voiceMonitorMic,
    testSound: voiceTestSound,

    speakerTuning: settingBoolean(settings.speakerTuning),
    speakerGainDb: Number(settings.speakerGainDb) || 0,
    speakerStereo: settingBoolean(settings.speakerStereo),
    speakerBassDb: Number(settings.speakerBassDb) || 0,
    speakerTrebleDb: Number(settings.speakerTrebleDb) || 0,
    clearerSpeech: settingBoolean(settings.clearerSpeech),
    louderTop: settingBoolean(settings.louderTop),
    micCancel: settingBoolean(settings.micCancel),
    micLeveling: settingBoolean(settings.micLeveling),
    micDenoise: settingBoolean(settings.micDenoise),
    micSensitivityDb: Number(settings.micSensitivityDb) || 8,
  };
}

let volumePopupTimer = null;

function showVolumePopup(percent) {
  ensureOverlayView();
  overlayPopup = true;
  resizeOverlay();
  sendToOverlay("app:volume-popup", { percent });
  clearTimeout(volumePopupTimer);
  volumePopupTimer = setTimeout(hideVolumePopup, 2200);
}

function hideVolumePopup() {
  clearTimeout(volumePopupTimer);
  volumePopupTimer = null;
  overlayPopup = false;
  resizeOverlay();
  sendToOverlay("app:volume-popup", null);
}

async function sendDeviceVolume(percent) {
  if (!controlClient?.connected) return false;
  return controlClient.sendVolume(percent);
}

function followDeviceTurn() {
  const state = deviceClient?.state;
  if (!state) return;

  const busy = Boolean(state.listening || state.thinking || state.speaking);
  if (!busy) return;

  wakeScreen();
}

let muteLock = null;

function followDeviceMute() {
  const muted = deviceClient?.state?.muted;
  if (typeof muted !== "boolean") return;
  if (muted === micMuted) return;
  micMuted = muted;

  showMuteIndicator(muted);
  sendState(true);
}

function showMuteIndicator(muted) {
  const on = Boolean(muted);
  overlayMute = on;
  if (on) ensureOverlayView();
  resizeOverlay();
  sendToOverlay("app:mute-indicator", on);
}

function withMuteLock(work) {
  const next = (muteLock || Promise.resolve()).then(work, work);

  muteLock = next.catch(() => {});
  return next;
}

async function handleCommand(command) {
  if (typeof command === "string") {
    try { command = JSON.parse(command); } catch { command = { action: command }; }
  }
  if (!command || typeof command !== "object" || typeof command.action !== "string") return;
  const action = command.action;
  switch (action) {
    case "wake": {
      wakeScreen();
      break;
    }
    case "dashboard": recordActivity("home-assistant"); openMode("dashboard"); break;
    case "slideshow": recordActivity("home-assistant"); openMode("slideshow"); break;
    case "screen_off": cancelIdleTimers(); turnDisplayOff(); break;
    case "open_url":
    case "url":
      recordActivity("home-assistant");
      openMode("url", command.url || command.target || command.value || "", command.duration);
      break;
    case "brightness": recordActivity("home-assistant"); setBrightness(command.value); break;

    case "auto_brightness_level": setAutomaticBrightness(command.value); break;

    case "volume":
      recordActivity("home-assistant");

      if (typeof command.volume === "number") {
        followDeviceVolume(command.volume);
        break;
      }
      await setVolume(command.value);
      break;
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
    case "setting": {
      const setting = {
        idle_slideshow_minutes: "idleToSlideshowMinutes",
        slideshow_off_minutes: "slideshowToOffMinutes",
        touch_keyboard_enabled: "touchKeyboardEnabled",
        music_enabled: "musicEnabled",
        music_unpaired: "musicUnpaired",
        camera_enabled: "cameraEnabled",
      }[command.setting];
      if (!setting) {
        console.warn("Ignored unknown Home Assistant setting:", command.setting);
        break;
      }
      const value = ["touch_keyboard_enabled", "music_enabled", "music_unpaired", "camera_enabled"].includes(command.setting)
        ? Boolean(command.value)
        : Math.max(0, Math.min(1440, Math.round(Number(command.value) || 0)));
      saveSettings({ [setting]: value }).catch((error) => console.error("Home Assistant setting could not be saved:", error.message));
      break;
    }
    case "exit":
      recordActivity("home-assistant");
      quitApp();
      break;
    default: console.warn("Ignored unknown command:", action);
  }
}

function setServiceStatus(name, status) {
  if (serviceStatus[name] === status) return;
  serviceStatus[name] = status;
  sendState();
}

function startControlPlane() {

  const socket = findControlSocket(runtimeDirectory(deviceId));
  controlClient = new ControlClient({
    socket,
    onCommand: (command) => handleCommand(command),

    onStatus: () => {},

    onConnect: () => {
      sendState(true);
      controlClient.sendCameraPassword(settings.cameraPassword || "");
    },

    onLink: (connected) => {
      setServiceStatus("control", connected ? "online" : "offline");
    },
    onLog: (message) => console.info(`control: ${message}`),
  });
  controlClient.start();
}

async function loadSettings() {
  try {
    settings = await decodeSettings(JSON.parse(await fs.readFile(configPath(), "utf8")));
    return;
  } catch {  }
  try {
    settings = await decodeSettings(JSON.parse(await fs.readFile(`${configPath()}.bak`, "utf8")));
    console.warn("Recovered settings from the backup copy.");
  } catch { settings = { ...DEFAULTS }; }
}

function clampNumber(value, min, max, fallback) {
  const n = Number(value);
  if (!Number.isFinite(n)) return fallback;
  return Math.min(max, Math.max(min, n));
}

async function saveSettings(next) {
  const clean = { ...settings, ...next };
  clean.deviceName = HOSTNAME;
  clean.esphomeDeviceName = HOSTNAME;
  clean.musicName = HOSTNAME;
  clean.configured = true;
  clean.touchKeyboardEnabled = Boolean(clean.touchKeyboardEnabled);
  clean.dashboardUrl = safeUrl(clean.dashboardUrl) || DEFAULTS.dashboardUrl;
  clean.slideshowUrl = clean.slideshowUrl ? safeUrl(clean.slideshowUrl) || "" : "";

  clean.musicEnabled = Boolean(clean.musicEnabled);
  clean.musicUnpaired = Boolean(clean.musicUnpaired);
  clean.cameraEnabled = Boolean(clean.cameraEnabled);

  clean.voiceAudioInput = String(clean.voiceAudioInput || "").trim();
  clean.voiceAudioOutput = String(clean.voiceAudioOutput || "").trim();

  clean.speakerTuning = Boolean(clean.speakerTuning);
  clean.speakerStereo = Boolean(clean.speakerStereo);
  clean.clearerSpeech = Boolean(clean.clearerSpeech);
  clean.louderTop = Boolean(clean.louderTop);
  clean.micCancel = Boolean(clean.micCancel);
  clean.micLeveling = Boolean(clean.micLeveling);
  clean.micDenoise = Boolean(clean.micDenoise);
  clean.speakerGainDb = clampNumber(clean.speakerGainDb, 0, 6, 0);
  clean.speakerBassDb = clampNumber(clean.speakerBassDb, -6, 6, 0);
  clean.speakerTrebleDb = clampNumber(clean.speakerTrebleDb, -6, 6, 0);
  clean.micSensitivityDb = clampNumber(clean.micSensitivityDb, 0, 30, 8);

  clean.esphomeEncryptionEnabled = Boolean(clean.esphomeEncryptionEnabled);
  clean.esphomeEncryptionKey = String(clean.esphomeEncryptionKey || "").trim();
  clean.cameraPassword = String(clean.cameraPassword || "");
  if (clean.cameraPassword && (clean.cameraPassword.length < 8 || clean.cameraPassword.length > 128)) {
    throw new Error("The camera password must contain 8 to 128 characters.");
  }
  if (!clean.esphomeEncryptionEnabled) {

    clean.esphomeEncryptionKey = "";
  } else if (!clean.esphomeEncryptionKey) {

    clean.esphomeEncryptionKey = generateEncryptionKey();
  }

  clean.displayMode = DISPLAY_MODES.includes(clean.displayMode) ? clean.displayMode : displayMode();
  const persisted = { ...clean };

  delete persisted.esphomeEncryptionKeyEncrypted;
  delete persisted.esphomeEncryptionKey;
  delete persisted.cameraPasswordEncrypted;
  delete persisted.cameraPassword;
  if (clean.esphomeEncryptionKey && await safeStorage.isAsyncEncryptionAvailable()) {
    persisted.esphomeEncryptionKeyEncrypted = (await safeStorage.encryptStringAsync(clean.esphomeEncryptionKey)).toString("base64");
  }
  if (clean.cameraPassword) {
    if (!await safeStorage.isAsyncEncryptionAvailable()) {
      throw new Error("Secure password storage is unavailable on this system.");
    }
    persisted.cameraPasswordEncrypted = (await safeStorage.encryptStringAsync(clean.cameraPassword)).toString("base64");
  }
  await fs.mkdir(path.dirname(configPath()), { recursive: true });
  await writeSettingsFile(persisted);
  const previous = settings;
  const previousDisplayMode = displayMode();
  settings = { ...clean };
  if (previous.cameraPassword !== settings.cameraPassword) controlClient?.sendCameraPassword(settings.cameraPassword);

  if (settings.displayMode !== previousDisplayMode) applyDisplayMode();
  if (!settings.touchKeyboardEnabled) setKeyboardVisible(false);
  const urlsChanged = previous.dashboardUrl !== settings.dashboardUrl || previous.slideshowUrl !== settings.slideshowUrl;
  const timersChanged = previous.idleToSlideshowMinutes !== settings.idleToSlideshowMinutes || previous.slideshowToOffMinutes !== settings.slideshowToOffMinutes;
  if (urlsChanged) {
    registerHealthProbes();
    if (mode === "dashboard" || mode === "slideshow") openMode(mode);
  } else if (timersChanged) {
    scheduleIdle();
  }
  const restartDevice = ["voiceAudioInput", "voiceAudioOutput", "esphomeEncryptionEnabled", "esphomeEncryptionKey"]
    .some((key) => previous[key] !== settings[key]);
  const deviceConfigChanged = ["musicEnabled", "musicUnpaired", "cameraEnabled",
    "speakerTuning", "speakerGainDb", "speakerStereo", "speakerBassDb",
    "speakerTrebleDb", "clearerSpeech", "louderTop", "micCancel", "micLeveling",
    "micDenoise", "micSensitivityDb"]
    .some((key) => previous[key] !== settings[key]);
  if (restartDevice) await startDeviceProgram();
  else if (deviceConfigChanged) sendDeviceConfig();
  sendState(true);
  return publicSettings();
}

function sendDeviceConfig() {
  if (!deviceClient) return;

  deviceClient.sendConfig(deviceConfig());
}

async function writeSettingsFile(data) {
  const target = configPath();
  const temporary = `${target}.tmp`;
  const serialized = JSON.stringify(data, null, 2);
  try {
    await fs.rename(target, `${target}.bak`);
  } catch {  }
  await fs.writeFile(temporary, serialized, { mode: 0o600 });
  await fs.rename(temporary, target);
}

function publicSettings() {
  const { cameraPassword, ...visible } = settings;
  return { ...visible, cameraPasswordConfigured: Boolean(cameraPassword) };
}

function runtimeDirectory(identity) {
  const name = identity.replace(/[^a-zA-Z0-9_-]/g, "-") || "device";
  for (const base of [runtimeBase(), "/dev/shm/homepanel"]) {
    const dir = path.join(base, name);
    try {
      fsSync.mkdirSync(dir, { recursive: true, mode: 0o700 });

      if (!isMemory(dir)) continue;
      return dir;
    } catch {  }
  }
  return path.join(app.getPath("userData"), "device-runtime");
}

function runtimeBase() {
  return process.env.RUNTIME_DIRECTORY || "/dev/shm/homepanel";
}

function isMemory(directory) {
  try {
    const stats = fsSync.statfsSync(directory);

    return stats.type === 0x01021994 || stats.type === 0x858458f6;
  } catch {
    return false;
  }
}

async function startDeviceProgram() {
  if (deviceClient) {

    await deviceClient.stop();
    deviceClient = null;
  }

  const binary = findSidecar(app.getAppPath(), process.resourcesPath, process.execPath);

  const identity = deviceId;
  const runtimeDir = runtimeDirectory(identity);

  const runtimeBaseDir = path.dirname(runtimeDir);
  const stateFile = path.join(runtimeDir, "state.json");
  const levelFile = path.join(runtimeDir, "level");
  const configFile = path.join(app.getPath("userData"), "device-config.json");

  const settingsFile = path.join(app.getPath("userData"), "device-settings.json");

  const dataDirectory = path.join(app.getPath("userData"), "device");
  deviceClient = new DeviceClient({
    binary,
    stateFile,
    levelFile,
    configFile,
    settingsFile,
    dataDirectory,
    deviceId: identity,
    board: HOSTNAME,
    runtimeDirectory: runtimeBaseDir,

    mic: settings.voiceAudioInput || "",
    speaker: settings.voiceAudioOutput || "",

    controlSocket: path.join(runtimeDir, "control.sock"),

    esphomeKey: settings.esphomeEncryptionKey || "",
    esphomeName: HOSTNAME,
    onState: () => {
      sendDeviceState();

      followDeviceMute();

      followDeviceTurn();
    },
    onLog: (message) => { if (message) console.info("device:", message); },
  });

  deviceClient.start();

  sendDeviceConfig();
}

function createOverlayView() {
  overlayView = new WebContentsView({ webPreferences: {

    preload: path.join(__dirname, "preload.js"), contextIsolation: true, nodeIntegration: false, sandbox: false,
  } });
  overlayView.setBackgroundColor("#00000000");
  const webContents = overlayView.webContents;
  webContents.loadFile(path.join(__dirname, "ui", "index.html"));

  webContents.on("did-finish-load", () => {
    enableTouchEmulation(webContents);

    lastSentState = "";
    sendState();

    sendDeviceState();
  });
  webContents.on("render-process-gone", (_event, details) => {
    if (details.reason === "clean-exit" || webContents.isDestroyed()) return;
    console.error("Panel overlay renderer stopped:", details.reason);
    setTimeout(() => {
      if (overlayView?.webContents !== webContents || webContents.isDestroyed()) return;
      webContents.reload();
    }, 1000).unref?.();
  });

  webContents.on("before-mouse-event", (_event, input) => {
    if (input.type === "mouseDown") wakeOnTouch();
  });
}

function destroyOverlayView() {
  if (!overlayView) return;
  if (overlayAttached && win && !win.isDestroyed()) {
    win.contentView.removeChildView(overlayView);
  }
  overlayAttached = false;
  overlayView.webContents.close();
  overlayView = null;

  lastSentState = "";
}

function createTabView() {
  tabView = new WebContentsView({ webPreferences: {
    preload: path.join(__dirname, "tab-preload.js"), contextIsolation: true, nodeIntegration: false, sandbox: false,
  } });
  tabView.setBackgroundColor("#00000000");
  tabView.webContents.loadFile(path.join(__dirname, "ui", "tab.html"));
  tabView.webContents.on("did-finish-load", () => {
    enableTouchEmulation(tabView.webContents);
    sendTabState();
  });
  tabView.webContents.on("render-process-gone", (_event, details) => {
    if (details.reason === "clean-exit" || tabView?.webContents.isDestroyed()) return;
    console.error("Panel tab renderer stopped:", details.reason);
    setTimeout(() => {
      if (tabView?.webContents.isDestroyed()) return;
      tabView.webContents.reload();
    }, 1000).unref?.();
  });

  tabView.webContents.on("before-mouse-event", (_event, input) => {
    if (input.type === "mouseDown") wakeOnTouch();
  });
  resizeTabView();
}

function resizeTabView() {
  if (!win || win.isDestroyed() || !tabView) return;
  const { width, height } = win.getContentBounds();
  const tabWidth = Math.min(56, width);
  const tabHeight = Math.min(150, Math.max(90, Math.round(height * 0.22)));

  const wanted = !sidePanelOpen && !voiceOverlayFull && !overlayExpanded && !keyboardVisible;
  if (!wanted) {
    if (tabAttached) {
      win.contentView.removeChildView(tabView);
      tabAttached = false;
    }
    return;
  }
  if (!tabAttached) {
    win.contentView.addChildView(tabView);
    tabAttached = true;
  }

  tabView.setBounds({
    x: Math.max(0, width - tabWidth),
    y: Math.round((height - tabHeight) / 2),
    width: tabWidth,
    height: tabHeight,
  });
}

function sendTabState() {
  if (!tabView || tabView.webContents.isDestroyed()) return;
  try {
    tabView.webContents.send("tab:drawer-state", sidePanelOpen);
  } catch {  }
}

function displayMode() {
  return readDisplayMode(settings.displayMode);
}

function applyDisplayMode() {
  if (!win || win.isDestroyed()) return;
  const mode = displayMode();

  if (mode === "fullscreen") {
    if (!win.isFullScreen()) win.setFullScreen(true);

    win.show();
    return;
  }

  if (win.isFullScreen()) win.setFullScreen(false);
  const primary = screen.getPrimaryDisplay();
  win.setBounds(windowBounds(primary.workArea || primary.bounds));
  win.show();
}

function setDisplayMode(mode) {
  if (!DISPLAY_MODES.includes(mode)) return false;
  settings.displayMode = mode;
  applyDisplayMode();
  sendState();

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

  applyDisplayMode();

  createOverlayView();

  createTabView();
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

    console.warn(`View load failed (${errorCode}): ${errorDescription} ${validatedURL}`);
    scheduleViewRetry();
    sendState();
  });
  view().on("render-process-gone", (_event, details) => {

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
    if (input.type === "mouseDown") wakeOnTouch();
  });
  view().on("before-mouse-event", (_event, input) => {
    if (input.type === "mouseDown") {
      if (keyboardVisible) setKeyboardVisible(false);
      recordActivity("pointer-down");
    }
    if (input.type === "mouseMove") {

      if (Date.now() - lastPointerMoveAt > POINTER_MOVE_INTERVAL_MS) {
        lastPointerMoveAt = Date.now();
        recordActivity("pointer-move");
      }
    }
  });
  const resize = () => { resizeOverlay(); resizeTabView(); };
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

function quitApp() {
  console.info("Shutting down at the user's request.");
  app.quit();
}

function registerIpc() {
  ipcMain.handle("app:get-settings", (event) => { assertOverlaySender(event); return publicSettings(); });
  ipcMain.handle("app:save-settings", async (event, value) => { assertOverlaySender(event); return saveSettings(value); });
  ipcMain.handle("app:new-encryption-key", (event) => { assertOverlaySender(event); return generateEncryptionKey(); });
  ipcMain.handle("app:get-sendspin-token", (event) => {
    assertOverlaySender(event);
    if (!controlClient) throw new Error("The device program is not connected.");
    return controlClient.requestSendspinToken();
  });
  ipcMain.handle("app:key-qr", (event, value) => {
    assertOverlaySender(event);
    if (typeof value !== "string" || !value.trim() || value.length > 512) throw new Error("This key cannot be encoded as a QR code.");
    return QRCode.toDataURL(value.trim(), { errorCorrectionLevel: "M", margin: 2, width: 320 });
  });
  ipcMain.handle("app:copy-secret", (event, value) => {
    assertOverlaySender(event);
    if (typeof value !== "string" || value.length > 512) return false;
    clipboard.writeText(value);
    return true;
  });
  ipcMain.handle("app:command", (event, command) => {
    assertOverlaySender(event);
    if (!command || typeof command.action !== "string") return false;
    handleCommand(command); return true;
  });
  ipcMain.handle("app:set-display-mode", (event, mode) => {
    assertOverlaySender(event);
    return setDisplayMode(mode);
  });
  ipcMain.on("app:voice-overlay", (event, full) => {
    assertOverlaySender(event);
    setVoiceOverlayFull(Boolean(full));
  });
  ipcMain.handle("app:transport", (event, command) => {
    assertOverlaySender(event);
    return sendTransport(command);
  });
  ipcMain.handle("app:quit", (event) => {
    assertOverlaySender(event);
    quitApp();
    return true;
  });

  ipcMain.handle("app:test-sound", (event) => {
    assertOverlaySender(event);

    voiceTestSound += 1;
    sendDeviceConfig();
    return voiceTestSound;
  });
  ipcMain.handle("app:monitor-mic", (event, on) => {
    assertOverlaySender(event);
    voiceMonitorMic = Boolean(on);
    sendDeviceConfig();
    return voiceMonitorMic;
  });
  ipcMain.on("app:activity", (event, source) => { assertOverlaySender(event); recordActivity(source === "keydown" ? "overlay-key" : "overlay"); });
  ipcMain.on("app:overlay-expanded", (event, expanded) => {
    assertOverlaySender(event);
    overlayExpanded = Boolean(expanded);
    ensureOverlayView();
    resizeOverlay();

    resizeTabView();
  });
  ipcMain.on("app:side-panel", async (event, open) => {
    assertOverlaySender(event);

    if (open) await refreshVolume();
    setSidePanel(open);

    if (open) await refreshVolume();
  });
  ipcMain.on("app:keyboard", (event, visible) => { assertOverlaySender(event); setKeyboardVisible(visible); });
  ipcMain.handle("app:set-brightness", (event, value) => { assertOverlaySender(event); return setBrightness(value); });
  ipcMain.handle("app:set-volume", (event, value) => { assertOverlaySender(event); return setVolume(value); });
  ipcMain.on("app:keyboard-key", (event, key) => { assertOverlaySender(event); sendKeyboardKey(key); });
  ipcMain.on("remote:text-input", (event, focused) => {
    if (event.sender !== win?.webContents) return;
    clearTimeout(inputBlurTimer);
    if (focused) { sidePanelOpen = false; resizeOverlay(); resizeTabView(); sendTabState(); setKeyboardVisible(true); }
    else {
      const keySequenceAtBlur = keyboardKeySequence;
      inputBlurTimer = setTimeout(() => {
        if (keyboardKeySequence === keySequenceAtBlur) setKeyboardVisible(false);
      }, 350);
    }
  });

  ipcMain.on("tab:open-drawer", (event) => {
    assertTabSender(event);
    if (sidePanelOpen) return;
    setSidePanel(true);
  });
}

const hasLock = app.requestSingleInstanceLock();
if (!hasLock) app.quit();

moveCachesToMemory(app.getPath("userData"), runtimeDirectory("chromium"));

function disableSpellChecker() {

  session.defaultSession.setSpellCheckerEnabled(false);
}

function readDisplayModeFlag() {
  const args = process.argv.slice(1);
  if (args.includes("--window")) return "window";
  if (args.includes("--fullscreen")) return "fullscreen";
  return null;
}

app.commandLine.appendSwitch("disable-features", "Translate,MediaRouter,OptimizationHints,CalculateNativeWinOcclusion,InterestFeedContentSuggestions,BackForwardCache,AcceptCHFrame,AutofillServerCommunication,CertificateTransparencyComponentUpdater,DialMediaRouteProvider,GlobalMediaControls,PrivacySandboxSettings4,SpellcheckService");

app.commandLine.appendSwitch("disk-cache-size", String(16 * 1024 * 1024));
app.commandLine.appendSwitch("media-cache-size", String(4 * 1024 * 1024));
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
  disableSpellChecker();
  session.defaultSession.setPermissionRequestHandler((webContents, permission, callback, details) => {
    const allowed = webContents === overlayView?.webContents && permission === "media" && (details.mediaTypes || []).includes("video");
    callback(allowed);
  });
  await loadSettings();

  const flagMode = readDisplayModeFlag();
  if (flagMode) settings.displayMode = flagMode;
  registerHealthProbes();
  healthChecker.start();
  registerIpc();
  createWindow();

  refreshVolume();
  watchVolume();

  watchScreenState();

  await startDeviceProgram();
  startControlPlane();
  app.on("activate", () => { if (!BrowserWindow.getAllWindows().length) createWindow(); });
  app.on("second-instance", () => {
    if (!win || win.isDestroyed()) return;
    if (win.isMinimized()) win.restore();
    win.focus();
  });
});

app.on("before-quit", (event) => {
  if (quitting) return;
  healthChecker.stop();
  stopWatchingVolume();
  stopWatchingScreen();
  cancelIdleTimers(); clearTimeout(urlTimer); clearTimeout(viewRetryTimer); clearTimeout(notificationTimer);

  if (deviceClient) {
    const client = deviceClient;
    deviceClient = null;
    event.preventDefault();
    client.stop().finally(() => {
      quitting = true;
      app.quit();
    });
  }
  if (controlClient) {
    controlClient.stop();
    controlClient = null;
  }
});
app.on("window-all-closed", () => { if (process.platform !== "darwin") app.quit(); });
