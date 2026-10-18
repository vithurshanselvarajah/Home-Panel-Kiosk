const $ = (selector) => document.querySelector(selector);
const panel = $("#settings-panel");
const drawer = $("#side-panel");
const drawerBackdrop = $("#side-panel-backdrop");
const keyboard = $("#keyboard");
const form = $("#settings-form");
let loadedSettings;
let activitySentAt = 0;
let touchStart;
let shiftOn = false;
let symbolsOn = false;

function populate(settings) {
  loadedSettings = settings;
  for (const [key, value] of Object.entries(settings)) {
    const input = form.elements.namedItem(key);
    if (input) {
      if (input.type === "checkbox") input.checked = Boolean(value);
      else input.value = value;
    }
  }
  if (settings.mqttPassword === "••••••••") form.elements.namedItem("mqttPassword").placeholder = "Saved securely · blank keeps current";
}

function setDrawer(open) {
  drawer.classList.toggle("open", open);
  drawer.setAttribute("aria-hidden", String(!open));
  drawerBackdrop.hidden = !open;
  window.homePanel.setSidePanel(open);
  if (open) window.homePanel.activity("keydown");
}

function openSettings() {
  panel.hidden = false;
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
      button.addEventListener("click", () => { window.homePanel.keyboardKey(shiftOn && !symbolsOn ? char.toUpperCase() : char); shiftOn = false; renderKeyboard(); });
      el.append(button);
    }
    keyboard.append(el);
  }
  const bottom = document.createElement("div"); bottom.className = "key-row key-row-bottom";
  const addKey = (label, value, className = "") => {
    const button = document.createElement("button"); button.className = `key ${className}`; button.textContent = label;
    button.addEventListener("click", () => window.homePanel.keyboardKey(value)); bottom.append(button);
  };
  const shift = document.createElement("button"); shift.className = `key key-wide ${shiftOn ? "active" : ""}`; shift.textContent = "⇧"; shift.addEventListener("click", () => { shiftOn = !shiftOn; renderKeyboard(); }); bottom.append(shift);
  addKey("⌫", "BACKSPACE", "key-wide");
  const symbols = document.createElement("button"); symbols.className = "key key-wide"; symbols.textContent = symbolsOn ? "ABC" : "?123"; symbols.addEventListener("click", () => { symbolsOn = !symbolsOn; shiftOn = false; renderKeyboard(); }); bottom.append(symbols);
  addKey("space", "SPACE", "key-space"); addKey("↵", "ENTER", "key-wide key-enter");
  keyboard.append(bottom);
}

function setKeyboard(visible) {
  keyboard.hidden = !visible;
  window.homePanel.setKeyboard(visible);
  if (visible) { renderKeyboard(); window.homePanel.activity("keydown"); }
}

$("#close-drawer").addEventListener("click", () => setDrawer(false));
$("#settings-button").addEventListener("click", openSettings);
$("#close-settings").addEventListener("click", () => {
  panel.hidden = true;
  window.homePanel.setOverlayExpanded(!$("#notice").hidden || !keyboard.hidden);
});
drawer.addEventListener("click", (event) => {
  const action = event.target.closest("[data-action]")?.dataset.action;
  if (action) { window.homePanel.command({ action }); setDrawer(false); }
});
$("#notice").addEventListener("click", (event) => {
  if (event.target.closest("[data-action]")) window.homePanel.command({ action: "dismiss_notification" });
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
    populate(saved); panel.hidden = true;
    window.homePanel.setOverlayExpanded(!$("#notice").hidden || !keyboard.hidden);
  } catch (error) { $("#form-error").textContent = error.message || "Could not save settings."; }
});

const statusLabels = { online: "Online", connecting: "Connecting", offline: "Offline", unknown: "Unknown" };
window.homePanel.onState((state) => {
  $("#mode-label").textContent = state.displayIsOff ? "Screen off" : ({ dashboard: "Dashboard", slideshow: "Slideshow", camera: "Camera" }[state.mode] || state.mode);
  $("#dashboard-action-label").textContent = state.mode === "dashboard" ? "Dashboard" : "Exit to dashboard";
  $("#return-home").hidden = !["url", "camera"].includes(state.mode);
  $("#slideshow-action").hidden = !loadedSettings?.slideshowUrl;
  for (const service of ["mqtt", "immich", "dashboard"]) {
    const value = state.serviceStatus?.[service] || "unknown";
    const led = document.querySelector(`[data-service="${service}"]`);
    led.dataset.status = value;
    $(`#${service}-state`).textContent = statusLabels[value] || "Unknown";
  }
  if (Boolean(state.keyboardVisible) !== !keyboard.hidden) { keyboard.hidden = !state.keyboardVisible; if (state.keyboardVisible) renderKeyboard(); }
  if (Boolean(state.sidePanelOpen) !== drawer.classList.contains("open")) {
    drawer.classList.toggle("open", Boolean(state.sidePanelOpen));
    drawer.setAttribute("aria-hidden", String(!state.sidePanelOpen));
    drawerBackdrop.hidden = !state.sidePanelOpen;
  }
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
  if (!keyboard.hidden && !event.target.closest?.("#keyboard")) { setKeyboard(false); return; }
  touchStart = { x: event.clientX, y: event.clientY };
  window.homePanel.activity("keydown");
}, { passive: true });
window.addEventListener("pointerup", (event) => {
  if (!touchStart) return;
  const dx = event.clientX - touchStart.x; const dy = Math.abs(event.clientY - touchStart.y);
  if (dy < 90 && !drawer.classList.contains("open") && touchStart.x > window.innerWidth - 45 && dx < -55) setDrawer(true);
  else if (dy < 90 && drawer.classList.contains("open") && dx > 65) setDrawer(false);
  touchStart = null;
}, { passive: true });
window.addEventListener("keydown", () => window.homePanel.activity("keydown"));
window.addEventListener("pointermove", () => {
  if (Date.now() - activitySentAt > 5000) { activitySentAt = Date.now(); window.homePanel.activity("pointermove"); }
}, { passive: true });

window.homePanel.getSettings().then((settings) => {
  loadedSettings = settings;
  populate(settings);
  if (!settings.configured) openSettings();
});
