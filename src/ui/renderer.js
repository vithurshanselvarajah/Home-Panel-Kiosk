const $ = (selector) => document.querySelector(selector);
const panel = $("#settings-panel");
const drawer = $("#side-panel");
const drawerBackdrop = $("#side-panel-backdrop");
const keyboard = $("#keyboard");
const form = $("#settings-form");
const voiceForm = $("#voice-form");
const voiceUI = window.VoiceUI ? window.VoiceUI.create({
  overlay: $("#voice-overlay"),
  onOverlayClosed: () => {
    overlayWasUp = false;
    window.homePanel.voiceOverlay(false);
  },

  statusLed: document.querySelector('[data-service="voice-settings"]'),
  statusLabel: $("#voice-status-label"),
  statusReason: $("#voice-status-reason"),
}) : null;
let loadedSettings;
let touchStart;
let dragState;
let shiftOn = false;
let symbolsOn = false;
let settingsKeyboardActive = false;

let keyboardTarget = null;

let displayMode = "fullscreen";

function keyboardOffset() {
  let bottom = 0;
  if (keyboard && !keyboard.hidden) {
    const rect = keyboard.getBoundingClientRect();
    bottom = Math.max(bottom, Math.round(window.innerHeight - rect.top));
  }
  const viewport = window.visualViewport;
  if (viewport && viewport.height && viewport.scale === 1) {
    bottom = Math.max(bottom, Math.round(window.innerHeight - viewport.height - viewport.offsetTop));
  }
  return bottom;
}

function syncSettingsLayout() {
  if (!panel || panel.hidden) return;
  panel.style.bottom = `${keyboardOffset()}px`;
  syncDrawerLayout();
}

function syncDrawerLayout() {
  if (!drawer) return;
  const homeBar = $("#return-home");
  const homeBarVisible = homeBar && !homeBar.hidden;
  drawer.classList.toggle("with-home-bar", Boolean(homeBarVisible));
  let bottom = 0;
  if (keyboard && !keyboard.hidden) {
    const rect = keyboard.getBoundingClientRect();
    bottom = Math.max(0, Math.round(window.innerHeight - rect.top));
  }
  drawer.style.bottom = `${bottom}px`;
}

function setCaret(field, position) {
  if (typeof field.selectionStart !== "number") return;
  try { field.setSelectionRange(position, position); } catch {  }
}

function applyToSettingsField(value) {
  const field = settingsField();
  if (!field) return false;
  if (value === "ENTER") { form.requestSubmit(); return true; }
  const result = window.homePanel.editFieldValue({
    value: field.value,
    type: field.type,
    selectionStart: field.selectionStart,
    selectionEnd: field.selectionEnd,
  }, value);
  if (!result) return true;
  field.value = result.value;
  setCaret(field, result.caret);
  field.dispatchEvent(new Event("input", { bubbles: true }));
  return true;
}

function settingsField() {
  const active = document.activeElement;
  if (active && form.contains(active) && "value" in active) return active;
  if (keyboardTarget && form.contains(keyboardTarget) && "value" in keyboardTarget) return keyboardTarget;
  return null;
}

function sendKey(value) {
  if (panel.hidden) { window.homePanel.keyboardKey(value); return; }
  applyToSettingsField(value);
}

function syncEncryptionKeyField() {
  const key = form.elements.namedItem("esphomeEncryptionKey");
  const enabled = form.elements.namedItem("esphomeEncryptionEnabled");
  if (!key || !enabled) return;
  key.disabled = !enabled.checked;
  const reveal = document.querySelector('[data-secret-toggle="ha"]');
  if (reveal) reveal.disabled = !enabled.checked;
  if (!enabled.checked) hideSecret("ha");
}

const secretViews = {
  ha: { open: false, token: "", reveal: $("#ha-secret-reveal"), value: $("#ha-secret-value"), qr: $("#ha-secret-qr") },
  sendspin: { open: false, token: "", reveal: $("#sendspin-secret-reveal"), value: $("#sendspin-secret-value"), qr: $("#sendspin-secret-qr") },
};

function hideSecret(kind) {
  const view = secretViews[kind];
  if (!view) return;
  view.open = false;
  view.token = "";
  view.reveal.hidden = true;
  view.value.textContent = "";
  view.qr.removeAttribute("src");
  const button = document.querySelector(`[data-secret-toggle="${kind}"]`);
  if (button) button.textContent = kind === "ha" ? "Show key + QR" : "Show token + QR";
}

