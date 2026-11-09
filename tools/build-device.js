// Builds the EchoMuse device program.
//
// The device is a separate process: it owns the link to the EchoMuse
// controller, the microphone and the speaker. The application renders the
// state it reports.
//
// The binaries are built ahead of time and kept in `vendor/`, one per
// architecture, so the package is self-contained and needs nothing from the
// network at install time.
//
//   npm run build:device
//
// The device needs the EchoMuse SDK, which is developed in a checkout
// rather than published. Set ECHOMUSE_SDK to point somewhere else.

const { spawnSync } = require("node:child_process");
const fs = require("node:fs");
const os = require("node:os");
const path = require("node:path");

const root = path.join(__dirname, "..");
const deviceDir = path.join(root, "device");
const vendorDir = path.join(root, "vendor");
const sdk = process.env.ECHOMUSE_SDK || path.join(os.homedir(), "Documents", "GitHub", "EchoMuse-SDK-upstream");

// The architectures a package can be built for, as they appear in the name
// of the packaged directory.
const ARCHITECTURES = [
  { package: "x64", goarch: "amd64" },
  { package: "arm64", goarch: "arm64" },
];

if (!fs.existsSync(path.join(sdk, "clients", "go"))) {
  console.error(`build: the SDK is not at ${sdk}. Set ECHOMUSE_SDK to the path of the EchoMuse SDK checkout.`);
  process.exit(1);
}

fs.mkdirSync(vendorDir, { recursive: true });

for (const architecture of ARCHITECTURES) {
  const target = path.join(vendorDir, `echomuse-device-linux-${architecture.goarch}`);
  console.log(`build: the device for ${architecture.goarch}`);
  // CGO is off: the audio reaches libasound through purego, which loads
  // the library at run time rather than linking against it. That means the
  // binary cross compiles without a C toolchain, and it still starts on a
  // machine with no sound card.
  const build = spawnSync("go", [
    "build", "-trimpath", "-ldflags=-s -w",
    "-o", target, ".",
  ], {
    cwd: deviceDir,
    stdio: "inherit",
    env: { ...process.env, CGO_ENABLED: "0", GOOS: "linux", GOARCH: architecture.goarch },
  });
  if (build.status !== 0) {
    console.error(`build: the device for ${architecture.goarch} did not build`);
    process.exit(1);
  }
  console.log(`build: ${target}`);
}
