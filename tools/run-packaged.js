

const { spawn, spawnSync } = require("node:child_process");
const fs = require("node:fs");
const path = require("node:path");

const root = path.join(__dirname, "..");
const outDir = path.join(root, "out");

const arch = process.arch === "arm64" ? "arm64" : "x64";
const packagedDir = path.join(outDir, `homepanel-kiosk-linux-${arch}`);
const binary = path.join(packagedDir, "homepanel-kiosk");

function fail(message) {
  console.error(`run: ${message}`);
  process.exit(1);
}

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

const device = path.join(packagedDir, "home-panel-device");
const source = path.join(root, "vendor", `home-panel-device-linux-${arch === "x64" ? "amd64" : arch}`);
if (!fs.existsSync(source)) fail(`there is no device program at ${source}`);

const needsCopy =
  !fs.existsSync(device) || fs.statSync(source).mtimeMs > fs.statSync(device).mtimeMs;
if (needsCopy) {
  console.log("run: putting the device program beside the app");
  fs.copyFileSync(source, device);
}
fs.chmodSync(device, 0o755);

console.log("run: starting the packaged app");
const app = spawn(binary, process.argv.slice(2), {
  cwd: packagedDir,
  stdio: "inherit",
  env: process.env,
});
app.on("exit", (code) => process.exit(code || 0));
