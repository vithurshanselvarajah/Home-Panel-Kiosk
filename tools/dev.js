// Runs the app against an EchoMuse controller.
//
//   npm run dev              the real controller, found by mDNS
//   npm run dev -- --controller ws://192.168.1.132:8767
//                            the real controller, by address
//   npm run dev -- --mock    a mock controller, built from the SDK
//
// The mock is not the default. A device that only ever meets a mock is a
// device that has never met the real thing, and the real thing is what the
// device is for.
//
// The controller is started with the app rather than with the sidecar,
// because the sidecar is the app's child and the controller is the thing
// the app talks to.
//
// The app's EchoMuse link is off by default, so a development run turns it
// on: a device that has never been seen has to be admitted by an
// administrator before it does anything, and a development run should not
// need the settings panel open to get there.

const { spawn, spawnSync } = require("node:child_process");
const fs = require("node:fs");
const os = require("node:os");
const path = require("node:path");

const root = path.join(__dirname, "..");
const sdk = process.env.ECHOMUSE_SDK || path.join(os.homedir(), "Documents", "GitHub", "EchoMuse-SDK-upstream");
const controllerDir = path.join(sdk, "clients", "go", "tools", "mockcontroller");
const binary = path.join(os.tmpdir(), "echomuse-mock-controller");

const args = process.argv.slice(2);
const controllerArg = args.indexOf("--controller");
const namedController = controllerArg >= 0 ? args[controllerArg + 1] : "";
const useMock = args.includes("--mock");
const mode = args.includes("--pending") ? "pending" : "approve";

function fail(message) {
  console.error(`dev: ${message}`);
  process.exit(1);
}

if (useMock && !fs.existsSync(controllerDir)) {
  fail(`the SDK is not at ${sdk}. Set ECHOMUSE_SDK to the path of the EchoMuse SDK checkout.`);
}

const children = [];
function cleanup() {
  for (const child of children) {
    try { child.kill(); } catch { /* already gone */ }
  }
}
process.on("exit", cleanup);
process.on("SIGINT", () => { cleanup(); process.exit(0); });
process.on("SIGTERM", () => { cleanup(); process.exit(0); });

// startApp runs the Electron app against a controller.
//
// An empty URL means the sidecar discovers the controller by mDNS, which
// is what a device on the network does.
function startApp(url) {
  console.log(url ? `dev: the controller is at ${url}` : "dev: the controller is found by mDNS");
  const app = spawn("npx", ["electron", ".", "--dev"], {
    cwd: root,
    stdio: "inherit",
    env: {
      ...process.env,
      ECHOMUSE_CONTROLLER_URL: url || "",
      // A development run turns the link on, because a device that has
      // never been admitted is waiting for an administrator either way.
      ECHOMUSE_ENABLED: "1",
    },
  });
  children.push(app);
  app.on("exit", (code) => { cleanup(); process.exit(code || 0); });
}

if (useMock) {
  // The sidecar is built for the machine, so the controller is too.
  console.log("dev: building the mock controller");
  const build = spawnSync("go", ["build", "-o", binary, "."], { cwd: controllerDir, stdio: "inherit" });
  if (build.status !== 0) fail("the mock controller did not build");

  console.log("dev: starting the mock controller");
  const controller = spawn(binary, ["--mode", mode], { stdio: ["ignore", "pipe", "inherit"] });
  children.push(controller);

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

  controller.on("exit", (code) => {
    if (code !== 0 && code !== null) console.error("dev: the mock controller stopped");
    process.exit(code || 0);
  });

  // The app is started once the controller is listening, so the sidecar
  // never dials a URL that is not there yet.
  const start = setInterval(() => {
    if (!url) return;
    clearInterval(start);
    startApp(url);
  }, 100);
} else {
  startApp(namedController);
}
