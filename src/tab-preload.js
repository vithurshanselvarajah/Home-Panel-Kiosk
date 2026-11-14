const { contextBridge, ipcRenderer } = require("electron");

// The bridge for the pull out tab.
//
// The tab is a page of its own, so it cannot reach the drawer directly: it
// asks the main process to open it, and the main process tells it when the
// drawer is open so that the tab can be put away.
//
// Nothing else is exposed. The tab only opens the drawer.
contextBridge.exposeInMainWorld("homePanelTab", {
  openDrawer: () => ipcRenderer.send("tab:open-drawer"),
  onDrawerState: (callback) => ipcRenderer.on("tab:drawer-state", (_event, open) => callback(Boolean(open))),
});
