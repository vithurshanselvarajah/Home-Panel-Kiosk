// The pull out tab.
//
// It is a page of its own, in a view of its own, so that it does not sit
// over the dashboard and swallow the taps meant for it. Everything it can
// do is therefore through the main process: it asks for the drawer to open,
// and the main process says whether the drawer is open so that the tab can
// be put away when it is not wanted.
//
// A tap is enough. The tab is a button, so it is a button.
const tab = document.querySelector("#drawer-tab");

tab?.addEventListener("click", () => {
  window.homePanelTab.openDrawer();
});

// The tab is put away when the drawer is open: the drawer is over the place
// it sits in, and a tab under the drawer would be a tab that cannot be seen
// or pressed.
window.homePanelTab.onDrawerState((open) => {
  if (!tab) return;
  tab.hidden = Boolean(open);
  tab.setAttribute("aria-expanded", String(!open));
});
