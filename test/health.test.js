const test = require("node:test");
const assert = require("node:assert/strict");
const { createHealthChecker, probeHttp, PROBE_INTERVAL_MS, PROBE_TIMEOUT_MS } = require("../src/health");

test("createHealthChecker reports online when probe succeeds", async () => {
  const changes = [];
  const checker = createHealthChecker({ onChange: (name, status) => changes.push([name, status]) });
  checker.setProbes({ foo: async () => {} });
  await checker.tick();
  assert.equal(checker.status("foo"), "online");
  assert.deepEqual(changes, [["foo", "online"]]);
});

test("createHealthChecker reports offline when probe throws", async () => {
  const changes = [];
  const checker = createHealthChecker({ onChange: (name, status) => changes.push([name, status]) });
  checker.setProbes({ bar: async () => { throw new Error("nope"); } });
  await checker.tick();
  assert.equal(checker.status("bar"), "offline");
  assert.deepEqual(changes, [["bar", "offline"]]);
});

test("createHealthChecker skips non-function probes", async () => {
  const checker = createHealthChecker();
  checker.setProbes({ a: async () => {}, b: undefined, c: null });
  await checker.tick();
  assert.equal(checker.status("a"), "online");
  assert.equal(checker.status("b"), "offline");
  assert.equal(checker.status("c"), "offline");
});

test("createHealthChecker reports unknown services as offline", () => {
  const checker = createHealthChecker();
  assert.equal(checker.status("nothing"), "offline");
});

test("createHealthChecker emits transitions in order", async () => {
  let fail = true;
  const changes = [];
  const checker = createHealthChecker({ onChange: (n, s) => changes.push([n, s]) });
  checker.setProbes({ x: async () => { if (fail) throw new Error("x"); } });
  await checker.tick();
  assert.deepEqual(changes, [["x", "offline"]]);
  fail = false;
  await checker.tick();
  assert.deepEqual(changes, [["x", "offline"], ["x", "online"]]);
  await checker.tick();
  assert.deepEqual(changes, [["x", "offline"], ["x", "online"]]);
});

test("createHealthChecker.setProbes resets state and re-probes", async () => {
  let count = 0;
  const checker = createHealthChecker();
  checker.setProbes({ a: async () => { count += 1; throw new Error("e"); } });
  await checker.tick();
  assert.equal(checker.status("a"), "offline");
  checker.setProbes({ a: async () => { count += 1; } });
  await checker.tick();
  assert.equal(checker.status("a"), "online");
});

test("probeHttp throws on invalid URL", async () => {
  await assert.rejects(() => probeHttp("not-a-url"), /invalid URL/);
  await assert.rejects(() => probeHttp("file:///etc/passwd"), /unsupported protocol/);
  await assert.rejects(() => probeHttp(""), /no URL configured/);
});

test("probeHttp rejects 5xx and accepts 2xx/3xx/4xx", async () => {
  const cases = [
    { status: 200, ok: true },
    { status: 204, ok: true },
    { status: 301, ok: true },
    { status: 404, ok: true },
    { status: 500, ok: false },
    { status: 502, ok: false },
  ];
  for (const { status, ok } of cases) {
    const origFetch = global.fetch;
    global.fetch = async () => ({ ok: status >= 200 && status < 300, status, body: { cancel: async () => {} } });
    try {
      await probeHttp("http://example.test/");
      assert.ok(ok, `status ${status} should have ${ok ? "succeeded" : "thrown"}`);
    } catch (err) {
      assert.ok(!ok, `status ${status} should have ${ok ? "succeeded" : "thrown"}`);
      assert.match(err.message, new RegExp(`HTTP ${status}`));
    } finally {
      global.fetch = origFetch;
    }
  }
});

test("probeHttp aborts after the configured timeout", async () => {
  const origFetch = global.fetch;
  let aborted = false;
  global.fetch = async (url, init) => {
    return await new Promise((_, reject) => {
      init.signal.addEventListener("abort", () => { aborted = true; reject(new Error("aborted")); });
    });
  };
  try {
    await assert.rejects(() => probeHttp("http://example.test/", { timeoutMs: 10 }), /aborted/);
    assert.equal(aborted, true);
  } finally {
    global.fetch = origFetch;
  }
});

test("constants match documented defaults", () => {
  assert.equal(PROBE_INTERVAL_MS, 60_000);
  assert.equal(PROBE_TIMEOUT_MS, 5_000);
});

test("createHealthChecker.probe re-probes a single service", async () => {
  let count = 0;
  const checker = createHealthChecker();
  checker.setProbes({ a: async () => { count += 1; throw new Error("e"); } });
  await checker.tick();
  assert.equal(count, 1);
  assert.equal(checker.status("a"), "offline");
  checker.setProbes({ a: async () => { count += 1; } });
  await checker.probe("a");
  assert.equal(count, 2);
  assert.equal(checker.status("a"), "online");
});

test("createHealthChecker.probe returns a promise that resolves after the probe completes", async () => {
  let resolved = false;
  const checker = createHealthChecker();
  checker.setProbes({ slow: async () => { await new Promise(r => setTimeout(r, 20)); resolved = true; } });
  await checker.probe("slow");
  assert.equal(resolved, true);
});