async function toggleSecret(kind) {
  const view = secretViews[kind];
  if (!view || view.open) { hideSecret(kind); return; }

  if (!keyboard.hidden) setKeyboard(false);
  const button = document.querySelector(`[data-secret-toggle="${kind}"]`);
  if (button) button.disabled = true;
  try {
    const token = kind === "ha"
      ? String(form.elements.namedItem("esphomeEncryptionKey")?.value || "").trim()
      : await window.homePanel.getSendspinToken();
    if (!token) throw new Error(kind === "ha" ? "Turn on Home Assistant encryption and save a key first." : "The Sendspin pairing token is not available yet.");
    view.token = token;
    view.value.textContent = token;
    view.qr.src = await window.homePanel.keyQr(token);
    view.reveal.hidden = false;
    view.open = true;
    if (button) button.textContent = kind === "ha" ? "Hide key + QR" : "Hide token + QR";
  } catch (error) {
    view.token = "";
    view.value.textContent = error?.message || "The key could not be read.";
    view.qr.removeAttribute("src");
    view.reveal.hidden = false;
  } finally {
    if (button) button.disabled = kind === "ha" && !form.elements.namedItem("esphomeEncryptionEnabled")?.checked;
  }
}

for (const button of document.querySelectorAll("[data-secret-toggle]")) {
  button.addEventListener("click", () => toggleSecret(button.dataset.secretToggle));
}
for (const button of document.querySelectorAll("[data-copy-secret]")) {
  button.addEventListener("click", async () => {
    const token = secretViews[button.dataset.copySecret]?.token;
    if (token) await window.homePanel.copySecret(token);
  });
}

async function makeEncryptionKey() {
  const key = await window.homePanel.newEncryptionKey();
  const field = form.elements.namedItem("esphomeEncryptionKey");
  if (field) field.value = key;
  return key;
}

function populate(settings) {
  loadedSettings = settings;
  const hostname = document.querySelector("#device-hostname");
  if (hostname) hostname.textContent = settings.deviceName || "Unknown host";
  const cameraPasswordStatus = document.querySelector("#camera-password-status");
  if (cameraPasswordStatus) cameraPasswordStatus.textContent = settings.cameraPasswordConfigured ? "saved · hidden" : "not set";
  const cameraUrl = document.querySelector("#camera-stream-url");
  if (cameraUrl) cameraUrl.textContent = `rtsp://${settings.deviceName || "DEVICE"}.local:8554/homepanel`;
  for (const [key, value] of Object.entries(settings)) {
    const input = form.elements.namedItem(key);
    if (input) {
      if (input.type === "checkbox") input.checked = Boolean(value);
      else input.value = value;
    }

    const voiceInput = voiceForm && voiceForm.elements.namedItem(key);
    if (voiceInput) {
      if (voiceInput.type === "checkbox") voiceInput.checked = Boolean(value);
      else voiceInput.value = value;
    }
  }
  const cameraPasswordField = form.elements.namedItem("cameraPassword");
  if (cameraPasswordField) cameraPasswordField.value = "";

  const enabledToggle = form.elements.namedItem("esphomeEncryptionEnabled");
  if (enabledToggle) enabledToggle.checked = Boolean(settings.esphomeEncryptionEnabled);
  const sendspinPairing = form.elements.namedItem("sendspinRequirePairing");
  if (sendspinPairing) sendspinPairing.checked = !Boolean(settings.musicUnpaired);
  syncEncryptionKeyField();
}

function setDrawer(open) {

  const wasOpen = drawer.classList.contains("open");
  drawer.classList.remove("dragging");
  drawer.style.transform = "";
  drawer.classList.toggle("open", open);
  drawer.setAttribute("aria-hidden", String(!open));
  drawerBackdrop.hidden = !open;
  syncDrawerLayout();
  if (wasOpen !== Boolean(open)) {
    window.homePanel.setSidePanel(Boolean(open));
  }
  if (open) window.homePanel.activity("keydown");
}

const drawerWidth = () => drawer.getBoundingClientRect().width || 330;

function dragDrawer(progress) {
  const width = drawerWidth();
  const clamped = Math.max(0, Math.min(1, progress));
  drawer.classList.add("dragging");
  drawer.style.transform = `translateX(${(1 - clamped) * width}px)`;
  return clamped;
}

function endDrawerDrag(progress) {
  drawer.classList.remove("dragging");
  drawer.style.transform = "";
  setDrawer(progress >= 0.5);
}

function openSettings() {
  panel.hidden = false;
  syncSettingsLayout();
  window.homePanel.setOverlayExpanded(true);
  window.homePanel.activity("keydown");
}

