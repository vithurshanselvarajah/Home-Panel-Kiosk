const { app, BrowserWindow, WebContentsView, ipcMain, screen, safeStorage, session, clipboard } = require("electron");
const fs = require("node:fs/promises");
const fsSync = require("node:fs");
const path = require("node:path");
const { execFile } = require("node:child_process");
const os = require("node:os");
const { safeUrl, settingNumber, settingBoolean, displayMode: readDisplayMode, windowBounds, hostIdentity, DISPLAY_MODES } = require("./utils");
const { generateEncryptionKey } = require("./encryption");
const QRCode = require("qrcode");
const { createHealthChecker, probeHttp } = require("./health");
const { DeviceClient, findSidecar } = require("./device");
const { ControlClient, findControlSocket } = require("./control");
const { moveCachesToMemory } = require("./caches");

const HOSTNAME = hostIdentity(os.hostname());

// The Wayland backend is selected before Electron starts, not through
// app.commandLine: the ozone platform is chosen while the process is being
// set up, and a switch added afterwards is too late, so Electron falls back
// to X11 even though the Wayland socket is there.
//
// The switch is added to the command line itself, and the environment is
// checked for the socket that goes with it. A session that has no Wayland
// socket keeps the default backend, which is what a desktop with X11 wants.
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
  // The microphone and speaker are local hardware choices.
  voiceAudioInput: "",
  voiceAudioOutput: "",
  voiceStartupVolume: 50,
  musicEnabled: true,
  musicName: HOSTNAME,
  musicUnpaired: false,
  cameraEnabled: false,
  cameraPassword: "",
};

// The microphone monitor and the test sound are not settings.
//
// They act at once rather than being saved: the monitor holds the
// microphone open, and a saved "on" would leave it open after a restart
// with nobody watching the meter; and the test sound is a counter, so a
// saved one plays the tone again every time the device starts.
//
// They live here rather than in the settings for that reason.
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
// overlayPopup is whether the volume popup or the mute indicator is up.
//
// They are drawn on the main screen rather than in the drawer, because
// the volume is changed with the buttons on the unit and the mute is
// flipped with the button on top of it — and a person doing either is
// not looking in a drawer.
//
// The overlay view is the only thing that draws them, so it has to be
// on the screen while they are up, and it has to be the size of the
// screen: the popup is across the top and the indicator is in the
// corner, and neither is inside the strip along the top right that the
// drawer and the tab share.
let overlayPopup = false;
// voiceOverlayFull is whether the wake word overlay is covering the
// screen.
//
// It is separate from the voice indicator: the indicator is a strip
// along the bottom of the view, and the wake word overlay covers
// everything. The overlay view is sized to whichever of the two is
// showing.
let voiceOverlayFull = false;

let sidePanelOpen = false;
// The tab is the pull out tab that opens the drawer.
//
// It is a view of its own rather than a part of the overlay, because a view
// that is over the dashboard swallows the taps meant for the dashboard: the
// tab is the only thing that is allowed to be over it, and it is only as
// big as the tab.
let tabView;
let tabAttached = false;
let keyboardVisible = false;
let inputBlurTimer;
let keyboardKeySequence = 0;
// The status of each thing the drawer shows.
//
// "control" is the link to Home Assistant. It is not the socket to the
// device program, which is on this machine and is up whatever Home
// Assistant is doing.
const serviceStatus = { control: "unknown", immich: "unknown", dashboard: "unknown" };
let lastSentState = "";
let displayCommandId = 0;
let requestedDisplayStatus = null;
let legacyCameraNames = [];
let overlayRecycleTimer;
let viewRetryTimer;
let lastPointerMoveAt = 0;
let deviceClient;
// quitting is whether the application is on its way out.
//
// The quit waits for the sidecar to release the sound card, and the wait
// holds the quit up. This flag says the second attempt is the real one.
let quitting = false;
const OVERLAY_RECYCLE_DELAY_MS = 60_000;
const VIEW_RETRY_INTERVAL_MS = 15_000;
const POINTER_MOVE_INTERVAL_MS = 5_000;
const deviceId = HOSTNAME;
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

// probeControl checks that the device program is reachable.
//
// This is the socket between the panel and the device program, which is
// on this machine. It is not the link to Home Assistant: the drawer
// shows that, and the device program is what knows about it.
//
// It is checked under its own name so that it does not overwrite what
// the drawer shows.
function probeControl() {
  if (!controlClient?.connected) throw new Error("disconnected");
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
    device: probeControl,
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
    // The machine hostname is the one stable identity used by every
    // Home Assistant and Music Assistant surface. Older saved aliases
    // are deliberately ignored.
    deviceName: HOSTNAME,
    esphomeDeviceName: HOSTNAME,
    musicName: HOSTNAME,
  };
}

function assertOverlaySender(event) {
  if (event.sender !== overlayView?.webContents) throw new Error("Untrusted IPC sender");
}

// Checks that the message came from the pull out tab.
//
// The tab is a page of its own, so it is told apart from the overlay and
// from the dashboard.
function assertTabSender(event) {
  if (event.sender !== tabView?.webContents) throw new Error("Untrusted IPC sender");
}

// Sends to the overlay, tolerating a view that is being torn down.
function sendToOverlay(channel, payload) {
  if (!overlayView?.webContents || overlayView.webContents.isDestroyed()) return;
  try {
    overlayView.webContents.send(channel, payload);
  } catch { /* the frame was disposed between the check and the send */ }
}

// sendState tells the overlay and Home Assistant what the panel is doing.
//
// The overlay is only told when something it draws has changed, because
// the state is sent often and the overlay redraws when it arrives.
//
// A caller that needs the overlay to have the state even if it has not
// changed asks for it to be forced. The overlay is destroyed and made
// again while the drawer is shut, so a drawer that was not there when
// the state last changed has never been told about it.
function sendState(force = false) {
  const state = { mode, displayIsOff, keyboardVisible, sidePanelOpen, displayMode: displayMode(), brightness: currentBrightness(), volume: currentVolume(), muted: currentMute(), version: app.getVersion(), serviceStatus: { ...serviceStatus }, lastActivity: new Date(lastActivity).toISOString() };
  const fingerprint = `${state.mode}|${state.displayIsOff}|${state.keyboardVisible}|${state.sidePanelOpen}|${state.displayMode}|${state.brightness}|${state.volume}|${JSON.stringify(state.serviceStatus)}`;
  if (force || fingerprint !== lastSentState) {
    lastSentState = fingerprint;
    sendToOverlay("app:state", state);
  }
  publishState(state);
}

