const { contextBridge, ipcRenderer } = require("electron");

contextBridge.exposeInMainWorld("homePanelTab", {
  openDrawer: () => ipcRenderer.send("tab:open-drawer"),
  onDrawerState: (callback) => ipcRenderer.on("tab:drawer-state", (_event, open) => callback(Boolean(open))),
});
