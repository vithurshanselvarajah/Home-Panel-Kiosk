const { contextBridge, ipcRenderer } = require("electron");

contextBridge.exposeInMainWorld("homePanel", {
  getSettings: () => ipcRenderer.invoke("app:get-settings"),
  saveSettings: (settings) => ipcRenderer.invoke("app:save-settings", settings),
  command: (command) => ipcRenderer.invoke("app:command", command),
  activity: () => ipcRenderer.send("app:activity"),
  setOverlayExpanded: (expanded) => ipcRenderer.send("app:overlay-expanded", expanded),
  setSidePanel: (open) => ipcRenderer.send("app:side-panel", open),
  setKeyboard: (visible) => ipcRenderer.send("app:keyboard", visible),
  keyboardKey: (key) => ipcRenderer.send("app:keyboard-key", key),
  onState: (callback) => ipcRenderer.on("app:state", (_event, state) => callback(state)),
  onNotification: (callback) => ipcRenderer.on("app:notification", (_event, notice) => callback(notice)),
  onDismissNotification: (callback) => ipcRenderer.on("app:dismiss-notification", callback),
});
