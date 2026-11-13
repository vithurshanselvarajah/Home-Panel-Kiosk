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
  // The drawer has one row for the device's link to Home Assistant, so
  // there is no separate row for the voice assistant: it is the same
  // link and it says the same thing.
  //
  // The settings page has its own, because that is where the voice
  // assistant's settings are and a person looking at them wants to
  // know the assistant is there.
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
  if (!panel || panel.hidden) return;
  panel.style.bottom = `${keyboardOffset()}px`;
  syncDrawerLayout();
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

// syncEncryptionKeyField enables or disables the encryption key field.
//
// The key is only wanted when the connection is encrypted, so the field
// is off when it is not. The key itself is shown: it is a setting that
// is read and copied into Home Assistant, not a password that is typed.
function syncEncryptionKeyField() {
  const key = form.elements.namedItem("esphomeEncryptionKey");
  const enabled = form.elements.namedItem("esphomeEncryptionEnabled");
  if (!key || !enabled) return;
  key.disabled = !enabled.checked;
}

// makeEncryptionKey puts a new key in the field.
//
// The key is made by the application, which is where the random source
// is, so it is made properly rather than in the page.
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
  for (const [key, value] of Object.entries(settings)) {
    const input = form.elements.namedItem(key);
    if (input) {
      if (input.type === "checkbox") input.checked = Boolean(value);
      else input.value = value;
    }
    // The voice settings live in their own form.
    const voiceInput = voiceForm && voiceForm.elements.namedItem(key);
    if (voiceInput) {
      if (voiceInput.type === "checkbox") voiceInput.checked = Boolean(value);
      else voiceInput.value = value;
    }
  }
  // The encryption key is shown, because it is a setting that is read
  // and copied rather than a password that is typed. The toggle says
  // whether it is wanted at all.
  const enabledToggle = form.elements.namedItem("esphomeEncryptionEnabled");
  if (enabledToggle) enabledToggle.checked = Boolean(settings.esphomeEncryptionEnabled);
  syncEncryptionKeyField();
}

function setDrawer(open) {
  // The drawer is only told about a change. A button press in the open
  // drawer must not tell the main process that the drawer is open,
  // because that resizes the overlay view and the drawer jumps.
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
  // The microphone is released when the settings page closes, because a
  // microphone left open holds the sound card for a dashboard that is
  // showing the weather.
  stopMicMonitor();
  window.homePanel.setOverlayExpanded(!$("#notice").hidden || !keyboard.hidden);
  syncSettingsLayout();
}

// stopMicMonitor stops the microphone level meter.
function stopMicMonitor() {
  const button = $("#mic-test-button");
  if (!button || button.getAttribute("aria-pressed") !== "true") return;
  button.setAttribute("aria-pressed", "false");
  button.textContent = "Test microphone";
  window.homePanel.monitorMic(false);
}

$("#close-settings").addEventListener("click", closeSettings);

// Settings tabs. The voice tab is a separate view in the same panel so
// the device setup form is not buried under it.
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
  // The media panel's controls are theirs: the drawer stays open while
  // the music is being driven, because closing it on every press makes
  // the music impossible to control.
  //
  // The sliders do not come here at all — they are inputs rather than
  // buttons — so the volume and the brightness have always left the
  // drawer open. The media panel has buttons, so it has to say that its
  // buttons are not drawer actions.
  if (button.dataset.transport || button.dataset.source) return;
  // A control inside the media panel is not a drawer action, whatever
  // it is.
  if (button.closest(".media-panel")) return;
  // Nor is a control inside the quick controls. The sliders are
  // inputs rather than buttons, so the handler does not see them as
  // buttons at all, but the mute is a button and it would close the
  // drawer.
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

// The toggle decides whether the key is wanted.
//
// Turning it on makes a key, so the panel is encrypted from the first
// moment rather than being left in plain text until somebody thinks of
// one.
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

// The button makes a new key.
const regenerateKeyButton = document.querySelector("[data-action='new-encryption-key']");
if (regenerateKeyButton) {
  regenerateKeyButton.addEventListener("click", async (event) => {
    event.preventDefault();
    await makeEncryptionKey();
  });
}

