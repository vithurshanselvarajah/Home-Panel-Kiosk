const test = require("node:test");
const assert = require("node:assert/strict");

const { DeviceClient } = require("../src/device");

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

  c.child = null;
  c.restart();

  assert.ok(
    !logs.some((line) => line.includes("started again")),
    "the device program was started again at once",
  );

  await new Promise((resolve) => setTimeout(resolve, 1200));
  assert.ok(
    logs.some((line) => line.includes("started again")),
    `the device program was not started again: ${logs.join("; ")}`,
  );
});

test("the device program is not started again once the panel is stopping", async () => {
  const { c, logs } = client();

  c.stopping = true;

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
