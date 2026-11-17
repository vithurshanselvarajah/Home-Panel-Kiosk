

const tab = document.querySelector("#drawer-tab");

tab?.addEventListener("click", () => {
  window.homePanelTab.openDrawer();
});

window.homePanelTab.onDrawerState((open) => {
  if (!tab) return;
  tab.hidden = Boolean(open);
  tab.setAttribute("aria-expanded", String(!open));
});
