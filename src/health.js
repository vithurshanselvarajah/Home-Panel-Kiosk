const PROBE_INTERVAL_MS = 60_000;
const PROBE_TIMEOUT_MS = 5_000;

function createHealthChecker({ onChange } = {}) {
  const probes = new Map();
  const states = new Map();
  let timer = null;

  function emit(name, status) {
    if (states.get(name) === status) return;
    states.set(name, status);
    if (typeof onChange === "function") onChange(name, status);
  }

  async function runOne(name) {
    const probe = probes.get(name);
    if (typeof probe !== "function") return;
    try {
      await probe({ timeoutMs: PROBE_TIMEOUT_MS });
      emit(name, "online");
    } catch {
      emit(name, "offline");
    }
  }

  async function tick() {
    await Promise.all(Array.from(probes.keys(), runOne));
  }

  async function probe(name) {
    await runOne(name);
  }

  function setProbes(nextProbes) {
    probes.clear();
    states.clear();
    for (const [name, fn] of Object.entries(nextProbes || {})) {
      if (typeof fn === "function") probes.set(name, fn);
    }
  }

  function start() {
    if (timer) return;
    tick();
    timer = setInterval(tick, PROBE_INTERVAL_MS);
  }

  function stop() {
    if (!timer) return;
    clearInterval(timer);
    timer = null;
  }

  function status(name) {
    return states.get(name) || "offline";
  }

  return { start, stop, setProbes, tick, probe, status };
}

async function probeHttp(url, { timeoutMs = PROBE_TIMEOUT_MS } = {}) {
  if (!url) throw new Error("no URL configured");
  let parsed;
  try { parsed = new URL(url); }
  catch { throw new Error(`invalid URL: ${url}`); }
  if (!["http:", "https:"].includes(parsed.protocol)) throw new Error(`unsupported protocol: ${parsed.protocol}`);

  const controller = new AbortController();
  const timer = setTimeout(() => controller.abort(), timeoutMs);
  try {
    const response = await fetch(parsed.toString(), {
      method: "GET",
      redirect: "follow",
      signal: controller.signal,
      headers: { Accept: "text/html, application/json, */*" },
    });
    if (response.status >= 500) throw new Error(`HTTP ${response.status}`);
    await response.body?.cancel?.().catch(() => {});
  } finally {
    clearTimeout(timer);
  }
}

module.exports = {
  createHealthChecker,
  probeHttp,
  PROBE_INTERVAL_MS,
  PROBE_TIMEOUT_MS,
};