function renderKeyboard() {
  keyboard.replaceChildren();
  const header = document.createElement("div"); header.className = "keyboard-header";
  const title = document.createElement("span"); title.textContent = "On-screen keyboard";
  const close = document.createElement("button"); close.className = "keyboard-close"; close.type = "button"; close.textContent = "×"; close.setAttribute("aria-label", "Close keyboard");
  close.addEventListener("click", () => setKeyboard(false));
  header.append(title, close); keyboard.append(header);
  const rows = symbolsOn ? ["1234567890", "@#$%&*-+()", "!?:;'/.,"] : ["qwertyuiop", "asdfghjkl", "zxcvbnm"];
  for (const row of rows) {
    const el = document.createElement("div"); el.className = "key-row";
    for (const char of row) {
      const button = document.createElement("button"); button.className = "key"; button.textContent = shiftOn && !symbolsOn ? char.toUpperCase() : char;
      button.addEventListener("click", () => { const typed = shiftOn && !symbolsOn ? char.toUpperCase() : char; shiftOn = false; sendKey(typed); renderKeyboard(); });
      el.append(button);
    }
    keyboard.append(el);
  }
  const bottom = document.createElement("div"); bottom.className = "key-row key-row-bottom";
  const addKey = (label, value, className = "") => {
    const button = document.createElement("button"); button.className = `key ${className}`; button.textContent = label;
    button.addEventListener("click", () => sendKey(value)); bottom.append(button);
  };
  const shift = document.createElement("button"); shift.className = `key key-wide ${shiftOn ? "active" : ""}`; shift.textContent = "⇧"; shift.addEventListener("click", () => { shiftOn = !shiftOn; renderKeyboard(); }); bottom.append(shift);
  addKey("⌫", "BACKSPACE", "key-wide");
  const symbols = document.createElement("button"); symbols.className = "key key-wide"; symbols.textContent = symbolsOn ? "ABC" : "?123"; symbols.addEventListener("click", () => { symbolsOn = !symbolsOn; shiftOn = false; renderKeyboard(); }); bottom.append(symbols);
  addKey("space", "SPACE", "key-space"); addKey("↵", "ENTER", "key-wide key-enter");
  keyboard.append(bottom);

  for (const button of keyboard.querySelectorAll("button")) {
    button.addEventListener("mousedown", (event) => event.preventDefault());
  }
}

function setKeyboard(visible, { settings = false } = {}) {
  settingsKeyboardActive = settings && visible;
  keyboard.hidden = !visible;
  if (!visible) keyboardTarget = null;
  window.homePanel.setKeyboard(visible);
  if (visible) renderKeyboard();
  syncSettingsLayout();
  syncDrawerLayout();
  if (visible) { window.homePanel.activity("keydown"); }
}

function closeSettings() {
  panel.hidden = true;
  if (settingsKeyboardActive) setKeyboard(false);

  stopMicMonitor();
  hideSecret("ha");
  hideSecret("sendspin");
  window.homePanel.setOverlayExpanded(!$("#notice").hidden || !keyboard.hidden);
  syncSettingsLayout();
}

function stopMicMonitor() {
  const button = $("#mic-test-button");
  if (!button || button.getAttribute("aria-pressed") !== "true") return;
  button.setAttribute("aria-pressed", "false");
  button.textContent = "Test microphone";
  window.homePanel.monitorMic(false);
}

$("#close-settings").addEventListener("click", closeSettings);

const settingsTabs = [
  { tab: $("#tab-device"), view: $("#settings-view-device") },
  { tab: $("#tab-voice"), view: $("#settings-view-voice") },
];

function selectSettingsTab(index) {
  settingsTabs.forEach((entry, position) => {
    const selected = position === index;
    entry.tab.setAttribute("aria-selected", String(selected));
    entry.view.hidden = !selected;
  });
  syncSettingsLayout();
}

settingsTabs.forEach((entry, position) => {
  entry.tab.addEventListener("click", () => selectSettingsTab(position));
});
selectSettingsTab(0);

drawer.addEventListener("click", (event) => {
  const button = event.target.closest("button");
  if (!button) return;
  if (button.id === "exit-button") {

    setDrawer(false);
    window.homePanel.quit();
    return;
  }
  if (button.id === "display-mode-button") {
    setDrawer(false);
    window.homePanel.setDisplayMode(displayMode === "fullscreen" ? "window" : "fullscreen");
    return;
  }

  if (button.dataset.transport || button.dataset.source) return;

  if (button.closest(".media-panel")) return;

  if (button.closest(".quick-controls")) return;
  const action = button.dataset.action;
  if (action) window.homePanel.command({ action });
  else if (button.id === "settings-button") openSettings();
  setDrawer(false);
});
$("#notice").addEventListener("click", (event) => {
  if (event.target.closest("[data-action]")) window.homePanel.command({ action: "dismiss_notification" });
});

form.addEventListener("focusin", (event) => {
  const field = event.target;
  if (!field?.matches?.('input:not([type="checkbox"]):not([type="radio"]):not([type="range"]):not([type="button"]):not([type="submit"]), textarea')) return;

  keyboardTarget = field;

  if (!loadedSettings?.touchKeyboardEnabled) return;
  if (keyboard.hidden) setKeyboard(true, { settings: true });
  else settingsKeyboardActive = true;
});

