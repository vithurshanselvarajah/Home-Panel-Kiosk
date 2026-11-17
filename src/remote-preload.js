const { ipcRenderer } = require("electron");

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
