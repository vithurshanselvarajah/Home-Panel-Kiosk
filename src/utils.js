function safeUrl(raw) {
  try {
    const url = new URL(String(raw));
    return ["http:", "https:"].includes(url.protocol) ? url.toString() : null;
  } catch {
    return null;
  }
}

const NAMED_KEYS = new Set(["BACKSPACE", "SPACE", "ENTER", "SHIFT", "TAB", "LEFT", "RIGHT"]);

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

function settingNumber(value) {
  if (value === undefined || value === null || value === "") return undefined;
  const parsed = Number(value);
  return Number.isFinite(parsed) ? parsed : undefined;
}

function settingBoolean(value) {
  return typeof value === "boolean" ? value : undefined;
}

const DISPLAY_MODES = ["fullscreen", "window"];

function displayMode(value) {
  return DISPLAY_MODES.includes(value) ? value : "fullscreen";
}

function hostIdentity(value) {
  const hostname = String(value || "").trim();
  return hostname || "home-panel";
}

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

module.exports = { safeUrl, editFieldValue, settingNumber, settingBoolean, displayMode, windowBounds, hostIdentity, DISPLAY_MODES };
