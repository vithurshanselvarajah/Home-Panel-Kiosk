

const { spawnSync } = require("node:child_process");
const fs = require("node:fs");
const path = require("node:path");

const { ARCHITECTURES, ensureRuntime, libraryDirectory } = require("./fetch-onnxruntime");

const root = path.join(__dirname, "..");
const deviceDir = path.join(root, "device");
const vendorDir = path.join(root, "vendor");

// The build tag that puts the wake word binding in. The binding is a C
// library, so it needs the runtime at link time; the tag says the build
// wants it, and the runtime is fetched below.
const TAG = "onnxruntime";

function fail(message) {
  console.error(`build: ${message}`);
  process.exit(1);
}

async function main() {
  fs.mkdirSync(vendorDir, { recursive: true });

  for (const architecture of Object.keys(ARCHITECTURES)) {
    const { directory } = await ensureRuntime(architecture);
    const target = path.join(vendorDir, `home-panel-device-linux-${ARCHITECTURES[architecture].goarch}`);
    console.log(`build: the device for ${ARCHITECTURES[architecture].goarch}`);

    const runtimeDirectory = path.join(vendorDir, ARCHITECTURES[architecture].goarch);
    fs.mkdirSync(runtimeDirectory, { recursive: true });
    const runtimeTarget = path.join(runtimeDirectory, "libonnxruntime.so.1");
    fs.copyFileSync(path.join(directory, `libonnxruntime.so.1`), runtimeTarget);
    fs.chmodSync(runtimeTarget, 0o755);
    console.log(`build: the onnx runtime for ${ARCHITECTURES[architecture].goarch} is at ${runtimeTarget}`);

    const build = spawnSync("go", [
      "build", "-trimpath", "-ldflags=-s -w",
      "-tags", TAG,
      "-o", target, ".",
    ], {
      cwd: deviceDir,
      stdio: "inherit",
      env: {
        ...process.env,
        CGO_ENABLED: "1",
        GOOS: "linux",
        GOARCH: ARCHITECTURES[architecture].goarch,
        CC: ARCHITECTURES[architecture].cc,

        CGO_CFLAGS: `-I${path.join(deviceDir, "wakeword", "ort", "include")} -O2`,

        // The runpath is the runtime's own directory in the package, so the
        // device finds the runtime beside it rather than one that happens
        // to be on the system's library path.
        CGO_LDFLAGS: `-L${directory} -lonnxruntime -Wl,-rpath,$ORIGIN`,
      },
    });
    if (build.status !== 0) {
      console.error(`build: the device for ${ARCHITECTURES[architecture].goarch} did not build`);
      process.exit(1);
    }
    console.log(`build: ${target}`);
  }
}

main().catch((error) => {
  console.error(`build: ${error.message}`);
  process.exit(1);
});
