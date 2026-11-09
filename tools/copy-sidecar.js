// Copies the EchoMuse sidecar into the packaged application.
//
// The sidecar is a separate process: it owns the link to the EchoMuse
// controller, the microphone and the speaker. The application renders the
// state it reports.
//
// The binaries are built ahead of time and kept in `vendor/`, one per
// architecture, so the package is self-contained and needs nothing from the
// network at install time.

const fs = require("node:fs");
const path = require("node:path");

// The name the device program is installed as.
const BINARY_NAME = "echomuse-device";

// Where the built binaries live.
const VENDOR_DIRECTORY = path.join(__dirname, "..", "vendor");

// Forge names the architecture "x64" or "arm64"; the binaries are named for
// the architecture Go builds for.
const BINARY_NAMES = {
  x64: "echomuse-device-linux-amd64",
  arm64: "echomuse-device-linux-arm64",
};

// The architectures a package can be built for, as they appear in the name
// of the packaged directory.
const ARCHITECTURES = ["x64", "arm64"];

/**
 * Copies the sidecar into a packaged output directory.
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
    console.warn(`skipping the EchoMuse device: ${outputPath} names no known architecture`);
    return;
  }

  const source = path.join(VENDOR_DIRECTORY, BINARY_NAMES[architecture]);
  if (!fs.existsSync(source)) {
    throw new Error(`the EchoMuse device for ${architecture} is missing: ${source} was not found`);
  }

  const target = path.join(outputPath, BINARY_NAME);
  fs.copyFileSync(source, target);
  fs.chmodSync(target, 0o755);
  console.log(`the EchoMuse device for ${architecture} is at ${target}`);

  // The third-party notices travel with the binary: it contains BSD-licensed
  // Go libraries.
  const notice = path.join(VENDOR_DIRECTORY, "ECHOMUSE-NOTICE.md");
  if (fs.existsSync(notice)) {
    fs.copyFileSync(notice, path.join(outputPath, "ECHOMUSE-NOTICE.md"));
  }
}

module.exports = { copySidecar, BINARY_NAME };