form.addEventListener("submit", async (event) => {
  event.preventDefault();
  const data = Object.fromEntries(new FormData(form));
  data.idleToSlideshowMinutes = Number(data.idleToSlideshowMinutes);
  data.slideshowToOffMinutes = Number(data.slideshowToOffMinutes);
  data.touchKeyboardEnabled = form.elements.namedItem("touchKeyboardEnabled").checked;
  // The key is only wanted when the connection is encrypted. The toggle
  // says so, and the key follows it: no key when the connection is in
  // plain text.
  data.esphomeEncryptionEnabled = form.elements.namedItem("esphomeEncryptionEnabled").checked;
  if (!data.esphomeEncryptionEnabled) {
    data.esphomeEncryptionKey = "";
  }
  $("#form-error").textContent = "";
  try {
    const saved = await window.homePanel.saveSettings(data);
    populate(saved); closeSettings();
  } catch (error) { $("#form-error").textContent = error.message || "Could not save settings."; }
});

// The voice settings are saved with the rest of the device settings, so
// there is one place the configuration lives.
if (voiceForm) {
  voiceForm.addEventListener("submit", async (event) => {
    event.preventDefault();
    const data = Object.fromEntries(new FormData(voiceForm));
    // A form only submits the checkboxes that are ticked, so unticking one
    // would leave the old value behind. The state of every checkbox is
    // sent instead, so a tick can be undone.
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

// ── The quick controls ────────────────────────────────────────────────
//
// Brightness and volume are the two things a person reaches for most
// often, so they are in the drawer rather than in the settings.

const brightnessSlider = $("#brightness-slider");
const volumeSlider = $("#volume-slider");
const micMute = $("#mic-mute");
const brightnessValue = $("#brightness-value");
const volumeValue = $("#volume-value");
const brightnessControl = $("#brightness-control");
const volumeControl = $("#volume-control");

// paintSlider draws a slider at a value.
//
// The filled part of the track is a gradient that stops where the thumb
// is, and the gradient is set from the value, so it moves with the thumb.
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

// paintMute shows whether the microphone is muted.
//
// The microphone and not the speaker: a person who mutes the panel
// wants the room to stop hearing them, and they still want to hear the
// music.
//
// The button is green when the microphone is open and red when it is
// muted, so the state is seen rather than read.
function paintMute(muted) {
  if (!micMute) return;
  const on = Boolean(muted);
  micMute.classList.toggle("is-muted", on);
  micMute.setAttribute("aria-pressed", String(on));
  micMute.setAttribute("aria-label", on ? "Unmute the microphone" : "Mute the microphone");
}

// The slider is set on release rather than on every movement, because a
// backlight is written to for every value and a drag would write it
// dozens of times.
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
});

// The mute button mutes the microphone, or unmutes it.
//
// The microphone and not the speaker: a person who mutes the panel
// wants the room to stop hearing them, and they still want to hear the
// music.
if (micMute) {
  micMute.addEventListener("click", () => {
    const muted = micMute.getAttribute("aria-pressed") !== "true";
    window.homePanel.setMute(muted);
  });
}

// ── The music ─────────────────────────────────────────────────────────────
//
// What is playing, and the buttons for it.
//
// The transport belongs to the music server and the panel has the
// buttons, so a press is passed to the device program, which passes it
// on. The server decides which commands it will take, and says so, and
// a control it does not support is not offered: a button that does
// nothing is worse than no button.

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
const mediaPrevious = $("#media-previous");
const mediaNext = $("#media-next");

// What is playing, as the last state said.
//
// It is kept so the scrubber can be dragged without the state arriving
// again for every pixel, and so a state that has not changed does not
// redraw the panel.
let media = {
  title: "",
  artist: "",
  artwork: "",
  position: 0,
  duration: 0,
  playing: false,
  commands: [],
  // at is when the position was last heard from the server.
  at: 0,
};

// mediaLoading is whether something is on its way.
//
// Pressing play does not start the music there and then: the command
// goes to the server, the server gets the music, and the music starts.
// That takes a moment, and a button that does nothing when it is
// pressed looks broken.
//
// So the button waits and says that it is waiting. The wait ends when
// the state says what is playing, and it ends by itself if the state
// never comes, so a button cannot be left spinning for ever.
let mediaLoading = false;

// mediaLoadingTimer is how long the wait is allowed to last.
const mediaLoadingTimeout = 8000;

// The position the panel shows.
//
// The server says where the track has got to, but only when it says so,
// so the position is carried on from there. A scrubber that jumps from
// one update to the next is a scrubber that does not look like music.
function currentPosition() {
  if (!media.playing || !media.at) return media.position;
  return Math.min(media.duration || Number.MAX_SAFE_INTEGER, media.position + (Date.now() - media.at));
}

// repaintMedia draws the position and the scrubber.
//
// It is called on a timer while the music is playing, because the
// position moves on its own and the server does not say so often.
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

// formatTime is a length of time as minutes and seconds.
//
// A length that is not known is shown as a dash rather than as zero,
// because zero is a length and a dash is the absence of one.
function formatTime(ms) {
  if (!Number.isFinite(ms) || ms < 0) return "—";
  const total = Math.floor(ms / 1000);
  const minutes = Math.floor(total / 60);
  const seconds = total % 60;
  return `${minutes}:${String(seconds).padStart(2, "0")}`;
}

// setArtwork shows the album art, or the fallback when there is none.
//
// The picture is only fetched when it is a new one: the artwork does not
// change while a track plays, and fetching it on every state would pull
// the same picture over and over.
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
  // A picture that cannot be fetched leaves the fallback showing, which
  // is what the stylesheet does when the image fails to load.
  mediaArtwork.onerror = () => { mediaArtwork.hidden = true; };
}

