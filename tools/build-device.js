// Builds the device program.
//
// The device is a separate process: it owns the link to Home Assistant,
// the microphone, the speaker and the wake word. The application renders
// the state it reports.
//
// The binaries are built ahead of time and kept in `vendor/`, one per
// architecture, so the package is self-contained and needs nothing from the
// network at install time.
//
//   npm run build:device
//
// Everything it needs is in this repository, apart from the ONNX Runtime,
// which is fetched for each architecture. The runtime is linked into the
// binary, so a device that has the binary has the wake word: there is
// nothing to install on the device and nothing that can be missing.

const { spawnSync } = require("node:child_process");
const fs = require("node:fs");
const os = require("node:os");
const path = require("node:path");

const root = path.join(__dirname, "..");
const deviceDir = path.join(root, "device");
const vendorDir = path.join(root, "vendor");

// The ONNX Runtime version to link against.
//
// It is the same version for both architectures, and the same version the
// header in device/wakeword/ort/include is written against.
const ORT_VERSION = "1.23.2";

// Where the runtimes are kept between builds. They are big and they are
// the same every time, so they are kept out of the repository.
const cacheDir = path.join(os.homedir(), ".cache", "home-panel", "onnxruntime");

// The architectures a package can be built for, as they appear in the name
// of the packaged directory.
const ARCHITECTURES = [
  {
    package: "x64",
    goarch: "amd64",
    // The name of the upstream release for this architecture.
    release: "onnxruntime-linux-x64",
    // The C compiler to cross-compile with. The runtime is a C library, so
    // cgo is on and a compiler for the target is needed.
    cc: "gcc",
  },
  {
    package: "arm64",
    goarch: "arm64",
    release: "onnxruntime-linux-aarch64",
    cc: "aarch64-linux-gnu-gcc",
  },
];

function fail(message) {
  console.error(`build: ${message}`);
  process.exit(1);
}

/**
 * Fetches the ONNX Runtime for an architecture.
 *
 * The runtime is downloaded once and kept, because it is twenty megabytes
 * and it does not change between builds.
 *
 * @param {{release: string, goarch: string}} architecture the architecture
 * @returns {string} the directory holding the library and the headers
 */
function fetchRuntime(architecture) {
  const target = path.join(cacheDir, `${architecture.release}-${ORT_VERSION}`);
  const library = path.join(target, "lib", "libonnxruntime.so");
  if (fs.existsSync(library)) {
    return target;
  }

  const archive = path.join(cacheDir, `${architecture.release}-${ORT_VERSION}.tgz`);
  fs.mkdirSync(cacheDir, { recursive: true });

  if (!fs.existsSync(archive)) {
    const url = `https://github.com/microsoft/onnxruntime/releases/download/v${ORT_VERSION}/${architecture.release}-${ORT_VERSION}.tgz`;
    console.log(`build: fetching the onnx runtime for ${architecture.goarch}`);
    const fetch = spawnSync("curl", ["-sSL", "--fail", "-o", archive, url], { stdio: "inherit" });
    if (fetch.status !== 0) fail(`the onnx runtime for ${architecture.goarch} could not be fetched`);
  }

  console.log(`build: unpacking the onnx runtime for ${architecture.goarch}`);
  const unpack = spawnSync("tar", ["xzf", archive, "-C", cacheDir], { stdio: "inherit" });
  if (unpack.status !== 0) fail(`the onnx runtime for ${architecture.goarch} could not be unpacked`);

  if (!fs.existsSync(library)) {
    fail(`the onnx runtime for ${architecture.goarch} is not where it was expected: ${library}`);
  }
  return target;
}

fs.mkdirSync(vendorDir, { recursive: true });

for (const architecture of ARCHITECTURES) {
  const runtime = fetchRuntime(architecture);
  const libraryDirectory = path.join(runtime, "lib");
  const includeDirectory = path.join(runtime, "include");
  const target = path.join(vendorDir, `home-panel-device-linux-${architecture.goarch}`);
  console.log(`build: the device for ${architecture.goarch}`);

  // The runtime travels beside the binary.
  //
  // The binary is linked against it and looks for it where it is, so
  // putting it beside the binary is what makes the wake word work. A
  // device that has the binary and not the runtime has a panel that
  // cannot hear its wake word.
  const runtimeLibrary = path.join(libraryDirectory, "libonnxruntime.so.1.23.2");
  // The name is the one the loader looks for: the binary is linked
  // against the soname, and the loader finds a library by its soname and
  // not by the file's name.
  //
  // The two architectures cannot both be called that in one directory,
  // so each is built into its own directory.
  const runtimeDirectory = path.join(vendorDir, architecture.goarch);
  fs.mkdirSync(runtimeDirectory, { recursive: true });
  const runtimeTarget = path.join(runtimeDirectory, "libonnxruntime.so.1");
  if (!fs.existsSync(runtimeLibrary)) {
    fail(`the onnx runtime for ${architecture.goarch} was not found: ${runtimeLibrary}`);
  }
  fs.copyFileSync(runtimeLibrary, runtimeTarget);
  fs.chmodSync(runtimeTarget, 0o755);
  console.log(`build: the onnx runtime for ${architecture.goarch} is at ${runtimeTarget}`);

  // CGO is on, because the ONNX Runtime is a C library and it is linked
  // into the binary. The cross-compiler is the target's own, so the binary
  // is built for the target and not for this machine.
  //
  // The audio reaches libasound through purego, which loads the library at
  // run time rather than linking against it, so the sound system itself
  // needs no cross-compiled library — only the runtime does.
  const build = spawnSync("go", [
    "build", "-trimpath", "-ldflags=-s -w",
    "-o", target, ".",
  ], {
    cwd: deviceDir,
    stdio: "inherit",
    env: {
      ...process.env,
      CGO_ENABLED: "1",
      GOOS: "linux",
      GOARCH: architecture.goarch,
      CC: architecture.cc,
      // The linker needs the runtime's directory to find the library, and
      // the header's directory to compile against. The header the build
      // uses is the one vendored with the package, which is the older API
      // version and is honoured by the newer runtime.
      CGO_CFLAGS: `-I${path.join(deviceDir, "wakeword", "ort", "include")} -I${includeDirectory} -O2`,
      // The runtime is copied beside the binary in the package, so the
      // binary looks for it there rather than on the system's library
      // path. $ORIGIN is where the binary itself is, so the panel finds
      // its wake word with nothing installed on the system.
      CGO_LDFLAGS: `-L${libraryDirectory} -lonnxruntime -Wl,-rpath,$ORIGIN -Wl,-rpath,${path.join(vendorDir, "..", "runtime", architecture.goarch)}`,
    },
  });
  if (build.status !== 0) {
    console.error(`build: the device for ${architecture.goarch} did not build`);
    process.exit(1);
  }
  console.log(`build: ${target}`);
}
