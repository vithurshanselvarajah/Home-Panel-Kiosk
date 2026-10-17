const { ipcRenderer } = require("electron");

// Report only whether HA currently has a text editor focused. No kiosk command
// API is exposed to the remote page.
let focusedEditable = false;
let focusCheckQueued = false;
const observedRoots = new WeakSet();

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
  const next = isEditableElement(active) || active?.getAttribute?.("role") === "textbox";
  if (next === focusedEditable) return;
  focusedEditable = next;
  ipcRenderer.send("remote:text-input", focusedEditable);
}

function queueFocusCheck() {
  if (focusCheckQueued) return;
  focusCheckQueued = true;
  setTimeout(checkFocus, 0);
}

function discoverElement(element) {
  if (!element || element.nodeType !== 1) return;
  if (element.shadowRoot) inspectTree(element.shadowRoot);
  for (const child of element.querySelectorAll?.("*") || []) {
    if (child.shadowRoot) inspectTree(child.shadowRoot);
  }
}

function inspectTree(root) {
  if (observedRoots.has(root)) return;
  observedRoots.add(root);
  root.addEventListener("focusin", queueFocusCheck, true);
  root.addEventListener("focusout", queueFocusCheck, true);
  for (const element of root.querySelectorAll?.("*") || []) {
    if (element.shadowRoot) inspectTree(element.shadowRoot);
  }
  new MutationObserver((mutations) => {
    for (const mutation of mutations) {
      for (const node of mutation.addedNodes) discoverElement(node);
    }
  }).observe(root, { childList: true, subtree: true });
}

inspectTree(document);

let edgeSwipeStart;
document.addEventListener("pointerdown", (event) => {
  edgeSwipeStart = { x: event.clientX, y: event.clientY };
}, true);
document.addEventListener("pointerup", (event) => {
  if (!edgeSwipeStart) return;
  const dx = event.clientX - edgeSwipeStart.x;
  const dy = Math.abs(event.clientY - edgeSwipeStart.y);
  const edge = document.documentElement.clientWidth - 45;
  if (edgeSwipeStart.x > edge && dx < -55 && dy < 90) ipcRenderer.send("remote:open-side-panel");
  edgeSwipeStart = undefined;
}, true);
document.addEventListener("pointercancel", () => { edgeSwipeStart = undefined; }, true);
