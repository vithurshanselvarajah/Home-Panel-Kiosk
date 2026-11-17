

const fs = require("node:fs");
const os = require("node:os");
const path = require("node:path");
const { spawnSync } = require("node:child_process");

const BINARY_NAME = "home-panel-device";

const VENDOR_DIRECTORY = path.join(__dirname, "..", "vendor");

const BINARY_NAMES = {
  x64: "home-panel-device-linux-amd64",
  arm64: "home-panel-device-linux-arm64",
};

const ARCHITECTURES = ["x64", "arm64"];

const RUNTIME_LIBRARIES = {
  x64: "libonnxruntime-linux-amd64.so.1",
  arm64: "libonnxruntime-linux-arm64.so.1",
};

const RUNTIME_PACKAGE_NAME = "libonnxruntime.so.1";

const RUNTIME_CACHE = path.join(
  require("node:os").homedir(), ".cache", "home-panel", "onnxruntime"
);

function copySidecar(outputPath) {
  const architecture = ARCHITECTURES.find((name) => outputPath.endsWith(`-linux-${name}`));
  if (!architecture) {
    console.warn(`skipping the device program: ${outputPath} names no known architecture`);
    return;
  }

  const source = path.join(VENDOR_DIRECTORY, BINARY_NAMES[architecture]);
  if (!fs.existsSync(source)) {
    throw new Error(`the device program for ${architecture} is missing: ${source} was not found`);
  }

  const target = path.join(outputPath, BINARY_NAME);
  fs.copyFileSync(source, target);
  fs.chmodSync(target, 0o755);
  console.log(`the device program for ${architecture} is at ${target}`);

  const runtime = findRuntime(architecture);
  if (runtime) {
    const library = path.join(outputPath, RUNTIME_PACKAGE_NAME);
    fs.copyFileSync(runtime, library);
    fs.chmodSync(library, 0o755);
    console.log(`the onnx runtime for ${architecture} is at ${library}`);
  } else {
    console.warn(`the onnx runtime for ${architecture} was not found; the panel will have no local wake word`);
  }

  const notice = path.join(VENDOR_DIRECTORY, "THIRD-PARTY-NOTICE.md");
  if (fs.existsSync(notice)) {
    fs.copyFileSync(notice, path.join(outputPath, "THIRD-PARTY-NOTICE.md"));
  }
}

function findRuntime(architecture) {

  const built = path.join(
    __dirname, "..", "vendor", RUNTIME_LIBRARIES[architecture]
  );
  if (fs.existsSync(built)) return built;

  const release = architecture === "x64" ? "onnxruntime-linux-x64" : "onnxruntime-linux-aarch64";
  if (!fs.existsSync(RUNTIME_CACHE)) return null;
  for (const entry of fs.readdirSync(RUNTIME_CACHE)) {
    if (!entry.startsWith(release)) continue;
    const library = path.join(RUNTIME_CACHE, entry, "lib", "libonnxruntime.so.1.23.2");
    if (fs.existsSync(library)) return library;
  }
  return null;
}

function clearSandboxSetuid(packagePath) {
  if (!packagePath.endsWith(".deb")) return;

  const staging = fs.mkdtempSync(path.join(os.tmpdir(), "homepanel-sandbox-"));
  try {
    const unpacked = path.join(staging, "unpacked");
    const unpack = spawnSync("dpkg-deb", ["-R", packagePath, unpacked], {
      stdio: ["ignore", "ignore", "pipe"],
      encoding: "utf8",
    });
    if (unpack.status !== 0) {
      console.warn(`the package could not be unpacked: ${packagePath}`);
      return;
    }

    const sandbox = path.join(unpacked, "usr/lib/homepanel-kiosk/chrome-sandbox");
    if (!fs.existsSync(sandbox)) {
      console.warn(`the Chromium sandbox helper is not in ${packagePath}`);
      return;
    }

    const mode = fs.statSync(sandbox).mode & 0o7777;
    if ((mode & 0o4000) === 0) {
      console.log(`the sandbox helper in ${packagePath} is not setuid`);
      return;
    }
    fs.chmodSync(sandbox, mode & ~0o4000);

    const rebuild = spawnSync(
      "dpkg-deb",
      ["--root-owner-group", "-b", unpacked, packagePath],
      {
        stdio: ["ignore", "ignore", "pipe"],
        encoding: "utf8",
      }
    );
    if (rebuild.status !== 0) {
      console.warn(`the package could not be rebuilt: ${packagePath}\n${rebuild.stderr}`);
      return;
    }
    console.log(`the setuid bit was taken off the sandbox helper in ${packagePath}`);
  } finally {
    fs.rmSync(staging, { recursive: true, force: true });
  }
}

module.exports = { copySidecar, clearSandboxSetuid, BINARY_NAME };