// newEncryptionKey makes a key for the Home Assistant connection.
//
// The key is thirty two bytes, and the protocol wants it as base64.
// It is made from the system's own random source, so two panels never
// get the same one.
//
// The key is shown in the settings, because it is a setting that is read
// and copied into Home Assistant rather than a password that is typed
// once and forgotten.

// Sends the device state to the overlay.
//
// It is sent on its own channel because it changes on its own schedule, and
// the overlay draws it differently from the rest of the state.
function sendDeviceState() {
  if (!deviceClient) return;
  const state = deviceClient.state;
  sendToOverlay("app:device", state);
  watchTheOverlay(state);
}

// The overlay watchdog.
//
// The voice overlay is drawn by the renderer, from the device's state.
// The renderer tells this process when the overlay is up, so that the
// view can be sized to cover the screen.
//
// That telling can be lost: the renderer may have been built after the
// state was sent, and a state that arrives at a page that is not
// listening yet is a state nobody saw. The overlay then does not come
// up, and the panel sits there saying nothing while the assistant is
// answering.
//
// So this process watches the state itself. When the state says the
// voice is happening and the renderer has not said the overlay is up,
// the state is sent again — the renderer gets a second chance, and the
// overlay appears.
//
// The timer is short, because the thing being watched is a person
// waiting for an answer, and it stops as soon as the overlay is up.
const OVERLAY_WATCHDOG_MS = 400;

let overlayWatchdogTimer = null;

// watchTheOverlay watches for an overlay that did not come up.
function watchTheOverlay(state) {
  if (!overlayWanted(state)) {
    clearTimeout(overlayWatchdogTimer);
    overlayWatchdogTimer = null;
    return;
  }
  // The overlay is up, or the renderer is about to say so.
  if (voiceOverlayFull) {
    clearTimeout(overlayWatchdogTimer);
    overlayWatchdogTimer = null;
    return;
  }
  if (overlayWatchdogTimer) return;
  overlayWatchdogTimer = setTimeout(() => {
    overlayWatchdogTimer = null;
    // The state is sent again. The renderer may not have been listening
    // the first time, and the state is the only thing it needs.
    if (overlayWanted(deviceClient?.state)) sendDeviceState();
  }, OVERLAY_WATCHDOG_MS);
}

// overlayWanted says whether the state says the overlay belongs on the
// screen.
//
// It is the same rule the renderer draws by: the voice is happening and
// the microphone is not muted.
function overlayWanted(state) {
  if (!state || state.muted) return false;
  return Boolean(state.speaking || state.thinking || state.listening);
}

// publishState tells the device program what the panel is doing.
//
// The device program holds the Home Assistant connection, so it is the
// one that publishes the state. The panel only has to say what it is
// doing, and the device program turns that into the entities Home
// Assistant sees.
function publishState(state) {
  if (!controlClient?.connected) return;
  // The volume is the speaker's volume, so it goes to the device
  // program, which tells the speaker.
  //
  // It is only sent when it is known. A panel that has not read the
  // mixer yet does not know the volume, and saying nothing is not the
  // same as saying zero.
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
    startupVolume: Math.max(0, Math.min(100, Number(settings.voiceStartupVolume) || 0)),
    musicEnabled: Boolean(settings.musicEnabled),
    musicUnpaired: Boolean(settings.musicUnpaired),
  };
  if (Number.isFinite(state.volume)) payload.volume = state.volume;
  controlClient.sendState(payload);
}

// sendTransport asks the device program to drive the music transport.
//
// The transport belongs to the music server and the panel has the
// buttons, so the request goes over the control socket. The device
// program knows which commands the server accepts and drops the rest.
function sendTransport(command) {
  if (!command || typeof command.action !== "string") return false;
  if (!controlClient?.connected) return false;
  const payload = { action: command.action };
  // Which music the command is for: the music server's or the panel's
  // own. They are different things and each has its own transport.
  if (command.source === "home" || command.source === "server") {
    payload.source = command.source;
  }
  if (Number.isFinite(Number(command.positionMs))) {
    payload.positionMs = Math.max(0, Math.round(Number(command.positionMs)));
  }
  return controlClient.sendTransport(payload);
}