form.addEventListener("focusout", (event) => {
  if (!settingsKeyboardActive) return;
  const next = event.relatedTarget;
  if (next && form.contains(next)) return;

  if (next && keyboard.contains(next)) return;
  setKeyboard(false);
});

const encryptionToggle = form.elements.namedItem("esphomeEncryptionEnabled");
if (encryptionToggle) {
  encryptionToggle.addEventListener("change", async () => {
    syncEncryptionKeyField();
    if (encryptionToggle.checked) {
      const field = form.elements.namedItem("esphomeEncryptionKey");
      if (field && !field.value) await makeEncryptionKey();
    }
  });
}

const regenerateKeyButton = document.querySelector("[data-action='new-encryption-key']");
if (regenerateKeyButton) {
  regenerateKeyButton.addEventListener("click", async (event) => {
    event.preventDefault();
    await makeEncryptionKey();
    hideSecret("ha");
  });
}

form.addEventListener("submit", async (event) => {
  event.preventDefault();
  const data = Object.fromEntries(new FormData(form));

  if (!data.cameraPassword) delete data.cameraPassword;

  data.esphomeEncryptionEnabled = form.elements.namedItem("esphomeEncryptionEnabled").checked;
  data.musicUnpaired = !form.elements.namedItem("sendspinRequirePairing").checked;
  if (!data.esphomeEncryptionEnabled) {
    data.esphomeEncryptionKey = "";
  }
  $("#form-error").textContent = "";
  try {
    const saved = await window.homePanel.saveSettings(data);
    populate(saved); closeSettings();
  } catch (error) { $("#form-error").textContent = error.message || "Could not save settings."; }
});

if (voiceForm) {
  voiceForm.addEventListener("submit", async (event) => {
    event.preventDefault();
    const data = Object.fromEntries(new FormData(voiceForm));

    for (const element of voiceForm.elements) {
      if (element.type === "checkbox") data[element.name] = element.checked;
    }
    $("#voice-error").textContent = "";
    try {
      const saved = await window.homePanel.saveSettings(data);
      populate(saved);
    } catch (error) {
      $("#voice-error").textContent = error.message || "Could not save the voice settings.";
    }
  });
}

const brightnessSlider = $("#brightness-slider");
const volumeSlider = $("#volume-slider");
const brightnessValue = $("#brightness-value");
const volumeValue = $("#volume-value");
const brightnessControl = $("#brightness-control");
const volumeControl = $("#volume-control");
const volumePopup = $("#volume-popup");
const volumePopupFill = $("#volume-popup-fill");
const volumePopupValue = $("#volume-popup-value");
const muteIndicator = $("#mute-indicator");

function paintSlider(slider, percent) {
  if (!slider) return;
  slider.value = String(percent);
  slider.style.setProperty("--fill", `${percent}%`);
}

function paintBrightness(value) {
  const percent = Math.max(0, Math.min(100, Math.round(value)));
  paintSlider(brightnessSlider, percent);
  if (brightnessValue) brightnessValue.textContent = `${percent}%`;
}

function paintVolume(value) {
  const percent = Math.max(0, Math.min(100, Math.round(value)));
  paintSlider(volumeSlider, percent);
  if (volumeValue) volumeValue.textContent = `${percent}%`;
}

function paintMute(muted) {
  if (!muteIndicator) return;
  const on = Boolean(muted);
  muteIndicator.hidden = !on;

  if (on) requestAnimationFrame(() => muteIndicator.classList.add("is-visible"));
  else muteIndicator.classList.remove("is-visible");
}

if (window.homePanel.onMuteIndicator) {
  window.homePanel.onMuteIndicator((on) => paintMute(on));
}

let volumePopupTimer = null;

let lastPopupVolume = null;

function showVolumePopup(value) {
  if (!volumePopup) return;
  const percent = Math.max(0, Math.min(100, Math.round(value)));
  if (volumePopupFill) volumePopupFill.style.width = `${percent}%`;
  if (volumePopupValue) volumePopupValue.textContent = String(percent);
  lastPopupVolume = percent;
  volumePopup.hidden = false;
  requestAnimationFrame(() => volumePopup.classList.add("is-visible"));
  clearTimeout(volumePopupTimer);

  volumePopupTimer = setTimeout(hideVolumePopup, 2200);
}

function hideVolumePopup() {
  if (!volumePopup) return;
  volumePopup.classList.remove("is-visible");
  clearTimeout(volumePopupTimer);

  volumePopupTimer = setTimeout(() => {
    if (!volumePopup.classList.contains("is-visible")) {
      volumePopup.hidden = true;
    }
  }, 220);
}

