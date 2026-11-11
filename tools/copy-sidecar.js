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
const path = require("node:path");

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
const RUNTIME_LIBRARIES = {
  x64: "libonnxruntime.so.1",
  arm64: "libonnxruntime.so.1",
};

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
    const library = path.join(outputPath, RUNTIME_LIBRARIES[architecture]);
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
  const release = architecture === "x64" ? "onnxruntime-linux-x64" : "onnxruntime-linux-aarch64";
  if (!fs.existsSync(RUNTIME_CACHE)) return null;
  for (const entry of fs.readdirSync(RUNTIME_CACHE)) {
    if (!entry.startsWith(release)) continue;
    const library = path.join(RUNTIME_CACHE, entry, "lib", RUNTIME_LIBRARIES[architecture]);
    if (fs.existsSync(library)) return library;
  }
  return null;
}

module.exports = { copySidecar, BINARY_NAME };
