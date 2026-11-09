function safeUrl(raw) {
  try {
    const url = new URL(String(raw));
    return ["http:", "https:"].includes(url.protocol) ? url.toString() : null;
  } catch {
    return null;
  }
}

const NAMED_KEYS = new Set(["BACKSPACE", "SPACE", "ENTER", "SHIFT", "TAB", "LEFT", "RIGHT"]);

// Applies an on-screen keyboard key to a text-like field descriptor and
// returns the new value plus caret position. Pure so it can be unit tested
// without a DOM, and safe to pass across the renderer bridge.
function editFieldValue(field, key) {
  const current = typeof field?.value === "string" ? field.value : "";
  const type = field?.type || "text";
  if (type === "checkbox" || type === "radio") return null;
  if (key === "BACKSPACE") {
    const start = caretStart(field, current);
    const end = Number.isInteger(field?.selectionEnd) ? field.selectionEnd : start;
    const from = start === end ? Math.max(0, start - 1) : start;
    return { value: current.slice(0, from) + current.slice(end), caret: from };
  }
  if (key === "SPACE") return insertText(field, current, " ");
  if (NAMED_KEYS.has(key)) return null;
  if (typeof key !== "string" || !key) return null;
  if (type === "number" && !/^\d+$/.test(key)) return { value: current, caret: caretStart(field, current), rejected: true };
  return insertText(field, current, key);
}

function caretStart(field, current) {
  if (Number.isInteger(field?.selectionStart)) return field.selectionStart;
  return current.length;
}

function insertText(field, current, text) {
  const start = caretStart(field, current);
  const end = Number.isInteger(field?.selectionEnd) ? field.selectionEnd : start;
  return { value: current.slice(0, start) + text + current.slice(end), caret: start + text.length };
}

// Reads a number from the settings, or nothing when it was not set.
//
// The distinction matters. A setting that was never chosen has to be left
// out of the configuration, so the device keeps what it had. A setting that
// was chosen as zero has to be sent as zero. A fallback of zero would turn
// "not set" into "off", and a fallback of any other value would overwrite a
// real choice with a default nobody asked for.
function settingNumber(value) {
  if (value === undefined || value === null || value === "") return undefined;
  const parsed = Number(value);
  return Number.isFinite(parsed) ? parsed : undefined;
}

// Reads a checkbox from the settings, or nothing when it was not set.
//
// A checkbox that was never touched is not the same as one that was
// unticked: the first means "leave it as it is" and the second means "off".
function settingBoolean(value) {
  return typeof value === "boolean" ? value : undefined;
}

// The display modes the device can be in.
const DISPLAY_MODES = ["fullscreen", "window"];

// Reads the display mode from a value, falling back to fullscreen.
//
// A device that was set up before the setting existed has no value for it,
// and fullscreen is what it was running in. A value that is not a mode at
// all is treated the same way, because a device in a mode nobody knows
// about is a device that cannot be reasoned about.
function displayMode(value) {
  return DISPLAY_MODES.includes(value) ? value : "fullscreen";
}

// The bounds of the window in windowed mode.
//
// A window that fills the screen is a fullscreen window with a title bar, so
// the window is a fraction of the screen rather than all of it. The floor
// keeps it usable: a window too small for the dashboard is not a window
// mode, it is a bug.
//
// The window is centred on the work area, which is the screen without the
// panels a desktop has, so the title bar is not hidden under one.
function windowBounds(bounds) {
  const width = Math.max(900, Math.min(1440, Math.round(bounds.width * 0.78)));
  const height = Math.max(600, Math.min(900, Math.round(bounds.height * 0.78)));
  return {
    x: Math.round(bounds.x + (bounds.width - width) / 2),
    y: Math.round(bounds.y + (bounds.height - height) / 2),
    width,
    height,
  };
}

module.exports = { safeUrl, editFieldValue, settingNumber, settingBoolean, displayMode, windowBounds, DISPLAY_MODES };