if (window.homePanel.onVolumePopup) {
  window.homePanel.onVolumePopup((payload) => {
    if (payload && typeof payload.percent === "number") showVolumePopup(payload.percent);
    else hideVolumePopup();
  });
}

function bindSlider(slider, onChange) {
  if (!slider) return;
  slider.addEventListener("input", () => {
    const value = Number(slider.value);
    slider.style.setProperty("--fill", `${value}%`);
  });
  slider.addEventListener("change", () => onChange(Number(slider.value)));
}

bindSlider(brightnessSlider, (value) => {
  paintBrightness(value);
  window.homePanel.setBrightness(value);
});

bindSlider(volumeSlider, (value) => {
  paintVolume(value);
  window.homePanel.setVolume(value);

  showVolumePopup(value);
});

const mediaPanel = $("#media-panel");
const mediaSourceServer = $("#media-source-server");
const mediaSourceHome = $("#media-source-home");
const mediaArtwork = $("#media-artwork");
const mediaTitle = $("#media-title");
const mediaArtist = $("#media-artist");
const mediaPosition = $("#media-position");
const mediaElapsed = $("#media-elapsed");
const mediaDuration = $("#media-duration");
const mediaPlay = $("#media-play");

const playIcon = '<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M8 5.5v13l11-6.5-11-6.5z" fill="currentColor" /></svg>';
const pauseIcon = '<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M8 5.5h3.2v13H8zM12.8 5.5H16v13h-3.2z" fill="currentColor" /></svg>';
const mediaPrevious = $("#media-previous");
const mediaNext = $("#media-next");

let media = {
  title: "",
  artist: "",
  artwork: "",
  position: 0,
  duration: 0,
  playing: false,
  commands: [],

  at: 0,
};

let mediaLoading = false;

const mediaLoadingTimeout = 8000;

function currentPosition() {
  if (!media.playing || !media.at) return media.position;
  return Math.min(media.duration || Number.MAX_SAFE_INTEGER, media.position + (Date.now() - media.at));
}

function repaintMedia() {
  if (!mediaPanel || mediaPanel.hidden) return;
  const position = currentPosition();
  const duration = media.duration;
  if (mediaElapsed) mediaElapsed.textContent = formatTime(position);
  if (mediaPosition && duration > 0 && document.activeElement !== mediaPosition) {
    const percent = Math.max(0, Math.min(1000, Math.round((position / duration) * 1000)));
    mediaPosition.value = String(percent);
    mediaPosition.style.setProperty("--fill", `${percent / 10}%`);
  }
}

function formatTime(ms) {
  if (!Number.isFinite(ms) || ms < 0) return "—";
  const total = Math.floor(ms / 1000);
  const minutes = Math.floor(total / 60);
  const seconds = total % 60;
  return `${minutes}:${String(seconds).padStart(2, "0")}`;
}

function setArtwork(url) {
  if (!mediaArtwork) return;
  if (!url) {
    mediaArtwork.hidden = true;
    mediaArtwork.removeAttribute("src");
    return;
  }
  if (mediaArtwork.getAttribute("src") === url) return;
  mediaArtwork.src = url;
  mediaArtwork.hidden = false;

  mediaArtwork.onerror = () => { mediaArtwork.hidden = true; };
}

let mediaSources = {
  server: { title: "", artist: "", artwork: "", position: 0, duration: 0, playing: false, commands: [] },
  home: { title: "", artist: "", artwork: "", position: 0, duration: 0, playing: false, commands: [] },
};

let lastMediaState = null;

let overlayWasUp = false;

let mediaSource = "server";

