// Copies the device program into the packaged application.
//
// The device program is a separate process: it owns the link to Home
// Assistant, the microphone, the speaker and the wake word. The
// application renders the state it reports.
//
// The binaries are built ahead of time and kept in `vendor/`, one per
// architecture, so the package is self-contained and needs nothing from the
// network at install time.

const fs = require("node:fs");
const os = require("node:os");
const path = require("node:path");
const { spawnSync } = require("node:child_process");

// The name the device program is installed as.
const BINARY_NAME = "home-panel-device";

// Where the built binaries live.
const VENDOR_DIRECTORY = path.join(__dirname, "..", "vendor");

// Forge names the architecture "x64" or "arm64"; the binaries are named for
// the architecture Go builds for.
const BINARY_NAMES = {
  x64: "home-panel-device-linux-amd64",
  arm64: "home-panel-device-linux-arm64",
};

// The architectures a package can be built for, as they appear in the name
// of the packaged directory.
const ARCHITECTURES = ["x64", "arm64"];

// The ONNX Runtime travels with the binary.
//
// The runtime is linked into the device program, so the device needs the
// shared library beside it. It is copied into the same directory as the
// binary, and the binary is told to look there, so the panel finds its
// wake word without anything being installed on the system.
//
// The name in the package is the plain one: it is beside the binary, and
// the binary looks for that name.
const RUNTIME_LIBRARIES = {
  x64: "libonnxruntime-linux-amd64.so.1",
  arm64: "libonnxruntime-linux-arm64.so.1",
};

// The name the runtime is given in the package.
const RUNTIME_PACKAGE_NAME = "libonnxruntime.so.1";

// Where the runtimes are kept between builds.
const RUNTIME_CACHE = path.join(
  require("node:os").homedir(), ".cache", "home-panel", "onnxruntime"
);

/**
 * Copies the device program into a packaged output directory.
 *
 * The architecture is read from the directory name, because a build can ask
 * for more than one at a time — "arm64,x64" — and the hook then runs once
 * for the whole build rather than once per architecture.
 *
 * @param {string} outputPath the packaged application directory
 */
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

  // The runtime the device program was linked against.
  const runtime = findRuntime(architecture);
  if (runtime) {
    const library = path.join(outputPath, RUNTIME_PACKAGE_NAME);
    fs.copyFileSync(runtime, library);
    fs.chmodSync(library, 0o755);
    console.log(`the onnx runtime for ${architecture} is at ${library}`);
  } else {
    console.warn(`the onnx runtime for ${architecture} was not found; the panel will have no local wake word`);
  }

  // The third-party notices travel with the binary: it contains BSD-licensed
  // Go libraries.
  const notice = path.join(VENDOR_DIRECTORY, "THIRD-PARTY-NOTICE.md");
  if (fs.existsSync(notice)) {
    fs.copyFileSync(notice, path.join(outputPath, "THIRD-PARTY-NOTICE.md"));
  }
}

/**
 * Finds the ONNX Runtime library for an architecture.
 *
 * It is looked for in the build cache, which is where the build put it.
 *
 * @param {string} architecture "x64" or "arm64"
 * @returns {string|null} the path to the library, or null when it is not there
 */
function findRuntime(architecture) {
  // The build puts the runtime beside the binaries it built, under a name
  // that says which architecture it is.
  const built = path.join(
    __dirname, "..", "vendor", RUNTIME_LIBRARIES[architecture]
  );
  if (fs.existsSync(built)) return built;

  // Otherwise it is looked for in the build cache.
  const release = architecture === "x64" ? "onnxruntime-linux-x64" : "onnxruntime-linux-aarch64";
  if (!fs.existsSync(RUNTIME_CACHE)) return null;
  for (const entry of fs.readdirSync(RUNTIME_CACHE)) {
    if (!entry.startsWith(release)) continue;
    const library = path.join(RUNTIME_CACHE, entry, "lib", "libonnxruntime.so.1.23.2");
    if (fs.existsSync(library)) return library;
  }
  return null;
}

/**
 * Takes the setuid bit off the Chromium sandbox helper in a package.
 *
 * The helper is setuid root, and Chromium re-executes the browser process
 * through it. That re-execution does not carry the environment over: the
 * browser process comes up with an empty environment, so it cannot find the
 * Wayland socket or the session bus, and the panel comes up on a blank
 * screen.
 *
 * Without the setuid bit there is no re-execution and the environment
 * survives. The panel then runs its own sandbox as an unprivileged user,
 * which is what the renderer needs anyway.
 *
 * The package is built with the bit set, because the packaging helper sets
 * it whatever the application asks for, so it is taken off afterwards.
 *
 * @param {string} packagePath the .deb that was built
 */
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

    // The files are put back owned by root, the way the package had them,
    // without the build needing to be root itself.
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
