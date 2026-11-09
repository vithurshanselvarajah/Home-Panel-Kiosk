const $ = (selector) => document.querySelector(selector);
const panel = $("#settings-panel");
const drawer = $("#side-panel");
const drawerBackdrop = $("#side-panel-backdrop");
const keyboard = $("#keyboard");
const form = $("#settings-form");
const echomuseForm = $("#echomuse-form");
const echomuseUI = window.EchoMuseUI ? window.EchoMuseUI.create({
  container: $("#voice-indicator"),
  dim: $("#voice-dim"),
  row: $("#echomuse-row"),
  state: $("#echomuse-state"),
  led: document.querySelector('[data-service="echomuse"]'),
  statusLed: document.querySelector('[data-service="echomuse-settings"]'),
  statusLabel: $("#echomuse-status-label"),
  statusReason: $("#echomuse-status-reason"),
}) : null;
let loadedSettings;
let touchStart;
let dragState;
let shiftOn = false;
let symbolsOn = false;
let settingsKeyboardActive = false;
// The mode the device is in, kept here so the drawer can label the button
// before the first state arrives.
let displayMode = "fullscreen";

// How much room the on-screen keyboard takes off the bottom of the view.
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
  // The indicator is not part of the settings panel, so it is lifted whether
  // or not the panel is open.
  syncWaveLayout();
  if (!panel || panel.hidden) return;
  panel.style.bottom = `${keyboardOffset()}px`;
  syncDrawerLayout();
}

// Keeps the voice indicator clear of the on-screen keyboard.
function syncWaveLayout() {
  if (!echomuseUI) return;
  echomuseUI.indicator.lift(keyboardOffset());
}

// Keeps the slide-in drawer clear of the return home bar and the keyboard.
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
  try { field.setSelectionRange(position, position); } catch { /* number inputs do not expose a selection */ }
}

// Types into the focused settings field when the panel is open, otherwise the
// keystroke belongs to the Home Assistant page behind the overlay.
function applyToSettingsField(value) {
  const field = document.activeElement;
  if (!field || !form.contains(field) || !("value" in field)) return false;
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

function sendKey(value) {
  if (panel.hidden) { window.homePanel.keyboardKey(value); return; }
  applyToSettingsField(value);
}

function populate(settings) {
  loadedSettings = settings;
  for (const [key, value] of Object.entries(settings)) {
    const input = form.elements.namedItem(key);
    if (input) {
      if (input.type === "checkbox") input.checked = Boolean(value);
      else input.value = value;
    }
    // The EchoMuse settings live in their own form.
    const echomuseInput = echomuseForm && echomuseForm.elements.namedItem(key);
    if (echomuseInput) {
      if (echomuseInput.type === "checkbox") echomuseInput.checked = Boolean(value);
      else echomuseInput.value = value;
    }
  }
  if (settings.mqttPassword === "••••••••") form.elements.namedItem("mqttPassword").placeholder = "Saved securely · blank keeps current";
}

function setDrawer(open) {
  drawer.classList.remove("dragging");
  drawer.style.transform = "";
  drawer.classList.toggle("open", open);
  drawer.setAttribute("aria-hidden", String(!open));
  drawerBackdrop.hidden = !open;
  syncDrawerLayout();
  window.homePanel.setSidePanel(open);
  if (open) window.homePanel.activity("keydown");
}

const drawerWidth = () => drawer.getBoundingClientRect().width || 330;

// Progress of the drawer between closed (0) and fully open (1). Following the
// finger lets it be pushed in or pulled out instead of only snapping between
// the two states.
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
}

function setKeyboard(visible, { settings = false } = {}) {
  settingsKeyboardActive = settings && visible;
  keyboard.hidden = !visible;
  window.homePanel.setKeyboard(visible);
  if (visible) renderKeyboard();
  syncSettingsLayout();
  syncDrawerLayout();
  if (visible) { window.homePanel.activity("keydown"); }
}

function closeSettings() {
  panel.hidden = true;
  if (settingsKeyboardActive) setKeyboard(false);
  window.homePanel.setOverlayExpanded(!$("#notice").hidden || !keyboard.hidden);
  syncSettingsLayout();
}

$("#close-settings").addEventListener("click", closeSettings);