function paintMedia(state) {
  if (!mediaPanel) return;
  lastMediaState = state;

  mediaSources.server = {
    title: state.music_title || "",
    artist: state.music_artist || "",
    artwork: state.music_artwork || "",
    position: Number(state.music_position) || 0,
    duration: Number(state.music_duration) || 0,
    playing: Boolean(state.music_playing),
    commands: Array.isArray(state.music_transport) ? state.music_transport : [],
  };

  const mediaState = state.media_state || "idle";
  const homePlaying = mediaState === "playing" || mediaState === "announcing";

  if (mediaLoading && (mediaSources.server.playing || homePlaying)) {
    mediaLoading = false;
    if (mediaLoadingTimer) {
      clearTimeout(mediaLoadingTimer);
      mediaLoadingTimer = null;
    }
  }
  mediaSources.home = {
    title: homePlaying ? "Playing in Home Assistant" : "",
    artist: "",
    artwork: "",
    position: 0,
    duration: 0,
    playing: homePlaying,
    commands: ["play", "pause", "stop"],
  };

  const serverHas = Boolean(mediaSources.server.title || mediaSources.server.artist || mediaSources.server.artwork);
  const homeHas = mediaSources.home.playing;
  mediaPanel.hidden = !serverHas && !homeHas;
  if (mediaPanel.hidden) {
    setArtwork("");
    return;
  }

  if (mediaSource === "home" && !homeHas && serverHas) mediaSource = "server";
  if (mediaSource === "server" && !serverHas && homeHas) mediaSource = "home";

  const shown = mediaSources[mediaSource];

  if (mediaSourceServer) {
    mediaSourceServer.classList.toggle("is-selected", mediaSource === "server");
    mediaSourceServer.disabled = !serverHas;
  }
  if (mediaSourceHome) {
    mediaSourceHome.classList.toggle("is-selected", mediaSource === "home");
    mediaSourceHome.disabled = !homeHas;
  }

  if (mediaTitle) mediaTitle.textContent = shown.title || "Nothing playing";
  if (mediaArtist) mediaArtist.textContent = shown.artist;
  setArtwork(shown.artwork);

  const canSeek = shown.duration > 0 && shown.commands.includes("seek");
  if (mediaPosition) {
    mediaPosition.disabled = !canSeek;
  }

  media = { ...shown, at: Date.now() };
  repaintMedia();
  if (mediaDuration) mediaDuration.textContent = shown.duration > 0 ? formatTime(shown.duration) : "—";

  if (mediaPlay) {
    mediaPlay.disabled = !shown.commands.includes(shown.playing ? "pause" : "play");

    if (mediaLoading) {
      mediaPlay.textContent = "";
      mediaPlay.classList.add("is-loading");
      mediaPlay.setAttribute("aria-label", "Loading");
    } else {
      mediaPlay.classList.remove("is-loading");

      mediaPlay.innerHTML = shown.playing ? pauseIcon : playIcon;
      mediaPlay.setAttribute("aria-label", shown.playing ? "Pause" : "Play");
    }

    mediaPlay.dataset.transport = shown.playing ? "pause" : "play";
  }
  if (mediaPrevious) mediaPrevious.disabled = !shown.commands.includes("previous");
  if (mediaNext) mediaNext.disabled = !shown.commands.includes("next");
}

for (const button of document.querySelectorAll("[data-source]")) {
  button.addEventListener("click", () => {
    const source = button.dataset.source;
    if (!source) return;
    mediaSource = source;
    paintMedia(lastMediaState || {});
  });
}

setInterval(() => {
  if (media.playing) repaintMedia();
}, 500);

for (const button of document.querySelectorAll("[data-transport]")) {
  button.addEventListener("click", () => {
    const action = button.dataset.transport;
    if (!action) return;

    window.homePanel.transport({ action, source: mediaSource });

    if (action === "play") startMediaLoading();
  });
}

function startMediaLoading() {
  mediaLoading = true;
  paintMedia(lastMediaState || {});
  if (mediaLoadingTimer) clearTimeout(mediaLoadingTimer);
  mediaLoadingTimer = setTimeout(() => {
    mediaLoading = false;
    paintMedia(lastMediaState || {});
  }, mediaLoadingTimeout);
}

let mediaLoadingTimer = null;

if (mediaPosition) {
  mediaPosition.addEventListener("input", () => {
    const value = Number(mediaPosition.value);
    mediaPosition.style.setProperty("--fill", `${value / 10}%`);
  });
  mediaPosition.addEventListener("change", () => {
    const duration = media.duration;
    if (!duration) return;
    const position = Math.round((Number(mediaPosition.value) / 1000) * duration);
    window.homePanel.transport({ action: "seek", positionMs: position, source: mediaSource });
  });
}