// paintMedia draws what is playing.
// The two music sources, as the last state said.
//
// The music server knows what it is playing and the panel's own music
// does not, so the music server is the default and the other is a tap
// away.
let mediaSources = {
  server: { title: "", artist: "", artwork: "", position: 0, duration: 0, playing: false, commands: [] },
  home: { title: "", artist: "", artwork: "", position: 0, duration: 0, playing: false, commands: [] },
};

// The last state the device reported, so a change of source can be
// drawn without waiting for the next one.
let lastMediaState = null;

// Whether the wake word overlay was up the last time the device's state
// was looked at.
//
// The main process has to be told when the overlay comes and goes,
// because the view it is drawn in is sized by the main process.
let overlayWasUp = false;

// Which source the panel is showing.
//
// It is kept between states, so the person can look at the panel's own
// music while the music server is playing and it does not jump back.
let mediaSource = "server";

// paintMedia draws what is playing.
function paintMedia(state) {
  if (!mediaPanel) return;
  lastMediaState = state;

  // The music server's music.
  mediaSources.server = {
    title: state.music_title || "",
    artist: state.music_artist || "",
    artwork: state.music_artwork || "",
    position: Number(state.music_position) || 0,
    duration: Number(state.music_duration) || 0,
    playing: Boolean(state.music_playing),
    commands: Array.isArray(state.music_transport) ? state.music_transport : [],
  };

  // The music Home Assistant is playing.
  //
  // The panel does not know what it is: Home Assistant hands over a URL
  // and not the name of the track, so there is no title to show. What
  // it knows is whether something is playing.
  const mediaState = state.media_state || "idle";
  const homePlaying = mediaState === "playing" || mediaState === "announcing";
  // A music that has started is the end of the wait. The wait is also
  // ended by the timer, so a music that never starts does not leave the
  // button spinning.
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

  // The panel is only there when there is something to show. A drawer
  // with an empty music card in it is worse than no music card.
  const serverHas = Boolean(mediaSources.server.title || mediaSources.server.artist || mediaSources.server.artwork);
  const homeHas = mediaSources.home.playing;
  mediaPanel.hidden = !serverHas && !homeHas;
  if (mediaPanel.hidden) {
    setArtwork("");
    return;
  }

  // The music server's music takes priority, because it is the one that
  // knows what it is playing. The person can still ask for the other.
  if (mediaSource === "home" && !homeHas && serverHas) mediaSource = "server";
  if (mediaSource === "server" && !serverHas && homeHas) mediaSource = "home";

  const shown = mediaSources[mediaSource];

  // The source buttons say which is showing and which is available.
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

  // The scrubber is only usable when the track has a length and the
  // server will take a seek.
  const canSeek = shown.duration > 0 && shown.commands.includes("seek");
  if (mediaPosition) {
    mediaPosition.disabled = !canSeek;
  }

  media = { ...shown, at: Date.now() };
  repaintMedia();
  if (mediaDuration) mediaDuration.textContent = shown.duration > 0 ? formatTime(shown.duration) : "—";

  // The transport buttons are only there for the commands the server
  // says it will take.
  if (mediaPlay) {
    mediaPlay.disabled = !shown.commands.includes(shown.playing ? "pause" : "play");
    // The button shows what it will do, unless something is on its way
    // in which case it waits and says so.
    if (mediaLoading) {
      mediaPlay.textContent = "";
      mediaPlay.classList.add("is-loading");
      mediaPlay.setAttribute("aria-label", "Loading");
    } else {
      mediaPlay.classList.remove("is-loading");
      mediaPlay.textContent = shown.playing ? "⏸" : "▶";
      mediaPlay.setAttribute("aria-label", shown.playing ? "Pause" : "Play");
    }
    // The button has to send what it shows. A button that shows the
    // pause sign and sends "play" is a button that pauses nothing.
    mediaPlay.dataset.transport = shown.playing ? "pause" : "play";
  }
  if (mediaPrevious) mediaPrevious.disabled = !shown.commands.includes("previous");
  if (mediaNext) mediaNext.disabled = !shown.commands.includes("next");
}

