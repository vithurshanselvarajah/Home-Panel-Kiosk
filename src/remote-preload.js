const { ipcRenderer } = require("electron");

// Report only whether HA currently has a text editor focused. No kiosk command
// API is exposed to the remote page.
//
// focusin is a composed event, so a single capture listener on the document
// sees focus changes from any shadow root at any depth. That avoids walking
// the Home Assistant DOM to discover shadow hosts, which was expensive because
// the dashboard re-renders on every state change.
let focusCheckQueued = false;

function isEditableElement(node) {
  if (!node || node.nodeType !== 1) return false;
  const tag = node.tagName?.toLowerCase();
  if (tag === "textarea") return true;
  if (tag === "input") return !["button", "submit", "reset", "checkbox", "radio", "range", "color", "file", "image"].includes((node.type || "text").toLowerCase());
  if (["ha-textfield", "ha-form-string", "ha-form-multiline", "ha-code-editor", "ha-date-input", "ha-time-input", "ha-input-text", "ha-input-number"].includes(tag)) return true;
  return node.isContentEditable || node.getAttribute?.("role") === "textbox";
}

function deepestActiveElement(root = document) {
  let active = root.activeElement;
  while (active?.shadowRoot?.activeElement) active = active.shadowRoot.activeElement;
  return active;
}

function checkFocus() {
  focusCheckQueued = false;
  const active = deepestActiveElement();
  const focused = isEditableElement(active) || active?.getAttribute?.("role") === "textbox";
  ipcRenderer.send("remote:text-input", focused);
}

function queueFocusCheck() {
  if (focusCheckQueued) return;
  focusCheckQueued = true;
  setTimeout(checkFocus, 0);
}

document.addEventListener("focusin", queueFocusCheck, true);
document.addEventListener("focusout", queueFocusCheck, true);