const statusLabels = { online: "Online", connecting: "Connecting", offline: "Offline", unknown: "Unknown" };
window.homePanel.onState((state) => {

  document.body.classList.toggle("screen-off", Boolean(state.displayIsOff));
  $("#mode-label").textContent = state.displayIsOff ? "Screen off" : ({ dashboard: "Dashboard", slideshow: "Slideshow" }[state.mode] || state.mode);

  if (state.version) $("#version-label").textContent = `Home Panel - v${state.version}`;

  if (typeof state.brightness === "number") paintBrightness(state.brightness);
  if (typeof state.volume === "number") {
    paintVolume(state.volume);

    if (volumePopup && state.volume !== lastPopupVolume) {

      showVolumePopup(state.volume);
    } else if (volumePopup) {

      lastPopupVolume = state.volume;
    }
  }
  if (typeof state.muted === "boolean") paintMute(state.muted);

  if (brightnessControl) brightnessControl.hidden = typeof state.brightness !== "number";
  if (volumeControl) volumeControl.hidden = typeof state.volume !== "number";
  $("#dashboard-action-label").textContent = state.mode === "dashboard" ? "Dashboard" : "Exit to dashboard";
  $("#return-home").hidden = state.mode !== "url";
  $("#slideshow-action").hidden = !loadedSettings?.slideshowUrl;

  if (state.displayMode) {
    displayMode = state.displayMode;
    $("#display-mode-label").textContent = displayMode === "fullscreen" ? "Window mode" : "Fullscreen";
  }
  for (const service of ["control", "immich", "dashboard"]) {
    const value = state.serviceStatus?.[service] || "unknown";
    const led = document.querySelector(`[data-service="${service}"]`);
    led.dataset.status = value;
    $(`#${service}-state`).textContent = statusLabels[value] || "Unknown";
  }

  const controlState = $("#control-state");
  if (controlState) {
    const online = state.serviceStatus?.control === "online";
    const muted = state.muted === true;
    controlState.textContent = muted ? (online ? "Muted" : "Muted (offline)") : (statusLabels[state.serviceStatus?.control] || "Unknown");
  }

  if (!settingsKeyboardActive && Boolean(state.keyboardVisible) !== !keyboard.hidden) {
    keyboard.hidden = !state.keyboardVisible;
    if (state.keyboardVisible) renderKeyboard();
  }
  if (Boolean(state.sidePanelOpen) !== drawer.classList.contains("open")) {
    drawer.classList.toggle("open", Boolean(state.sidePanelOpen));
    drawer.setAttribute("aria-hidden", String(!state.sidePanelOpen));
    drawerBackdrop.hidden = !state.sidePanelOpen;
  }
  syncSettingsLayout();
  syncDrawerLayout();
});

$("#return-home").addEventListener("click", () => window.homePanel.command({ action: "dashboard" }));
drawerBackdrop.addEventListener("click", () => setDrawer(false));

function renderNotice(notice) {
  $("#notice-title").textContent = notice.title;
  $("#notice-message").textContent = notice.message;
  $("#notice").hidden = false;
  window.homePanel.setOverlayExpanded(true);
  clearTimeout(window.noticeTimer);
  window.noticeTimer = setTimeout(() => {
    $("#notice").hidden = true;
    window.homePanel.setOverlayExpanded(!panel.hidden || !keyboard.hidden);
  }, notice.duration);
}
window.homePanel.onNotification(renderNotice);
window.homePanel.onDismissNotification(() => {
  $("#notice").hidden = true;
  window.homePanel.setOverlayExpanded(!panel.hidden || !keyboard.hidden);
});

window.addEventListener("pointerdown", (event) => {
  if (!keyboard.hidden && !event.target.closest?.("#keyboard")) {
    const insideForm = !panel.hidden && Boolean(event.target.closest?.("#settings-form"));
    if (!insideForm) { setKeyboard(false); return; }
  }
  const drawerOpen = drawer.classList.contains("open");
  const onDrawer = Boolean(event.target.closest?.("#side-panel"));

  const onControl = Boolean(event.target.closest?.("input, textarea, select, button, [role='slider']"));
  touchStart = { x: event.clientX, y: event.clientY };
  if (onControl) {
    dragState = null;
  } else if (drawerOpen && onDrawer) {

    dragState = { start: 1, active: true };
  } else {
    dragState = null;
  }
  window.homePanel.activity("keydown");
}, { passive: true });
window.addEventListener("pointermove", (event) => {
  if (!dragState?.active) return;
  const dx = event.clientX - touchStart.x;
  const dy = Math.abs(event.clientY - touchStart.y);

  if (dy > Math.abs(dx)) { dragState.active = false; return; }

  if (!dragState.moved) {
    if (Math.abs(dx) < 6) return;
    dragState.moved = true;
  }
  dragDrawer(dragState.start - dx / drawerWidth());
}, { passive: true });
window.addEventListener("pointerup", (event) => {
  if (!touchStart) return;
  const dx = event.clientX - touchStart.x; const dy = Math.abs(event.clientY - touchStart.y);

  if (dragState?.active && dragState.moved && dy < 90) {
    const progress = dragState.start - dx / drawerWidth();
    dragState = null;
    endDrawerDrag(progress);
    touchStart = null;
    return;
  }
  dragState = null;
  touchStart = null;
}, { passive: true });
window.addEventListener("pointercancel", () => {
  if (!dragState?.active) return;
  const moved = Boolean(dragState.moved);
  dragState = null;
  touchStart = null;
  if (moved) endDrawerDrag(drawer.classList.contains("open") ? 1 : 0);
});
window.addEventListener("keydown", () => window.homePanel.activity("keydown"));
window.addEventListener("resize", () => {
  syncSettingsLayout();
});
window.visualViewport?.addEventListener("resize", syncSettingsLayout);
window.visualViewport?.addEventListener("scroll", syncSettingsLayout);

