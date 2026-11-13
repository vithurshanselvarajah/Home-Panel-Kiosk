const test = require("node:test");
const assert = require("node:assert/strict");

// The device program.
//
// The device program is the panel's voice: it owns the microphone, the
// speaker, the wake word and the connection to Home Assistant.
//
// A kiosk is on a wall for a year. The device program can stop — a
// sound card is taken by something else, the machine runs out of
// something, the program meets a fault — and a panel that cannot hear
// and cannot be heard is a panel that needs a person to visit it.
//
// So the device program is started again when it stops.
//
// The child process itself is not started here: a test that leaves a
// child running is a test that never finishes, and what is being
// checked is the panel's reaction to the child going, not the child's
// own life.

const { DeviceClient } = require("../src/device");

// A device client whose child is a stand-in.
//
// The stand-in is a child process that is never really spawned: the
// test says the child has stopped and watches what the panel does.
function client() {
  const logs = [];
  const c = new DeviceClient({
    binary: "",
    stateFile: "/tmp/homepanel-test-state.json",
    levelFile: "/tmp/homepanel-test-level",
    deviceId: "test-panel",
    dataDirectory: "/tmp",
    onState: () => {},
    onLog: (message) => logs.push(message),
  });
  // The child is replaced with a stand-in, so that the panel's reaction
  // to a child that has stopped can be watched without a child.
  c.child = {
    kill() {},
    once() {},
    on() {},
    unref() {},
    stderr: { on() {}, destroy() {} },
  };
  return { c, logs };
}

test("the device program is started again when it stops", async () => {
  const { c, logs } = client();

  // The child has stopped. The panel notices and says so.
  c.child = null;
  c.restart();

  // The panel waits a moment before starting it again, so that a
  // program that will not start is not started over and over.
  assert.ok(
    !logs.some((line) => line.includes("started again")),
    "the device program was started again at once",
  );

  // The wait ends and the device program is started.
  await new Promise((resolve) => setTimeout(resolve, 1200));
  assert.ok(
    logs.some((line) => line.includes("started again")),
    `the device program was not started again: ${logs.join("; ")}`,
  );
});

test("the device program is not started again once the panel is stopping", async () => {
  const { c, logs } = client();

  // The panel is going away.
  c.stopping = true;

  // The child has stopped at the same moment.
  c.restart();

  await new Promise((resolve) => setTimeout(resolve, 1200));
  assert.ok(
    !logs.some((line) => line.includes("started again")),
    `the device program was started again after the panel stopped: ${logs.join("; ")}`,
  );
});

test("the wait grows when the device program keeps stopping", async () => {
  const { c } = client();

  c.restart();
  const first = c.restartWait;
  c.restartTimer = null;
  c.restart();
  const second = c.restartWait;

  assert.ok(second > first, `the wait did not grow: ${first} then ${second}`);
  assert.ok(second <= 30000, `the wait grew past its limit: ${second}`);
});