function showNotice({ title = "Home Panel", message = "", duration = 8000, priority = "normal" } = {}) {  const safeDuration = Math.max(1000, Math.min(Number(duration) || 8000, 120000));
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
    // How long the address stays is how long the caller asked for, and
    // half a minute when nobody said.
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

// setDrawerTabVisible says whether the pull tab is wanted.
//
// The tab lives in the overlay, so turning it on or off changes what the
// overlay has to show, and the overlay is resized to match.
function setSidePanel(open) {
  // The drawer is only told about a change. Resizing the overlay view
  // when nothing has changed makes the drawer jump, and the drawer is
  // told about itself every time somebody presses a button in it.
  const next = Boolean(open);
  if (sidePanelOpen === next) return;
  sidePanelOpen = next;
  if (open) ensureOverlayView();
  resizeOverlay();
  // The tab is over the place the drawer opens in, so it is put away while
  // the drawer is open.
  resizeTabView();
  sendTabState();
  sendState();
}

function resizeOverlay() {
  if (!win || win.isDestroyed() || !overlayView) return;
  const { width, height } = win.getContentBounds();
  const homeButtonVisible = mode === "url";
  // The pull tab is the way the drawer is opened, so the overlay has to be
  // on the screen for the tab to be caught, even when nothing else in it
  // is showing.
  const needsOverlay = overlayExpanded || sidePanelOpen || keyboardVisible || homeButtonVisible || voiceOverlayFull || overlayPopup;
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
  if (overlayExpanded || sidePanelOpen || voiceOverlayFull || overlayPopup) {
    // The wake word overlay covers the whole screen, and so does the
    // drawer and the settings page. So does the volume popup and the
    // mute indicator: the popup is across the top and the indicator is
    // in the corner, and the strip along the top right is not where
    // either of them is.
    overlayView.setBounds({ x: 0, y: 0, width, height });
  } else if (keyboardVisible) {
    const keyboardHeight = Math.min(300, height);
    overlayView.setBounds({ x: 0, y: height - keyboardHeight, width, height: keyboardHeight });
  } else {
    // The strip along the top right, which holds the home button and the
    // pull tab. It is wide enough for the tab and the button together.
    const stripWidth = Math.min(210, width);
    overlayView.setBounds({ x: Math.max(0, width - stripWidth), y: 0, width: stripWidth, height: Math.min(58, height) });
  }
}

// setVoiceOverlayFull says whether the wake word overlay is covering the
// screen.
//
// The renderer knows when it is up: the overlay is drawn there. The view
// has to be sized to match, and the view is the main process's.
//
// The view is made first when the overlay is wanted, because a view that
// does not exist cannot be sized and the overlay would be drawn in a
// strip along the top right rather than over the screen.
function setVoiceOverlayFull(needed) {
  const next = Boolean(needed);
  if (voiceOverlayFull === next) return;
  voiceOverlayFull = next;
  if (next) ensureOverlayView();
  resizeOverlay();
  // The wake word overlay covers the whole screen, so the tab is put away
  // while it is up.
  resizeTabView();
}

// The overlay is a whole renderer process. Once nothing needs it for a while,
// close it so the memory is released on a small device, and rebuild it on the
// next use.
function scheduleOverlayRecycle() {
  if (overlayRecycleTimer) return;
  overlayRecycleTimer = setTimeout(() => {
    overlayRecycleTimer = undefined;
    if (overlayExpanded || sidePanelOpen || keyboardVisible || mode === "url" || voiceOverlayFull || overlayPopup) return;
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
  // The keyboard is along the bottom of the screen, where the tab is, so
  // the tab is put away while it is up.
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

// The brightness the screen was at before it was turned off.
//
// It is remembered because the way the screen is turned off on this
// device is by taking the backlight to zero, and the level it was at is
// what it is put back to.
let brightnessBeforeOff = null;

// runDisplayCommand turns the screen off or on.
//
// The screen is powered down with whatever the machine has: wlopm,
// kscreen-doctor or xset. A machine with none of them — this one — has
// no way to power the panel off, so the backlight is used instead: the
// screen goes dark, which is what turning it off means to a person
// standing in front of it.
//
// The backlight is the fallback rather than the only way, because the
// backlight is not the screen: a panel with a display that can be
// powered down should be powered down, so that it draws nothing at all.
function runDisplayCommand(operation) {
  const requestId = ++displayCommandId;
  requestedDisplayStatus = operation;
  const candidates = operation === "off"
    ? [["wlopm", ["--off", "*"]], ["kscreen-doctor", ["--dpms", "off"]], ["xset", ["dpms", "force", "off"]]]
    : [["wlopm", ["--on", "*"]], ["kscreen-doctor", ["--dpms", "on"]], ["xset", ["dpms", "force", "on"]]];
  const tryNext = (index) => {
    if (index >= candidates.length) {
      // None of them worked, so the backlight is the way.
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

// setBacklightOff turns the screen dark by taking the backlight to zero,
// and puts it back where it was.
//
// The level it was at is remembered, because a screen that comes back
// dark is a screen that looks broken.
//
// The level is published as the brightness, so that Home Assistant sees
// a screen that is off rather than a screen that is at zero: a person
// reading the entity should not think the brightness has been turned
// down when the screen is merely off.
async function setBacklightOff(off) {
  if (off) {
    const level = currentBrightness();
    if (level === null || level === 0) {
      // It is already dark, so there is nothing to remember.
      displayIsOff = true;
      sendState();
      return;
    }
    brightnessBeforeOff = level;
    await writeBacklight(0);
    displayIsOff = true;
    sendState();
    return;
  }
  const level = brightnessBeforeOff;
  brightnessBeforeOff = null;
  // A screen that comes back at the level it was at is a screen that
  // looks like it never went off.
  await writeBacklight(level === null || level <= 0 ? 60 : level);
  displayIsOff = false;
  sendState();
}

// writeBacklight sets the backlight to a percentage.
async function writeBacklight(percent) {
  try {
    const devices = await fs.readdir("/sys/class/backlight");
    for (const device of devices) {
      const root = path.join("/sys/class/backlight", device);
      const maximum = Number(await fs.readFile(path.join(root, "max_brightness"), "utf8"));
      if (!maximum) continue;
      await fs.writeFile(path.join(root, "brightness"), String(Math.round(maximum * percent / 100)));
      return true;
    }
  } catch (error) {
    console.warn("Backlight control unavailable:", error.message);
  }
  return false;
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

// backlightLevel is the level the backlight is actually at, as a
// percentage.
//
// It is the light's own level and nothing else: it does not know about
// a screen that has been turned off, and it does not remember anything.
// The difference matters, because a comparison against the wrong thing
// is how a brightness that is already set ends up not being set.
function backlightLevel() {
  try {
    for (const device of fsSync.readdirSync("/sys/class/backlight")) {
      const root = path.join("/sys/class/backlight", device);
      const maximum = Number(fsSync.readFileSync(path.join(root, "max_brightness"), "utf8"));
      const value = Number(fsSync.readFileSync(path.join(root, "brightness"), "utf8"));
      if (maximum && Number.isFinite(value)) return Math.round((value / maximum) * 100);
    }
  } catch { /* no backlight to read */ }
  return null;
}

// currentBrightness is the brightness the backlight is set to.
//
// The backlight is the truth: the panel may have been dimmed by something
// else, or the setting may not have been applied. Reading it back means
// the drawer never claims a brightness the screen is not at.
//
// A screen that has been turned off is the exception. It is turned off
// by taking the backlight to zero on this device, so the backlight says
// zero while the screen is off — and a brightness of zero published to
// Home Assistant is a brightness that comes straight back as a command.
// So the level the screen was at is published instead, which is the
// truth about the setting, as opposed to the truth about the light.
function currentBrightness() {
  const percent = backlightLevel();
  if (percent === null) return null;
  if (percent === 0 && brightnessBeforeOff !== null) return brightnessBeforeOff;
  return percent;
}

// systemVolume is the volume of the mixer.
//
// It is kept rather than read every time, because reading it means
// running a command and the state is sent often. It is refreshed when the
// drawer opens and when the volume is changed.
let systemVolume = null;

// micMuted is whether the system microphone is muted.
//
// It is null when the mixer cannot say, and the drawer shows the
// microphone's mute as unknown when it cannot.
//
// The mute is the microphone's and not the speaker's: a person who
// mutes the panel wants the room to stop hearing them, and they still
// want to hear the music.
let micMuted = null;

function currentVolume() {
  return systemVolume;
}

// currentMute is whether the microphone is muted.
function currentMute() {
  return micMuted;
}

// refreshVolume reads the microphone's mute.
//
// It is called when the drawer opens and when the volume is changed. A
// panel that read it every time it sent its state would run a command
// every time it sent its state.
//
// The volume is not read here. The level is the device program's and it
// tells the panel when it changes, so there is nothing to read. The mute
// comes from the device program too: on the Echo Show 8 it is the
// hardware latch's position.
async function refreshVolume() {
  // The reading is under the lock, so that a reading cannot happen
  // while the button is writing. A reading that happens halfway
  // through a write sees the value from before it, and takes it for a
  // change from outside — and undoes it.
  return withMuteLock(async () => {
    const muted = deviceClient?.state?.muted;
    if (typeof muted !== "boolean") return false;
    micMuted = muted;
    // The state is sent whatever the value is. The overlay is destroyed and
    // made again while the drawer is shut, so a drawer that was not there
    // when the volume was last read has never been told what it is.
    sendState(true);
    return true;
  });
}

// The volume watcher.
//
// The volume can be changed by something other than the panel: the
// keyboard's media keys, a desktop's own control, an automation. So the
// panel watches the mixer while the drawer is open and follows it.
//
// It watches for as long as the panel is running, and it stops when the
// panel is shutting down.
let volumeWatcher = null;

// watchVolume follows the mixer's volume.
//
// It runs for as long as the panel does, because the device program's
// speaker follows the mixer and the drawer is shut most of the time.
//
// The first reading is not a change, so it does not send the state: the
// panel has just been given the volume.
function watchVolume() {
  stopWatchingVolume();
  volumeWatcher = setInterval(watchTick, volumeWatchInterval);
  // The watcher is not what should keep the app alive.
  if (typeof volumeWatcher.unref === "function") volumeWatcher.unref();
}

// watchTick follows the device program's mute.
//
// It is the watcher's body on its own, so that a test can run it
// without waiting for the timer.
//
// Neither the volume nor the mute is read from the mixer. The level is
// the device program's and it tells the panel when it changes; the mute
// is the hardware latch's and the device program reports where the latch
// ended up. A watch would only ever race them: the panel would read a
// value the device program had not applied yet and undo the change.
async function watchTick() {
    // The device program's own state says where the microphone's mute
    // is, and it is the latch's position on the Echo Show 8. The latch
    // is moved by the button on the unit and by nothing else, so this
    // only ever follows: the panel cannot switch the mute, and so there
    // is no write here to race with.
    const muted = deviceClient?.state?.muted;
    if (typeof muted !== "boolean") return;
    if (muted === micMuted) return;
    micMuted = muted;
    sendState(true);
}

// stopWatchingVolume stops following the mixer.
function stopWatchingVolume() {
  // The check is against null and not for truthiness: a timer's number
  // may be zero, and zero is a timer like any other.
  if (volumeWatcher !== null) {
    clearInterval(volumeWatcher);
    volumeWatcher = null;
  }
}

// volumeWatchInterval is how often the mixer is read.
//
// The mixer is read for as long as the panel is running, because a
// volume changed by the keyboard's media keys has to reach the speaker
// as well as the drawer.
//
// It is quick enough that a change is seen as it happens and slow
// enough that it is not a busy loop: the read runs a process, and a
// process every few hundred milliseconds is a process every few hundred
// milliseconds for as long as the panel is up.
const volumeWatchInterval = 1000;

async function setBrightness(value) {
  const percent = Math.max(0, Math.min(100, Math.round(Number(value))));
  if (!Number.isFinite(percent)) return;
  // A brightness the screen is already at is not a change.
  //
  // This is what stops the brightness from wedging itself at zero. The
  // panel tells Home Assistant the brightness, Home Assistant sends its
  // number entities back as commands, and so a brightness of zero came
  // back as a command for a brightness of zero, over and over. The
  // slider could be moved and the screen would go straight back to
  // black, which on a screen that is already black looks like the
  // slider is broken.
  //
  // The backlight is the truth, so the comparison is against the light's
  // own level and not against what is published.
  //
  // The published level is not the same thing when the screen is off:
  // it is the level the screen was at, not the level the light is at.
  // Comparing against it would mean that asking for the level the
  // screen was at before it went off is taken for a change that has
  // already been made — and the screen stays dark.
  if (backlightLevel() === percent) return;
  try {
    const devices = await fs.readdir("/sys/class/backlight");
    for (const device of devices) {
      const root = path.join("/sys/class/backlight", device);
      const maximum = Number(await fs.readFile(path.join(root, "max_brightness"), "utf8"));
      if (!maximum) continue;
      await fs.writeFile(path.join(root, "brightness"), String(Math.round(maximum * percent / 100)));
      // The drawer shows the brightness, so it has to be told.
      sendState();
      return;
    }
  } catch (error) {
    console.warn("Backlight control unavailable:", error.message);
  }
  showNotice({ title: "Brightness control unavailable", message: "No writable Linux backlight device was found." });
}

// setVolume changes the volume.
//
// The level is the device program's: it applies the level to everything it
// plays — the reply, the music and the test sound — on a curve, so that
// half the dial is a quiet conversation rather than half the amplitude.
//
// So the panel asks the device program for the level and does not reach
// for the mixer itself. A level applied in two places is a level nobody
// can reason about: the same slider position sounds different depending
// on which path the audio took.
//
// It is best effort: a panel whose device program is not there yet is a
// panel whose volume cannot be changed from here.
async function setVolume(value) {
  const percent = Math.max(0, Math.min(100, Number(value)));
  if (!Number.isFinite(percent)) return false;
  return withMuteLock(async () => {
    if (!await sendDeviceVolume(percent)) return false;
    // The level is reflected in the drawer straight away, and published
    // back to Home Assistant.
    systemVolume = percent;
    sendState(true);
    return true;
  });
}

// followDeviceVolume shows the level the device program is using.
//
// The level is the device program's, and the panel shows it. So when the
// device program says where the level is — because the physical buttons
// moved it, or Home Assistant did — the slider follows.
//
// The level is not sent back: the device program has just told us, and
// the two telling each other about the same change for ever is what the
// follower's settling is there to stop.
function followDeviceVolume(percent) {
  const value = Math.max(0, Math.min(100, Math.round(Number(percent))));
  if (!Number.isFinite(value)) return;
  if (value === systemVolume) return;
  systemVolume = value;
  sendState(true);
}

// sendDeviceVolume asks the device program for a level.
//
// The device program is the owner of the level, so the request goes over
// the control socket rather than to the mixer.
async function sendDeviceVolume(percent) {
  if (!controlClient?.connected) return false;
  return controlClient.sendVolume(percent);
}

// followDeviceMute makes the system microphone follow the device's.
//
// The device's mute is Home Assistant's mute: a switch on the device
// that Home Assistant can turn on and off. Turning it on mutes the
// system's microphone, so the room stops hearing the person in front
// of the panel.
//
// And the other way round: muting the system's microphone turns the
// device's switch on, so Home Assistant shows what is really happening.
//
// The two follow each other, and each one only follows the other when
// the change did not come from here. That is what stops them arguing:
// the panel mutes the microphone, the device says it is muted, the panel
// sees that it said so itself and does nothing.
//
// The mute is the hardware latch's, and only the button on top of the
// unit moves it. The latch is one-way from software: the button is also
// the only thing that releases it. So the panel does not switch the
// mute, and there is nothing here to remember about a change it made.
//
// What it does is follow the latch, so that the indicator on the screen
// says where the microphones really are.

// The mute is changed in one place at a time.
//
// The watcher reads the mixer and the button writes it, and if they
// happen at once the watcher can read the value from before the button
// wrote it — and then undo it. So a change and a reading of the
// change do not happen at the same time.
let muteLock = null;

function followDeviceMute() {
  const muted = deviceClient?.state?.muted;
  if (typeof muted !== "boolean") return;
  if (muted === micMuted) return;
  micMuted = muted;
  // The system microphone is not written.
  //
  // On the Echo Show 8 the mute is a hardware latch and the device
  // program has already moved it: the state it reports is the latch's
  // own position. The mixer is not where the microphone is cut.
  sendState(true);
}

// withMuteLock does one thing with the mute at a time.
//
// The watcher reads the mixer and the button writes it. If they happen
// at once the watcher can read the value from before the button wrote
// it, and then take it for a change from outside and undo it — which
// is a mute that turns itself off.
function withMuteLock(work) {
  const next = (muteLock || Promise.resolve()).then(work, work);
  // The lock is not what should keep the app alive.
  muteLock = next.catch(() => {});
  return next;
}

// handleCommand acts on a command.
//
// The commands come from two places: the interface, when a person
// presses something, and the device program, when Home Assistant asks
// for something. They are the same commands and they are handled the
// same way, so there is one handler for both.
//
// The names are the ones the panel has always used, so an automation
// that was written against the old broker still works.
async function handleCommand(command) {
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
      openMode("url", command.url || command.target || command.value || "", command.duration);
      break;
    case "brightness": recordActivity("home-assistant"); setBrightness(command.value); break;
    // A remote volume change (Music Assistant or Home Assistant) is
    // applied to the system mixer, then read back into the panel state.
    case "volume":
      recordActivity("home-assistant");
      // The device program sends the level with a number, and Home
      // Assistant sends it with a value. They are different directions:
      // the first is the level being told to us, the second is a level
      // being asked for.
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
        startup_volume: "voiceStartupVolume",
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
        : Math.max(0, Math.min(command.setting === "startup_volume" ? 100 : 1440, Math.round(Number(command.value) || 0)));
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

// startControlPlane connects the panel to the device program.
//
// The device program holds the Home Assistant connection, so the panel
// talks to it rather than to Home Assistant. The socket is in memory
// and the connection is tried again until it works, because the two
// are started together and the socket is not there yet when the panel
// starts.
function startControlPlane() {
  // The socket is where the sidecar was told to put it, so the two
  // cannot disagree about it.
  const socket = findControlSocket(runtimeDirectory(deviceId));
  controlClient = new ControlClient({
    socket,
    onCommand: (command) => handleCommand(command),
    // The link to the device program is not the link to Home
    // Assistant. The socket between the two is on this machine and is
    // up whatever Home Assistant is doing, so the drawer shows what the
    // device program says about Home Assistant instead.
    // The socket to the device program is on this machine, so its state
    // says nothing about Home Assistant. It is kept for the health
    // check, which is about the panel being able to reach it at all.
    onStatus: () => {},
    // The state is sent as soon as the connection is there, because the
    // connection is made after the first state has been sent.
    onConnect: () => {
      sendState(true);
      controlClient.sendCameraPassword(settings.cameraPassword || "");
    },
    // The link to Home Assistant is what the drawer shows, and only the
    // device program knows whether it is there.
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
  } catch { /* fall through to the backup */ }
  try {
    settings = await decodeSettings(JSON.parse(await fs.readFile(`${configPath()}.bak`, "utf8")));
    console.warn("Recovered settings from the backup copy.");
  } catch { settings = { ...DEFAULTS }; }
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
  // The voice settings. The panel is a voice satellite, and these are
  // what make it one.
  clean.voiceStartupVolume = settingNumber(clean.voiceStartupVolume) ?? DEFAULTS.voiceStartupVolume;
  clean.musicEnabled = Boolean(clean.musicEnabled);
  clean.musicUnpaired = Boolean(clean.musicUnpaired);
  clean.cameraEnabled = Boolean(clean.cameraEnabled);
  // The audio devices are named by the sidecar, so the names are kept as
  // they are. An empty name is the system default.
  clean.voiceAudioInput = String(clean.voiceAudioInput || "").trim();
  clean.voiceAudioOutput = String(clean.voiceAudioOutput || "").trim();
  // The encryption key is what the Home Assistant connection is made
  // with. It is a secret, so it is kept out of the file and put back
  // when the settings are read.
  // Whether the connection is encrypted is a setting of its own, and the
  // key follows it: no key means the connection is in plain text.
  clean.esphomeEncryptionEnabled = Boolean(clean.esphomeEncryptionEnabled);
  clean.esphomeEncryptionKey = String(clean.esphomeEncryptionKey || "").trim();
  clean.cameraPassword = String(clean.cameraPassword || "");
  if (clean.cameraPassword && (clean.cameraPassword.length < 8 || clean.cameraPassword.length > 128)) {
    throw new Error("The camera password must contain 8 to 128 characters.");
  }
  if (!clean.esphomeEncryptionEnabled) {
    // The connection is in plain text, so there is no key.
    clean.esphomeEncryptionKey = "";
  } else if (!clean.esphomeEncryptionKey) {
    // The connection is to be encrypted and there is no key, so one is
    // made. A panel that is encrypted is encrypted from the first
    // moment, rather than being left in plain text until somebody
    // thinks of a key.
    clean.esphomeEncryptionKey = generateEncryptionKey();
  }
  // The display mode is a setting, so a device that was set up before it
  // existed has none, and one with a nonsense value falls back to the
  // mode it was running in.
  clean.displayMode = DISPLAY_MODES.includes(clean.displayMode) ? clean.displayMode : displayMode();
  const persisted = { ...clean };
  // The encryption key is a secret, so it is kept out of the file and put
  // back when the settings are read.
  //
  // The old copy is always removed first. A key that has been cleared
  // has to stay cleared, and leaving the old copy in the file would
  // bring it back the next time the settings were read.
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
  // The mode is applied only when it changed, so saving the other settings
  // does not move the window the user is looking at.
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
  const deviceConfigChanged = ["voiceStartupVolume", "musicEnabled", "musicUnpaired", "cameraEnabled"]
    .some((key) => previous[key] !== settings[key]);
  if (restartDevice) await startDeviceProgram();
  else if (deviceConfigChanged) sendDeviceConfig();
  sendState(true);
  return publicSettings();
}

// Sends the configuration to the device program.
//
// Only the settings that were actually chosen are sent. A setting that was
// never touched is left out, so the device keeps what it had rather than
// being handed a default that was not the user's.
function sendDeviceConfig() {
  if (!deviceClient) return;
  // The wake word's settings are not here: they are entities on the
  // panel, so Home Assistant owns them. Writing them from here would
  // undo whatever Home Assistant had set.
  deviceClient.sendConfig({
    startupVolume: settingNumber(settings.voiceStartupVolume),
    sendspinEnabled: settingBoolean(settings.musicEnabled),
    sendspinName: settings.musicName || "",
    sendspinUnpaired: settingBoolean(settings.musicUnpaired),
    cameraEnabled: settingBoolean(settings.cameraEnabled),
    audioInput: settings.voiceAudioInput || "",
    audioOutput: settings.voiceAudioOutput || "",
    monitorMic: voiceMonitorMic,
    testSound: voiceTestSound,
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
  const { cameraPassword, ...visible } = settings;
  return { ...visible, cameraPasswordConfigured: Boolean(cameraPassword) };
}

// runtimeDirectory is where the live state of a device lives.
//
// It is in memory, because the live state is a reading and not a record:
// it changes as the device listens and speaks, and it is worth nothing
// once the process is gone. Writing it to the flash would wear the flash
// out for nothing.
//
// The service gives the application a directory in memory, and that is
// where the state goes when there is one. Without it the shared memory
// directory is used, and failing that the application's own directory —
// which is the disk, and is worse but works.
function runtimeDirectory(identity) {
  const name = identity.replace(/[^a-zA-Z0-9_-]/g, "-") || "device";
  for (const base of [runtimeBase(), "/dev/shm/homepanel"]) {
    const dir = path.join(base, name);
    try {
      fsSync.mkdirSync(dir, { recursive: true, mode: 0o700 });
      // The directory has to be in memory, or the live state goes to the
      // disk and the flash wears out for nothing. A directory that was
      // made on the disk is not one.
      if (!isMemory(dir)) continue;
      return dir;
    } catch { /* try the next one */ }
  }
  return path.join(app.getPath("userData"), "device-runtime");
}

/**
 * The directory the live state is kept in.
 *
 * The service makes a directory in memory and names it through
 * RUNTIME_DIRECTORY. The device program looks in the same place, and it
 * is told which place that is when it is started, so the two cannot
 * disagree about where the state is.
 */
function runtimeBase() {
  return process.env.RUNTIME_DIRECTORY || "/dev/shm/homepanel";
}

/**
 * Says whether a path is on a memory filesystem.
 *
 * The state is a few kilobytes, so the memory filesystem does not have to
 * be big. What matters is that it is not the disk.
 */
function isMemory(directory) {
  try {
    const stats = fsSync.statfsSync(directory);
    // TMPFS_MAGIC and RAMFS_MAGIC.
    return stats.type === 0x01021994 || stats.type === 0x858458f6;
  } catch {
    return false;
  }
}

// Starts the device program, if the panel is set up to be a voice
// satellite.
//
// It is the same program that answers Home Assistant over the ESPHome
// protocol. The panel is a renderer and the device program owns the
// connection, the microphone, the speaker and the wake word.
//
// It is started whether or not the voice is switched on, because the
// connection to Home Assistant lives in it. A panel whose voice is off is
// still an ESPHome device with entities and services.
async function startDeviceProgram() {
  if (deviceClient) {
    // The old one has to be gone before the new one starts. It holds
    // the microphone and the speaker, and a new one that starts while the
    // old one is still holding them finds the card busy — so the panel
    // never hears anything.
    await deviceClient.stop();
    deviceClient = null;
  }

  const binary = findSidecar(app.getAppPath(), process.resourcesPath, process.execPath);
  // The live state and the live level are written into memory, not onto
  // the disk: they change as the device listens and speaks, and they are
  // worth nothing once the process is gone. The device program decides
  // where they go — it is the one that writes them — and the application
  // reads them from the same place.
  //
  // The path is derived from the device's identity, which is what the
  // device program derives it from too.
  const identity = deviceId;
  const runtimeDir = runtimeDirectory(identity);
  // The device program is told where the live state lives, so that it
  // writes it where this reads it.
  const runtimeBaseDir = path.dirname(runtimeDir);
  const stateFile = path.join(runtimeDir, "state.json");
  const levelFile = path.join(runtimeDir, "level");
  const configFile = path.join(app.getPath("userData"), "device-config.json");
  // The data directory is where the device program keeps what it has to
  // keep — the music player's identity and its pairing records. It is on
  // the disk, because the music player's identity has to survive a
  // restart: a player that forgets its identity is a new player, and the
  // server has to pair with it again.
  const dataDirectory = path.join(app.getPath("userData"), "device");
  deviceClient = new DeviceClient({
    binary,
    stateFile,
    levelFile,
    configFile,
    dataDirectory,
    deviceId: identity,
    board: HOSTNAME,
    runtimeDirectory: runtimeBaseDir,
    // The audio devices are named on the command line as well as in the
    // configuration, so the device opens the right ones before the
    // configuration is read.
    mic: settings.voiceAudioInput || "",
    speaker: settings.voiceAudioOutput || "",
    // The control socket is where the panel and the device program meet.
    // It is in memory, because it carries what is happening now rather
    // than anything worth keeping.
    // The socket is in the device's own directory, beside the live
    // state, so the panel and the device program find the same one.
    controlSocket: path.join(runtimeDir, "control.sock"),
    // The Home Assistant connection is made by the device program, so it
    // needs the key and the name the panel is known by.
    esphomeKey: settings.esphomeEncryptionKey || "",
    esphomeName: HOSTNAME,
    onState: () => {
      sendDeviceState();
      // The device's mute is the microphone's, and it is the panel's
      // microphone: the panel mutes the system's microphone so the room
      // stops hearing the person in front of it.
      //
      // The two are the same thing said in two places, and each one
      // tells the other what it has just been told. So a change in
      // either settles rather than going round.
      followDeviceMute();
    },
    onLog: (message) => { if (message) console.info("device:", message); },
  });

  deviceClient.start();

  // The configuration is written before the sidecar starts, so the sidecar
  // reads the settings on its first pass rather than waiting for the next
  // change. Otherwise a device that was configured yesterday would run
  // today with the defaults.
  sendDeviceConfig();
}

function createOverlayView() {
  overlayView = new WebContentsView({ webPreferences: {
    // The overlay loads a bundled local page, so it is trusted enough to run
    // unsandboxed and share the field editing helper with the main process.
    preload: path.join(__dirname, "preload.js"), contextIsolation: true, nodeIntegration: false, sandbox: false,
  } });
  overlayView.setBackgroundColor("#00000000");
  const webContents = overlayView.webContents;
  webContents.loadFile(path.join(__dirname, "ui", "index.html"));
  // The overlay carries the drawer and the touch keyboard, and the drawer is
  // opened by dragging in from the edge of the screen. The touchscreen is
  // reported as a mouse, so the drag has to be turned into touch events
  // first, the way it is for the dashboard. Without this the buttons work
  // but the drawer cannot be dragged open.
  webContents.on("did-finish-load", () => {
    enableTouchEmulation(webContents);
    // The state is sent once the page is loaded, because a page that is
    // still loading has nothing listening for it. The fingerprint is
    // cleared first, so the state is sent even when it has not changed
    // since the last time: the overlay may have been built after the
    // state was last sent, and it would otherwise never hear about it.
    lastSentState = "";
    sendState();
    // The device's state is sent as well, and for the same reason.
    //
    // The voice overlay is drawn from the device's state and from
    // nothing else, so an overlay that was built after the device last
    // changed would sit there blank until the device happened to change
    // again — which, when the device is already talking, is when the
    // answer is over. The overlay was built because the voice started,
    // so the state that says the voice started is exactly the state the
    // new overlay needs.
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
}

function waitForOverlayLoad(webContents) {
  return new Promise((resolve, reject) => {
    const finish = () => { cleanup(); resolve(); };
    const fail = (_event, code, description, _url, isMainFrame) => {
      if (!isMainFrame) return;
      cleanup();
      reject(new Error(`Panel camera view failed to load (${code}): ${description}`));
    };
    const destroyed = () => { cleanup(); reject(new Error("Panel camera view was closed")); };
    const cleanup = () => {
      webContents.removeListener("did-finish-load", finish);
      webContents.removeListener("did-fail-load", fail);
      webContents.removeListener("destroyed", destroyed);
    };
    webContents.once("did-finish-load", finish);
    webContents.on("did-fail-load", fail);
    webContents.once("destroyed", destroyed);
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
  // Force the next sendState to reach the rebuilt overlay.
  lastSentState = "";
}

// The pull out tab.
//
// It is a view of its own, and it is small, because a view over the
// dashboard takes the taps meant for the dashboard. The tab is the only
// thing that is allowed to be over it, and it is only as big as the tab.
//
// It is always on the screen, because it is the way the drawer is opened.
// The only thing that puts it away is the drawer being open, because the
// drawer is over the place it sits in.
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
  resizeTabView();
}

// Puts the tab where it belongs.
//
// It sits on the middle right of the screen, hanging over the right hand
// edge so that it reads as something to pull out.
//
// The view is given the whole of the tab and is put on the screen, and the
// tab itself is half off the edge inside that view. The half that is off
// the screen cannot be pressed, so the tab is drawn so that the part that
// is on the screen is a finger's width: the visible half is the whole of
// the target, and it is big enough to press.
function resizeTabView() {
  if (!win || win.isDestroyed() || !tabView) return;
  const { width, height } = win.getContentBounds();
  const tabWidth = Math.min(56, width);
  const tabHeight = Math.min(150, Math.max(90, Math.round(height * 0.22)));
  // The tab is wanted only when the dashboard is showing plainly.
  //
  // The drawer is over the place it sits in, so it is put away while the
  // drawer is open. The wake word overlay covers the whole screen. The
  // settings page and the notification are drawn over the dashboard, and
  // the touch keyboard is along the bottom, which is where the tab is.
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
  // The view is on the right hand edge of the screen, and the tab inside
  // it hangs over the edge.
  tabView.setBounds({
    x: Math.max(0, width - tabWidth),
    y: Math.round((height - tabHeight) / 2),
    width: tabWidth,
    height: tabHeight,
  });
}

// Tells the tab whether the drawer is open, so that the tab can be put
// away when it is not wanted.
function sendTabState() {
  if (!tabView || tabView.webContents.isDestroyed()) return;
  try {
    tabView.webContents.send("tab:drawer-state", sidePanelOpen);
  } catch { /* the view was disposed between the check and the send */ }
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
    // The window is shown in both modes. Leaving it to the default in
    // fullscreen means a session that was never shown stays that way, and
    // the panel is then running with nothing on the screen.
    win.show();
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
  // The pull out tab is a view of its own, so it is made here too.
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
  // The test sound and the microphone monitor are settings that act
  // immediately rather than being saved, so they are their own calls.
  ipcMain.handle("app:test-sound", (event) => {
    assertOverlaySender(event);
    // The counter is what tells the device a new tone was asked for.
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
  ipcMain.on("app:overlay-popup", (event, up) => {
    assertOverlaySender(event);
    const next = Boolean(up);
    if (overlayPopup === next) return;
    overlayPopup = next;
    ensureOverlayView();
    resizeOverlay();
  });
  ipcMain.on("app:overlay-expanded", (event, expanded) => {
    assertOverlaySender(event);
    overlayExpanded = Boolean(expanded);
    ensureOverlayView();
    resizeOverlay();
    // The settings page and the notification are drawn over the dashboard,
    // so the tab is put away while they are up.
    resizeTabView();
  });
  ipcMain.on("app:side-panel", async (event, open) => {
    assertOverlaySender(event);
    // The drawer shows the volume, and the mixer may have been changed by
    // something else while the drawer was shut, so it is read when the
    // drawer opens rather than every time the state is sent.
    //
    // It is read first, and waited for, because setSidePanel sends the
    // state and the state is what carries the volume to the drawer.
    // Reading it after would send the state with the old volume and the
    // new one a moment later, which is the delay this is here to remove.
    if (open) await refreshVolume();
    setSidePanel(open);
    // The drawer shows the volume, and the volume can be changed by
    // something other than the panel, so the mixer is followed while the
    // drawer is open.
    // The mixer is followed whatever the drawer is doing, because the
    // device program's speaker follows it and the drawer is not the only
    // thing that shows the volume.
    //
    // The watcher is always running, so this is not needed here. It is
    // left so that the drawer's opening still reads the mixer at once
    // rather than waiting for the watcher.
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
  // The pull out tab opens the drawer. A tap is enough, and the tab is the
  // only thing over the dashboard, so the tap is not swallowed.
  ipcMain.on("tab:open-drawer", (event) => {
    assertTabSender(event);
    if (sidePanelOpen) return;
    setSidePanel(true);
  });
}

// Chromium's profile is split: the parts that are caches go into memory,
// and the parts that are records stay on the disk.
//
// Chromium writes its caches on every page load, and they are the largest
// thing the application writes. On a device whose storage is eMMC that is
// the main source of write wear, and the caches are worth nothing once
// the device is off.
//
// The records are the opposite: the settings, the cookies and the local
// storage are worth keeping, and they are small. They stay where they
// are.
const hasLock = app.requestSingleInstanceLock();
if (!hasLock) app.quit();

// Chromium's caches are put in memory while the module is loading, before
// Chromium has made its cache directories and opened them.
moveCachesToMemory(app.getPath("userData"), runtimeDirectory("chromium"));

// The spellchecker downloads a dictionary of half a megabyte into the
// profile on every start, and a kiosk never corrects a spelling.
//
// It is turned off when the app is ready, because the session cannot be
// asked for before then, and before the first page is loaded, because that
// is when the download starts.
function disableSpellChecker() {
  // The default session is the only one the app uses. Asking for a
  // partition would make one, and it would make one on the disk.
  session.defaultSession.setSpellCheckerEnabled(false);
}

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
// The disabled features are one list. A second call with the same switch
// replaces the first, so the spellchecker has to be in this list rather
// than in a list of its own.
app.commandLine.appendSwitch("disable-features", "Translate,MediaRouter,OptimizationHints,CalculateNativeWinOcclusion,InterestFeedContentSuggestions,BackForwardCache,AcceptCHFrame,AutofillServerCommunication,CertificateTransparencyComponentUpdater,DialMediaRouteProvider,GlobalMediaControls,PrivacySandboxSettings4,SpellcheckService");
// Keep Chromium's disk caches out of the eMMC: they are rewritten on every
// page load and are the main source of write wear on the target device.
// The caches are in memory and the memory is capped, so the caches
// themselves are capped. The default is a share of the free disk, which
// on a device with 4 GB of eMMC is a hundred times more than a kiosk
// needs.
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
  // The flag wins over the saved setting, but only for this run.
  const flagMode = readDisplayModeFlag();
  if (flagMode) settings.displayMode = flagMode;
  registerHealthProbes();
  healthChecker.start();
  registerIpc();
  createWindow();
  // The volume is read once when the app starts, and the mixer is
  // followed from then on.
  //
  // The mixer is followed whatever the drawer is doing, because the
  // device program's speaker follows it: a volume changed by the
  // keyboard's media keys has to reach the speaker as well as the
  // drawer, and the drawer is shut most of the time.
  //
  // It is not waited for: the panel is the thing a person is waiting
  // for, and a quarter of a second of mixer work does not hold it up.
  refreshVolume();
  watchVolume();
  // The device program is started before the control socket is opened.
  //
  // The socket is made by the device program, and it makes a new one
  // every time it starts — it removes the old file and binds a new
  // socket in its place. So a panel that connects first connects to
  // whichever socket file is lying around, which is the previous run's,
  // and the device program never sees it.
  //
  // The order is the other way round for the same reason: the device
  // program is stopped before the socket is closed, so that it is not
  // holding the sound card when the next one starts.
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
  cancelIdleTimers(); clearTimeout(urlTimer); clearTimeout(viewRetryTimer); clearTimeout(notificationTimer);
  // The sidecar holds the microphone and the speaker, so it is stopped
  // before the process that owns it goes away. Otherwise the system waits
  // for it to time out, and the audio devices stay held open.
  //
  // The quit waits for it, because the sidecar releases the sound card
  // when it exits and the next thing to want it — a restart, another
  // program — finds it busy. The wait is bounded inside the sidecar.
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
