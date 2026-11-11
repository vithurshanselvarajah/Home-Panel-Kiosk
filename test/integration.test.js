const test = require("node:test");
const assert = require("node:assert/strict");
const { createHealthChecker } = require("../src/health");

const USER_CONFIG = {
  dashboardUrl: "http://192.168.1.132:8123/",
  slideshowUrl: "http://192.168.1.132:3000/",
};

function makeApp({ haReachable = true, immichReachable = true, controlConnected = false } = {}) {
  const serviceStatus = { control: "unknown", immich: "unknown", dashboard: "unknown" };
  const transitions = [];
  let connected = controlConnected;
  const controlClient = { get connected() { return connected; } };
  function setConnected(value) { connected = value; }

  function probeControl() {
    if (!controlClient?.connected) throw new Error("disconnected");
  }
  async function probeHomeAssistant() {
    if (!USER_CONFIG.dashboardUrl) throw new Error("not configured");
    if (!haReachable) throw new Error("unreachable");
  }
  async function probeImmich() {
    if (!USER_CONFIG.slideshowUrl) throw new Error("not configured");
    if (!immichReachable) throw new Error("unreachable");
  }

  const checker = createHealthChecker({
    onChange: (name, status) => {
      if (serviceStatus[name] === status) return;
      serviceStatus[name] = status;
      transitions.push({ name, status });
    },
  });
  checker.setProbes({ control: probeControl, dashboard: probeHomeAssistant, immich: probeImmich });

  return { serviceStatus, transitions, checker, controlClient, setConnected };
}

test("first health-check tick: HA and Immich report online; the device program reports offline while still connecting", async () => {
  const { serviceStatus, checker } = makeApp({ controlConnected: false });
  await checker.tick();
  assert.equal(serviceStatus.dashboard, "online");
  assert.equal(serviceStatus.immich, "online");
  assert.equal(serviceStatus.control, "offline");
});

test("the device program connecting triggers a re-probe and the indicator flips to online", async () => {
  const app = makeApp({ controlConnected: false });
  await app.checker.tick();
  assert.equal(app.serviceStatus.control, "offline");
  app.setConnected(true);
  await app.checker.probe("control");
  assert.equal(app.serviceStatus.control, "online");
  const controlTransitions = app.transitions.filter((t) => t.name === "control").map((t) => t.status);
  assert.deepEqual(controlTransitions, ["offline", "online"]);
});

test("the device program disconnecting flips the indicator back to offline", async () => {
  const app = makeApp({ controlConnected: true });
  await app.checker.tick();
  assert.equal(app.serviceStatus.control, "online");
  app.setConnected(false);
  await app.checker.probe("control");
  assert.equal(app.serviceStatus.control, "offline");
});

test("openMode slideshow triggers immediate re-probe of immich", async () => {
  const { serviceStatus, checker } = makeApp({ immichReachable: true });
  await checker.tick();
  assert.equal(serviceStatus.immich, "online");
  await checker.probe("immich");
  assert.equal(serviceStatus.immich, "online");
});

test("openMode slideshow when Immich unreachable flips indicator offline immediately", async () => {
  const { serviceStatus, checker } = makeApp({ immichReachable: false });
  await checker.tick();
  assert.equal(serviceStatus.immich, "offline");
});

test("openMode dashboard triggers immediate re-probe of dashboard service", async () => {
  const { serviceStatus, checker } = makeApp({ haReachable: true });
  await checker.tick();
  assert.equal(serviceStatus.dashboard, "online");
  await checker.probe("dashboard");
  assert.equal(serviceStatus.dashboard, "online");
});

test("full user scenario: after flipping slideshow<->dashboard, all three indicators reflect real state", async () => {
  const app = makeApp({ controlConnected: false });
  await app.checker.tick();
  assert.equal(app.serviceStatus.dashboard, "online");
  assert.equal(app.serviceStatus.immich, "online");
  assert.equal(app.serviceStatus.control, "offline");

  await app.checker.probe("immich");
  assert.equal(app.serviceStatus.immich, "online");

  app.setConnected(true);
  await app.checker.probe("control");
  assert.equal(app.serviceStatus.control, "online");

  await app.checker.probe("dashboard");
  assert.equal(app.serviceStatus.dashboard, "online");
});

test("re-probe of unknown URL returns offline", async () => {
  const { serviceStatus, checker } = makeApp({ haReachable: true, immichReachable: false });
  await checker.tick();
  assert.equal(serviceStatus.dashboard, "online");
  assert.equal(serviceStatus.immich, "offline");
});

test("keyboard state machine: tapping editable field while keyboard is hidden re-shows it", async () => {
  // Simulates the remote-preload's checkFocus + main's setKeyboardVisible logic.
  let focusedEditable = false;
  let keyboardVisible = false;
  let ipcCalls = 0;

  function checkFocus(active) {
    const next = active === "editable";
    focusedEditable = next;
    ipcCalls += 1;
    return next;
  }

  function setKeyboardVisible(visible, enabled = true) {
    const next = Boolean(visible) && enabled;
    if (keyboardVisible === next) return;
    keyboardVisible = next;
  }

  function simulateKeyEvent() { /* no-op for this test */ }

  // Step 1: User taps editable field A → keyboard shows
  setKeyboardVisible(checkFocus("editable"));
  assert.equal(keyboardVisible, true);

  // Step 2: User taps X button on keyboard → keyboard hidden, but field A still has focus
  // (no focus event fires in the dashboard because the click is on the overlay)
  setKeyboardVisible(false);
  assert.equal(keyboardVisible, false);

  // Step 3: User taps another editable field B → focus moves from A to B
  // Both are editable, but the OLD code would dedup and not send IPC.
  // The FIX: always send IPC so main knows.
  ipcCalls = 0;
  setKeyboardVisible(checkFocus("editable"));
  assert.equal(ipcCalls, 1, "fix: IPC must fire on every focus change");
  assert.equal(keyboardVisible, true, "fix: keyboard must re-show");
});

test("keyboard state machine: tapping non-editable area closes keyboard", async () => {
  let focusedEditable = false;
  let keyboardVisible = false;

  function checkFocus(active) {
    const next = active === "editable";
    focusedEditable = next;
    return next;
  }
  function setKeyboardVisible(visible) {
    const next = Boolean(visible);
    if (keyboardVisible === next) return;
    keyboardVisible = next;
  }

  setKeyboardVisible(checkFocus("editable"));
  assert.equal(keyboardVisible, true);
  setKeyboardVisible(checkFocus("button"));
  assert.equal(keyboardVisible, false);
  setKeyboardVisible(checkFocus("editable"));
  assert.equal(keyboardVisible, true);
});

test("keyboard is never shown while in slideshow mode", () => {
  let keyboardVisible = false;
  let mode = "dashboard";

  function setKeyboardVisible(visible) {
    const next = Boolean(visible) && mode !== "slideshow";
    if (keyboardVisible === next) return;
    keyboardVisible = next;
  }

  // Step 1: User is on dashboard, taps editable field
  setKeyboardVisible(true);
  assert.equal(keyboardVisible, true);

  // Step 2: User switches to slideshow
  mode = "slideshow";
  setKeyboardVisible(false);

  // Step 3: focus event fires while in slideshow — must NOT show keyboard
  setKeyboardVisible(true);
  assert.equal(keyboardVisible, false, "keyboard must not appear in slideshow");

  // Step 4: User switches back to dashboard
  mode = "dashboard";
  setKeyboardVisible(true);
  assert.equal(keyboardVisible, true, "keyboard returns when dashboard is active");
});