// The source buttons choose which music is shown.
//
// The person chooses, and the choice is kept until there is no longer
// anything to show.
for (const button of document.querySelectorAll("[data-source]")) {
  button.addEventListener("click", () => {
    const source = button.dataset.source;
    if (!source) return;
    mediaSource = source;
    paintMedia(lastMediaState || {});
  });
}

// The position moves on its own while the music plays, so it is
// redrawn on a timer.
//
// The timer only runs while the music is playing: a drawer that is
// showing a paused track does not need to be redrawn.
setInterval(() => {
  if (media.playing) repaintMedia();
}, 500);

// The transport buttons. The panel does not decide what the commands
// are called on the wire: it says what the button means and the device
// program translates it.
for (const button of document.querySelectorAll("[data-transport]")) {
  button.addEventListener("click", () => {
    const action = button.dataset.transport;
    if (!action) return;
    // The command goes to whichever music is being shown, so the button
    // does what the panel says it does.
    window.homePanel.transport({ action, source: mediaSource });
    // Starting the music takes a moment, and the button shows that it
    // is waiting for it.
    if (action === "play") startMediaLoading();
  });
}

// startMediaLoading shows that the music is on its way.
//
// The wait ends when the state says the music is playing, and it ends
// by itself if that never happens, so a button cannot be left spinning
// for ever.
function startMediaLoading() {
  mediaLoading = true;
  paintMedia(lastMediaState || {});
  if (mediaLoadingTimer) clearTimeout(mediaLoadingTimer);
  mediaLoadingTimer = setTimeout(() => {
    mediaLoading = false;
    paintMedia(lastMediaState || {});
  }, mediaLoadingTimeout);
}

// mediaLoadingTimer is the wait that ends the spinner by itself.
let mediaLoadingTimer = null;

