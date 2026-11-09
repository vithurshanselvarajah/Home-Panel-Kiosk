// Builds the app and runs the packaged build.
//
//   npm run packaged
//
// The packaged build is what the .deb installs, so it is what a device
// actually runs. Running it means the device program is found beside the binary
// the way it is on a device, rather than in the repository's vendor
// directory, and the Electron binary is the one that was packaged rather
// than the one in node_modules.
//
// The build is made if it is not already there, and the device program is put
// beside it, because the packaging step is what does that.

const { spawn, spawnSync } = require("node:child_process");
const fs = require("node:fs");
const path = require("node:path");

const root = path.join(__dirname, "..");
const outDir = path.join(root, "out");

// The architecture the build is made for.
const arch = process.arch === "arm64" ? "arm64" : "x64";
const packagedDir = path.join(outDir, `homepanel-kiosk-linux-${arch}`);
const binary = path.join(packagedDir, "homepanel-kiosk");

function fail(message) {
  console.error(`run: ${message}`);
  process.exit(1);
}

// The build is made when it is not there, or when the source is newer than
// the build.
function needsBuild() {
  if (!fs.existsSync(binary)) return true;
  const built = fs.statSync(binary).mtimeMs;
  const sources = [path.join(root, "src"), path.join(root, "package.json")];
  let newest = 0;
  const walk = (target) => {
    const stat = fs.statSync(target);
    if (stat.isDirectory()) {
      for (const entry of fs.readdirSync(target)) walk(path.join(target, entry));
      return;
    }
    newest = Math.max(newest, stat.mtimeMs);
  };
  for (const source of sources) {
    if (fs.existsSync(source)) walk(source);
  }
  return newest > built;
}

if (needsBuild()) {
  console.log("run: building the app");
  const build = spawnSync("npx", ["electron-forge", "package"], { cwd: root, stdio: "inherit" });
  if (build.status !== 0) fail("the build did not finish");
}

if (!fs.existsSync(binary)) fail(`there is no build at ${binary}`);

// The sidecar has to be executable, and the packaging step puts it beside
// the binary. It is copied there again when it is missing, and when the
// one in the package is older than the one that was built.
//
// Copying only when it is missing would run an old device program: the
// package keeps whatever was copied the first time, and a rebuild of the
// device program would never reach the packaged build. The bug looks like
// a fix that did not take.
const device = path.join(packagedDir, "echomuse-device");
const source = path.join(root, "vendor", `echomuse-device-linux-${arch === "x64" ? "amd64" : arch}`);
if (!fs.existsSync(source)) fail(`there is no EchoMuse device at ${source}`);

const needsCopy =
  !fs.existsSync(device) || fs.statSync(source).mtimeMs > fs.statSync(device).mtimeMs;
if (needsCopy) {
  console.log("run: putting the device program beside the app");
  fs.copyFileSync(source, device);
}
fs.chmodSync(device, 0o755);

// The packaged app keeps its settings in the same place a device does, so
// a packaged run and a development run see the same configuration.
console.log("run: starting the packaged app");
const app = spawn(binary, process.argv.slice(2), {
  cwd: packagedDir,
  stdio: "inherit",
  env: {
    ...process.env,
    // A packaged run is a device run, so the EchoMuse link stays as the
    // device's own settings say. The environment can still turn it on for
    // a run that has not been configured yet.
    ECHOMUSE_ENABLED: process.env.ECHOMUSE_ENABLED || "",
  },
});
app.on("exit", (code) => process.exit(code || 0));
