function safeUrl(raw) {
  try {
    const url = new URL(String(raw));
    return ["http:", "https:"].includes(url.protocol) ? url.toString() : null;
  } catch {
    return null;
  }
}

function cameraObjectId(name) {
  return `camera_${String(name).toLowerCase().replace(/[^a-z0-9]+/g, "_").replace(/^_|_$/g, "").slice(0, 48)}`;
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

module.exports = { safeUrl, cameraObjectId, editFieldValue };