// Settings tabs. The EchoMuse tab is a separate view in the same panel so
// the device setup form is not buried under it.
const settingsTabs = [
  { tab: $("#tab-device"), view: $("#settings-view-device") },
  { tab: $("#tab-echomuse"), view: $("#settings-view-echomuse") },
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
// Any button in the drawer dismisses it, so the panel never lingers over the
// view once an action has been picked.
drawer.addEventListener("click", (event) => {
  const button = event.target.closest("button");
  if (!button) return;
  if (button.id === "exit-button") {
    // The drawer is closed before the app goes, so the panel is not left
    // hanging over a window that is closing.
    setDrawer(false);
    window.homePanel.quit();
    return;
  }
  if (button.id === "display-mode-button") {
    setDrawer(false);
    window.homePanel.setDisplayMode(displayMode === "fullscreen" ? "window" : "fullscreen");
    return;
  }
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
  if (!field || !("value" in field) || field.type === "checkbox" || field.type === "radio") return;
  // The keyboard is only for the settings form, and only when the setting
  // says it should appear. The main process vetoes it as well, but the
  // keyboard is drawn here, so the check has to be here too.
  if (!loadedSettings?.touchKeyboardEnabled) return;
  if (keyboard.hidden) setKeyboard(true, { settings: true });
  else settingsKeyboardActive = true;
});

form.addEventListener("focusout", (event) => {
  if (!settingsKeyboardActive) return;
  const next = event.relatedTarget;
  if (next && form.contains(next)) return;
  setKeyboard(false);
});

form.addEventListener("submit", async (event) => {
  event.preventDefault();
  const data = Object.fromEntries(new FormData(form));
  data.idleToSlideshowMinutes = Number(data.idleToSlideshowMinutes);
  data.slideshowToOffMinutes = Number(data.slideshowToOffMinutes);
  data.touchKeyboardEnabled = form.elements.namedItem("touchKeyboardEnabled").checked;
  if (!data.mqttPassword && loadedSettings.mqttPassword === "••••••••") data.mqttPassword = loadedSettings.mqttPassword;
  $("#form-error").textContent = "";
  try {
    const saved = await window.homePanel.saveSettings(data);
    populate(saved); closeSettings();
  } catch (error) { $("#form-error").textContent = error.message || "Could not save settings."; }
});

// The EchoMuse settings are saved with the rest of the device settings, so
// there is one place the configuration lives.
if (echomuseForm) {
  echomuseForm.addEventListener("submit", async (event) => {
    event.preventDefault();
    const data = Object.fromEntries(new FormData(echomuseForm));
    // A form only submits the checkboxes that are ticked, so unticking one
    // would leave the old value behind. The state of every checkbox is
    // sent instead, so a tick can be undone.
    for (const element of echomuseForm.elements) {
      if (element.type === "checkbox") data[element.name] = element.checked;
    }
    $("#echomuse-error").textContent = "";
    try {
      const saved = await window.homePanel.saveSettings(data);
      populate(saved);
    } catch (error) {
      $("#echomuse-error").textContent = error.message || "Could not save the EchoMuse settings.";
    }
  });
}

const statusLabels = { online: "Online", connecting: "Connecting", offline: "Offline", unknown: "Unknown" };
window.homePanel.onState((state) => {
  $("#mode-label").textContent = state.displayIsOff ? "Screen off" : ({ dashboard: "Dashboard", slideshow: "Slideshow" }[state.mode] || state.mode);
  $("#dashboard-action-label").textContent = state.mode === "dashboard" ? "Dashboard" : "Exit to dashboard";
  $("#return-home").hidden = state.mode !== "url";
  $("#slideshow-action").hidden = !loadedSettings?.slideshowUrl;
  // The button offers the mode the device is not in, so tapping it does
  // what the label says.
  if (state.displayMode) {
    displayMode = state.displayMode;
    $("#display-mode-label").textContent = displayMode === "fullscreen" ? "Window mode" : "Fullscreen";
  }
  for (const service of ["mqtt", "immich", "dashboard"]) {
    const value = state.serviceStatus?.[service] || "unknown";
    const led = document.querySelector(`[data-service="${service}"]`);
    led.dataset.status = value;
    $(`#${service}-state`).textContent = statusLabels[value] || "Unknown";
  }
  // The keyboard follows what the main process says, because that is where
  // the setting is applied. A keyboard the main process has vetoed is
  // hidden here, so turning the setting off takes the keyboard away even
  // when it was already up.
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
  const inEdgeZone = event.clientX > window.innerWidth - 45;
  const onDrawer = Boolean(event.target.closest?.("#side-panel"));
  touchStart = { x: event.clientX, y: event.clientY };
  if (drawerOpen && onDrawer) {
    dragState = { start: 1, active: true };
  } else if (!drawerOpen && inEdgeZone) {
    dragState = { start: 0, active: true };
  } else {
    dragState = null;
  }
  window.homePanel.activity("keydown");
}, { passive: true });
window.addEventListener("pointermove", (event) => {
  if (!dragState?.active) return;
  const dx = event.clientX - touchStart.x;
  const dy = Math.abs(event.clientY - touchStart.y);
  // Let vertical scrolls inside the drawer win over the horizontal drag.
  if (dy > Math.abs(dx)) { dragState.active = false; return; }
  dragDrawer(dragState.start - dx / drawerWidth());
}, { passive: true });
window.addEventListener("pointerup", (event) => {
  if (!touchStart) return;
  const dx = event.clientX - touchStart.x; const dy = Math.abs(event.clientY - touchStart.y);
  if (dragState?.active && dy < 90) {
    const progress = dragState.start - dx / drawerWidth();
    dragState = null;
    endDrawerDrag(progress);
    touchStart = null;
    return;
  }
  dragState = null;
  if (dy < 90 && !drawer.classList.contains("open") && touchStart.x > window.innerWidth - 45 && dx < -55) setDrawer(true);
  else if (dy < 90 && drawer.classList.contains("open") && dx > 65) setDrawer(false);
  touchStart = null;
}, { passive: true });
window.addEventListener("pointercancel", () => {
  if (!dragState?.active) return;
  dragState = null;
  touchStart = null;
  endDrawerDrag(drawer.classList.contains("open") ? 1 : 0);
});
window.addEventListener("keydown", () => window.homePanel.activity("keydown"));
window.addEventListener("resize", () => {
  syncSettingsLayout();
  if (echomuseUI) echomuseUI.indicator.resize();
});
window.visualViewport?.addEventListener("resize", syncSettingsLayout);
window.visualViewport?.addEventListener("scroll", syncSettingsLayout);

window.homePanel.getSettings().then((settings) => {
  loadedSettings = settings;
  populate(settings);
  if (!settings.configured) openSettings();
});

// The EchoMuse state comes from the sidecar, which owns the link to the
// controller and the audio. The main process pushes it whenever it changes.
if (echomuseUI && window.homePanel.onEchoMuseState) {
  window.homePanel.onEchoMuseState((state) => {
    echomuseUI.apply(state);
    populateAudioDevices(state);
  });
}

// Fills the microphone and speaker pickers with the devices this machine
// has.
//
// The list comes from the sidecar, because the sidecar is what opens the
// audio. The pickers keep whatever was chosen, so a device that was
// unplugged does not silently become the default.
function populateAudioDevices(state) {
  fillDeviceSelect($("#echomuse-audio-input"), state.audio_inputs, loadedSettings?.echomuseAudioInput, state.selected_audio_input);
  fillDeviceSelect($("#echomuse-audio-output"), state.audio_outputs, loadedSettings?.echomuseAudioOutput, state.selected_audio_output);
}

function fillDeviceSelect(select, devices, chosen, selected) {
  if (!select) return;
  const list = Array.isArray(devices) ? devices : [];
  const current = select.value || chosen || "";
  // The list is rebuilt only when it changed, so the picker does not lose
  // its place while the state is pushed every few hundred milliseconds.
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

  // The chosen device is preferred over the one in use, because the chosen
  // one is the person's choice and the one in use may be the default.
  const wanted = current || selected || "";
  if (wanted && list.some((d) => d.name === wanted)) {
    select.value = wanted;
  } else if (selected && list.some((d) => d.name === selected)) {
    select.value = selected;
  }
}
syncWaveLayout();
