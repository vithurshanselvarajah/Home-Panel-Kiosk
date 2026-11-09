// Runs the app locally with a mock EchoMuse controller.
//
// The mock controller is a Go program in the SDK. This script builds it,
// starts it, and hands the app the URL it is listening on, so the whole
// thing — app, sidecar, controller — runs with one command.
//
// The controller is started with the app rather than with the sidecar,
// because the sidecar is the app's child and the controller is the thing
// the app talks to.
//
//   npm run dev
//
// The controller's URL is printed, so it can be pointed at something else.

const { spawn, spawnSync } = require("node:child_process");
const fs = require("node:fs");
const os = require("node:os");
const path = require("node:path");

const root = path.join(__dirname, "..");
const sdk = process.env.ECHOMUSE_SDK || path.join(os.homedir(), "Documents", "GitHub", "EchoMuse-SDK-upstream");
const controllerDir = path.join(sdk, "clients", "go", "tools", "mockcontroller");
const binary = path.join(os.tmpdir(), "echomuse-mock-controller");

const mode = process.argv.includes("--pending") ? "pending" : "approve";

function fail(message) {
  console.error(`dev: ${message}`);
  process.exit(1);
}

if (!fs.existsSync(controllerDir)) {
  fail(`the SDK is not at ${sdk}. Set ECHOMUSE_SDK to the path of the EchoMuse SDK checkout.`);
}

// The sidecar is built for the machine, so the controller is too.
console.log("dev: building the mock controller");
const build = spawnSync("go", ["build", "-o", binary, "."], { cwd: controllerDir, stdio: "inherit" });
if (build.status !== 0) fail("the mock controller did not build");

console.log("dev: starting the mock controller");
const controller = spawn(binary, ["--mode", mode], { stdio: ["ignore", "pipe", "inherit"] });

let url = "";
controller.stdout.on("data", (chunk) => {
  const text = String(chunk);
  process.stdout.write(text);
  // The URL is the first line on the standard output.
  if (!url) {
    const line = text.split("\n")[0].trim();
    if (line.startsWith("ws://")) url = line;
  }
});

const sidecars = [];
function cleanup() {
  for (const child of sidecars) {
    try { child.kill(); } catch { /* already gone */ }
  }
  try { controller.kill(); } catch { /* already gone */ }
}
process.on("exit", cleanup);
process.on("SIGINT", () => { cleanup(); process.exit(0); });
process.on("SIGTERM", () => { cleanup(); process.exit(0); });

controller.on("exit", (code) => {
  if (code !== 0 && code !== null) console.error("dev: the mock controller stopped");
  process.exit(code || 0);
});

// The app is started once the controller is listening, so the sidecar never
// dials a URL that is not there yet.
const start = setInterval(() => {
  if (!url) return;
  clearInterval(start);

  console.log(`dev: the mock controller is at ${url}`);
  const app = spawn("npx", ["electron", ".", "--dev"], {
    cwd: root,
    stdio: "inherit",
    env: { ...process.env, ECHOMUSE_CONTROLLER_URL: url },
  });
  sidecars.push(app);
  app.on("exit", (code) => { cleanup(); process.exit(code || 0); });
}, 100);