window.homePanel.getSettings().then((settings) => {
  loadedSettings = settings;
  populate(settings);
  if (!settings.configured) openSettings();
});

const micTestButton = $("#mic-test-button");
const speakerTestButton = $("#speaker-test-button");

if (voiceUI && window.homePanel.onDeviceState) {
  window.homePanel.onDeviceState((state) => {
    voiceUI.apply(state);
    populateAudioDevices(state);
    paintAudioMeters(state);
    paintMedia(state);

    const overlayUp = Boolean(voiceUI.overlay && !voiceUI.overlay.container.hidden);
    if (overlayUp !== overlayWasUp) {
      overlayWasUp = overlayUp;
      window.homePanel.voiceOverlay(overlayUp);
    }
    if (overlayUp) {
      if (drawer.classList.contains("open")) setDrawer(false);
      if (!keyboard.hidden) setKeyboard(false);
      if (!panel.hidden) closeSettings();
    }

    lastDeviceState = state;
    checkOverlay();
  });
}

const OVERLAY_WATCHDOG_MS = 250;

let lastDeviceState = null;
let overlayWatchdogTimer = null;

function overlayShouldBeUp(state) {
  if (!state || state.muted) return false;
  return Boolean(state.speaking || state.thinking || state.listening);
}

function checkOverlay() {
  if (!voiceUI || !voiceUI.overlay) return;
  const shouldBeUp = overlayShouldBeUp(lastDeviceState);
  const isUp = !voiceUI.overlay.container.hidden;
  if (!shouldBeUp || isUp) return;

  voiceUI.apply(lastDeviceState);
}

function startOverlayWatchdog() {
  if (overlayWatchdogTimer) return;
  overlayWatchdogTimer = setInterval(() => {
    if (!overlayShouldBeUp(lastDeviceState)) return;
    checkOverlay();
  }, OVERLAY_WATCHDOG_MS);
}

if (voiceUI) startOverlayWatchdog();

if (micTestButton) {
  micTestButton.addEventListener("click", async () => {

    const on = micTestButton.getAttribute("aria-pressed") !== "true";
    micTestButton.setAttribute("aria-pressed", String(on));
    micTestButton.textContent = on ? "Stop monitoring" : "Test microphone";
    await window.homePanel.monitorMic(on);
  });
}

function paintMicMonitor(state) {
  if (!micTestButton) return;
  const monitoring = Boolean(state.monitoring);
  micTestButton.setAttribute("aria-pressed", String(monitoring));
  micTestButton.textContent = monitoring ? "Stop monitoring" : "Test microphone";
}

if (speakerTestButton) {
  speakerTestButton.addEventListener("click", async () => {
    await window.homePanel.testSound();
  });
}

function paintAudioMeters(state) {
  const monitoring = Boolean(state.monitoring);
  const micMeter = $("#mic-meter");
  const micValue = $("#mic-level-value");
  if (micMeter) micMeter.hidden = !monitoring;
  if (micValue) micValue.hidden = !monitoring;
  if (monitoring) {
    setMeter($("#mic-meter-fill"), micValue, Number(state.level) || 0);
  }
  setMeter($("#speaker-meter-fill"), $("#speaker-level-value"), Number(state.volume) || 0);
  paintMicMonitor(state);
}

function setMeter(fill, value, level) {
  if (fill) fill.style.width = `${Math.max(0, Math.min(100, level))}%`;
  if (value) value.textContent = level > 0 ? `${level}%` : "—";
}

function populateAudioDevices(state) {
  fillDeviceSelect($("#voice-audio-input"), state.audio_inputs, loadedSettings?.voiceAudioInput, state.selected_audio_input);
  fillDeviceSelect($("#voice-audio-output"), state.audio_outputs, loadedSettings?.voiceAudioOutput, state.selected_audio_output);
}

function fillDeviceSelect(select, devices, chosen, selected) {
  if (!select) return;
  const list = Array.isArray(devices) ? devices : [];
  const current = select.value || chosen || "";

  const signature = list.map((d) => d.name).join("|");
  if (select.dataset.signature === signature) {
    if (current && select.value !== current) select.value = current;
    return;
  }
  select.dataset.signature = signature;

  select.replaceChildren();
  const blank = document.createElement("option");
  blank.value = "";
  blank.textContent = "System default";
  select.append(blank);

  for (const device of list) {
    const option = document.createElement("option");
    option.value = device.name;
    option.textContent = device.description || device.name;
    select.append(option);
  }

  const wanted = current || selected || "";
  if (wanted && list.some((d) => d.name === wanted)) {
    select.value = wanted;
  } else if (selected && list.some((d) => d.name === selected)) {
    select.value = selected;
  }
}