// The scrubber seeks when it is let go.
//
// The position is sent on release rather than on every movement, because
// a seek that is sent for every pixel would make the music jump.
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
  $("#mode-label").textContent = state.displayIsOff ? "Screen off" : ({ dashboard: "Dashboard", slideshow: "Slideshow" }[state.mode] || state.mode);
  // The version is at the bottom of the drawer. It is the thing a person
  // is asked for when something goes wrong.
  if (state.version) $("#version-label").textContent = `Home Panel - v${state.version}`;
  // The brightness and the volume are drawn from the state, so they are
  // right the moment the drawer opens.
  if (typeof state.brightness === "number") paintBrightness(state.brightness);
  if (typeof state.volume === "number") paintVolume(state.volume);
  if (typeof state.muted === "boolean") paintMute(state.muted);
  // A control that cannot change anything is hidden rather than shown at
  // a value that would do nothing.
  if (brightnessControl) brightnessControl.hidden = typeof state.brightness !== "number";
  if (volumeControl) volumeControl.hidden = typeof state.volume !== "number";
  $("#dashboard-action-label").textContent = state.mode === "dashboard" ? "Dashboard" : "Exit to dashboard";
  $("#return-home").hidden = state.mode !== "url";
  $("#slideshow-action").hidden = !loadedSettings?.slideshowUrl;
  // The button offers the mode the device is not in, so tapping it does
  // what the label says.
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
  // The ESPHome row says when the microphone is muted.
  //
  // The mute is the microphone's, and the microphone belongs to the
  // device, so it is the device's row that says so. A row that said
  // "online" while the microphone was muted would be saying the device
  // was working while it could not hear.
  const controlState = $("#control-state");
  if (controlState) {
    const online = state.serviceStatus?.control === "online";
    const muted = state.muted === true;
    controlState.textContent = muted ? (online ? "Muted" : "Muted (offline)") : (statusLabels[state.serviceStatus?.control] || "Unknown");
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
  // A control is not a drag of the drawer.
  //
  // The sliders are inside the drawer and near its edge, so a drag of
  // one was taken for a drag of the drawer: the drawer moved under the
  // pointer and then closed, and the volume was changed on the way.
  const onControl = Boolean(event.target.closest?.("input, textarea, select, button, [role='slider']"));
  touchStart = { x: event.clientX, y: event.clientY };
  if (onControl) {
    dragState = null;
  } else if (drawerOpen && onDrawer) {
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
  // The drag only starts once the pointer has moved far enough to be a
  // drag and not a press. A press on a button in the open drawer is not
  // a drag, and treating it as one moves the drawer and then puts it
  // back, which is the bouncing.
  if (!dragState.moved) {
    if (Math.abs(dx) < 6) return;
    dragState.moved = true;
  }
  dragDrawer(dragState.start - dx / drawerWidth());
}, { passive: true });
window.addEventListener("pointerup", (event) => {
  if (!touchStart) return;
  const dx = event.clientX - touchStart.x; const dy = Math.abs(event.clientY - touchStart.y);
  // A press that never moved is not a drag, so the drawer is left alone.
  // The button under the pointer still gets its click.
  if (dragState?.active && dragState.moved && dy < 90) {
    const progress = dragState.start - dx / drawerWidth();
    dragState = null;
    endDrawerDrag(progress);
    touchStart = null;
    return;
  }
  const wasDragging = Boolean(dragState?.moved);
  dragState = null;
  if (wasDragging) {
    touchStart = null;
    return;
  }
  // A swipe is a swipe of the screen, not of a control.
  //
  // The sliders are inside the drawer and near its edge, so a drag of
  // one was taken for a swipe: the drawer opened or closed under the
  // pointer and the value was changed on the way.
  const onControl = Boolean(event.target.closest?.("input, textarea, select, button, [role='slider']"));
  if (!onControl) {
    if (dy < 90 && !drawer.classList.contains("open") && touchStart.x > window.innerWidth - 45 && dx < -55) setDrawer(true);
    else if (dy < 90 && drawer.classList.contains("open") && dx > 65) setDrawer(false);
  }
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

// The microphone and speaker test controls.
//
// The microphone is monitored while the settings page is open, so a person
// can see what the microphone hears before they commit to it. The speaker
// is tested by a tone, because a person choosing a speaker needs to hear
// it to know which one it is.
//
// They are found before the voice state handler is wired, because the
// handler paints the button and the meter.
const micTestButton = $("#mic-test-button");
const speakerTestButton = $("#speaker-test-button");

// The voice state comes from the device program, which owns the link to
// controller and the audio. The main process pushes it whenever it changes.
if (voiceUI && window.homePanel.onDeviceState) {
  window.homePanel.onDeviceState((state) => {
    voiceUI.apply(state);
    populateAudioDevices(state);
    paintAudioMeters(state);
    paintMedia(state);
    // The overlay covers the screen, so anything that would sit on top
    // of it or behind it is put away: the drawer, the keyboard and the
    // settings page.
    //
    // The main process is told as well, because the view the overlay is
    // drawn in is sized by it, and the overlay needs the whole screen.
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
  });
}

if (micTestButton) {
  micTestButton.addEventListener("click", async () => {
    // The button is a toggle, because the microphone has to be released
    // again when the settings page is closed.
    const on = micTestButton.getAttribute("aria-pressed") !== "true";
    micTestButton.setAttribute("aria-pressed", String(on));
    micTestButton.textContent = on ? "Stop monitoring" : "Test microphone";
    await window.homePanel.monitorMic(on);
  });
}

// The button follows the device rather than the other way round.
//
// The device is what holds the microphone, so it is what knows whether
// the microphone is being monitored. A button that remembers what it was
// asked for says "Stop monitoring" when the device is not monitoring, and
// a person presses it and nothing happens.
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

// paintAudioMeters draws the levels.
//
// The microphone meter shows what the microphone is hearing, and the
// speaker meter shows the volume that was asked for, which is what the
// device will play at.
//
// The microphone meter is only shown while the microphone is being
// monitored. The device reports a level whenever it has one — the
// controller's capture has a level too — and a meter that moved without
// anybody listening to the microphone was answering a question nobody
// asked.
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

// Fills the microphone and speaker pickers with the devices this machine
// has.
//
// The list comes from the sidecar, because the sidecar is what opens the
// audio. The pickers keep whatever was chosen, so a device that was
// unplugged does not silently become the default.
function populateAudioDevices(state) {
  fillDeviceSelect($("#voice-audio-input"), state.audio_inputs, loadedSettings?.voiceAudioInput, state.selected_audio_input);
  fillDeviceSelect($("#voice-audio-output"), state.audio_outputs, loadedSettings?.voiceAudioOutput, state.selected_audio_output);
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
